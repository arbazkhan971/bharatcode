package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// promptsToolName is the canonical name of the agent-callable tool that lists
// and renders MCP prompt templates advertised by connected servers.
const promptsToolName = "mcp_prompts"

// maxPromptBytes caps how many bytes of a rendered prompt are returned to the
// model, so a large template cannot blow the context. Renders past the cap are
// truncated with a trailing notice.
const maxPromptBytes = 32 * 1024

// promptProvider is the slice of *Client the prompts tool depends on. The
// narrow interface keeps the tool testable with a fake in unit tests.
type promptProvider interface {
	Prompts() []Prompt
	GetPrompt(ctx context.Context, serverName, name string, args map[string]string) ([]PromptMessage, error)
}

// promptsTool exposes MCP prompt templates to the agent: with no name it lists
// every prompt across connected servers; with a name it renders that prompt's
// messages. It mirrors the mcp_resources tool's "omit the key to list, pass it
// to fetch" shape so the model can discover then render in two calls.
type promptsTool struct {
	provider promptProvider
}

type promptsArgs struct {
	Name      string            `json:"name,omitempty"`
	Server    string            `json:"server,omitempty"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

var schemaPrompts = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string",
      "description": "Name of the MCP prompt to render (as listed by this tool with no arguments). Omit to list every available prompt."
    },
    "server": {
      "type": "string",
      "description": "Server that owns the prompt. Required only to disambiguate when two servers advertise a prompt of the same name."
    },
    "arguments": {
      "type": "object",
      "additionalProperties": {"type": "string"},
      "description": "Values for the prompt's arguments, keyed by argument name. Provide every required argument."
    }
  }
}`)

const promptsToolDescription = `List and render prompt templates exposed by connected MCP servers.

Call with no arguments to list every prompt across all connected MCP servers;
each line is ` + "`server  name — description (args: a, b*)`" + ` where a trailing
` + "`*`" + ` marks a required argument. Then call again with a ` + "`name`" + `
from that list (and any ` + "`arguments`" + ` it needs) to render the prompt's
messages.

Prompts are reusable instruction templates an MCP server publishes (code-review
checklists, doc-generation scaffolds, structured workflows). Each rendered
message is returned with its role and line-numbered text, so you can read the
template and act on it.

Arguments:
- name string, optional: the prompt to render. Omit to list available prompts.
- server string, optional: disambiguates a name shared by two servers.
- arguments object, optional: string values keyed by argument name.

Returns an error when the MCP client is unavailable, the name is unknown or
ambiguous, a required argument is missing, or the server fails the render.`

// newPromptsTool builds the mcp_prompts tool over the given provider.
func newPromptsTool(p promptProvider) tools.Tool {
	return &promptsTool{provider: p}
}

// PromptsToolFor returns the agent-callable mcp_prompts tool bound to c. It is
// nil-safe: when c is nil (no MCP client configured) the returned tool still
// satisfies tools.Tool and reports that prompts are unavailable instead of
// panicking, so callers can register it unconditionally. A typed nil *Client
// must not be passed as a promptProvider directly — its methods would deref a
// nil receiver — so the nil case is funneled to a genuinely nil interface here.
func PromptsToolFor(c *Client) tools.Tool {
	if c == nil {
		return newPromptsTool(nil)
	}
	return newPromptsTool(c)
}

func (t *promptsTool) Name() string { return promptsToolName }

func (t *promptsTool) Description() string { return promptsToolDescription }

func (t *promptsTool) Schema() json.RawMessage {
	return append(json.RawMessage(nil), schemaPrompts...)
}

func (t *promptsTool) Run(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	var args promptsArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return tools.Result{Content: "invalid mcp_prompts arguments: " + err.Error(), IsError: true}, nil
		}
	}
	if t.provider == nil {
		return tools.Result{Content: "mcp prompts are unavailable: no MCP client configured", IsError: true}, nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return t.list(), nil
	}
	return t.render(ctx, name, args), nil
}

// list renders every advertised prompt, sorted by name then server for stable
// output.
func (t *promptsTool) list() tools.Result {
	prompts := t.provider.Prompts()
	if len(prompts) == 0 {
		return tools.Result{Content: "No MCP prompts are currently available."}
	}

	sort.Slice(prompts, func(i, j int) bool {
		if prompts[i].Name != prompts[j].Name {
			return prompts[i].Name < prompts[j].Name
		}
		return prompts[i].Server < prompts[j].Server
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d MCP prompt(s) available:\n", len(prompts))
	for _, p := range prompts {
		b.WriteString(p.Server)
		b.WriteString("  ")
		b.WriteString(p.Name)
		if p.Description != "" {
			b.WriteString(" — ")
			b.WriteString(p.Description)
		}
		if spec := formatPromptArgs(p.Arguments); spec != "" {
			b.WriteString(" ")
			b.WriteString(spec)
		}
		b.WriteString("\n")
	}
	return tools.Result{Content: strings.TrimRight(b.String(), "\n")}
}

// formatPromptArgs renders a prompt's argument list as "(args: a, b*)", with a
// trailing "*" marking each required argument. It returns "" when the prompt
// takes no arguments.
func formatPromptArgs(args []PromptArgument) string {
	if len(args) == 0 {
		return ""
	}
	names := make([]string, 0, len(args))
	for _, a := range args {
		if a.Required {
			names = append(names, a.Name+"*")
		} else {
			names = append(names, a.Name)
		}
	}
	return "(args: " + strings.Join(names, ", ") + ")"
}

// render resolves the server owning name, checks required arguments are present,
// renders the prompt, and returns its messages for the model.
func (t *promptsTool) render(ctx context.Context, name string, args promptsArgs) tools.Result {
	prompt, ok, err := t.resolve(name, strings.TrimSpace(args.Server))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	// When the prompt was found in the advertised list, pre-check required
	// arguments so the model gets a precise "missing X" message instead of an
	// opaque server error. An unlisted prompt (ok == false) is still attempted
	// — the server is the source of truth — but cannot be pre-validated.
	if ok {
		if missing := missingRequiredArgs(prompt.Arguments, args.Arguments); len(missing) > 0 {
			return tools.Result{
				Content: fmt.Sprintf("prompt %q is missing required argument(s): %s", name, strings.Join(missing, ", ")),
				IsError: true,
			}
		}
	}

	messages, err := t.provider.GetPrompt(ctx, prompt.Server, name, args.Arguments)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(messages) == 0 {
		return tools.Result{Content: fmt.Sprintf("Prompt %q rendered no messages.", name)}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "prompt %q on %s (%d message(s)):\n", name, prompt.Server, len(messages))
	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		role := msg.Role
		if role == "" {
			role = "message"
		}
		fmt.Fprintf(&b, "\n[%s]\n%s\n", role, numberResourceLines(msg.Content))
	}

	out := b.String()
	truncated := false
	if len(out) > maxPromptBytes {
		out = out[:maxPromptBytes]
		truncated = true
	}
	if truncated {
		out += fmt.Sprintf("\n… [truncated at %d bytes]", maxPromptBytes)
	}
	return tools.Result{
		Content:  strings.TrimRight(out, "\n"),
		Metadata: map[string]any{"name": name, "server": prompt.Server, "messages": len(messages)},
	}
}

// resolve finds the advertised prompt named name. When server is set it pins the
// lookup to that server. It returns the matched prompt and ok=true when found in
// the advertised list; when no advertised prompt matches but server is set it
// returns a synthetic prompt (ok=false) so the render is still attempted. It
// errors when the name is unknown with no server hint, or ambiguous across
// servers with no server hint.
func (t *promptsTool) resolve(name, server string) (Prompt, bool, error) {
	var matches []Prompt
	for _, p := range t.provider.Prompts() {
		if p.Name != name {
			continue
		}
		if server != "" && p.Server != server {
			continue
		}
		matches = append(matches, p)
	}

	switch {
	case len(matches) == 1:
		return matches[0], true, nil
	case len(matches) > 1:
		servers := make([]string, 0, len(matches))
		for _, m := range matches {
			servers = append(servers, m.Server)
		}
		sort.Strings(servers)
		return Prompt{}, false, fmt.Errorf("prompt %q is ambiguous across servers (%s); set \"server\" to choose one", name, strings.Join(servers, ", "))
	case server != "":
		// Not advertised, but a server was named — attempt the render anyway.
		return Prompt{Server: server, Name: name}, false, nil
	default:
		return Prompt{}, false, fmt.Errorf("unknown mcp prompt %q; call mcp_prompts with no arguments to list available prompts", name)
	}
}

// missingRequiredArgs returns the names of required prompt arguments absent from
// supplied (or present but empty), preserving the prompt's declared order.
func missingRequiredArgs(spec []PromptArgument, supplied map[string]string) []string {
	var missing []string
	for _, a := range spec {
		if !a.Required {
			continue
		}
		if strings.TrimSpace(supplied[a.Name]) == "" {
			missing = append(missing, a.Name)
		}
	}
	return missing
}
