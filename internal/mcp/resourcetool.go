package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// resourcesToolName is the canonical name of the agent-callable tool that lists
// and reads MCP resources advertised by connected servers.
const resourcesToolName = "mcp_resources"

// maxResourceBytes caps how many bytes of a single resource are rendered into a
// tool result, so a large file cannot blow the model's context. Reads past the
// cap are truncated with a trailing notice.
const maxResourceBytes = 32 * 1024

// resourceProvider is the slice of *Client the resources tool depends on. The
// narrow interface keeps the tool testable with a fake in unit tests.
type resourceProvider interface {
	Resources() []Resource
	ReadResource(ctx context.Context, uri string) ([]byte, string, error)
}

// resourcesTool exposes MCP resources to the agent: with no uri it lists every
// resource across connected servers; with a uri it reads that resource's
// contents. It mirrors the diagnostics tool's "omit the path to list, pass it to
// inspect" shape so the model can discover then fetch in two calls.
type resourcesTool struct {
	provider resourceProvider
}

type resourcesArgs struct {
	URI string `json:"uri,omitempty"`
}

var schemaResources = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "uri": {
      "type": "string",
      "description": "URI of the MCP resource to read (as listed by this tool with no arguments). Omit to list every available resource."
    }
  }
}`)

const resourcesToolDescription = `List and read resources exposed by connected MCP servers.

Call with no arguments to list every resource across all connected MCP servers;
each line is ` + "`server  uri  name — description [mime]`" + `. Then call again
with a ` + "`uri`" + ` from that list to read the resource's contents.

Resources are read-only context an MCP server publishes (documents, schemas,
logs, live data). Text contents are returned line-numbered like the view tool;
binary contents are summarized by size and MIME type instead of dumped.

Arguments:
- uri string, optional: the resource URI to read. Omit to list available
  resources.

Returns an error when the MCP client is unavailable, the URI names an unknown or
disconnected server, or the server fails the read.`

// newResourcesTool builds the mcp_resources tool over the given provider.
func newResourcesTool(p resourceProvider) tools.Tool {
	return &resourcesTool{provider: p}
}

// ResourcesToolFor returns the agent-callable mcp_resources tool bound to c. It
// is nil-safe: when c is nil (no MCP client configured) the returned tool still
// satisfies tools.Tool and reports that resources are unavailable instead of
// panicking, so callers can register it unconditionally. A typed nil *Client
// must not be passed as a resourceProvider directly — its methods would deref a
// nil receiver — so the nil case is funneled to a genuinely nil interface here.
func ResourcesToolFor(c *Client) tools.Tool {
	if c == nil {
		return newResourcesTool(nil)
	}
	return newResourcesTool(c)
}

func (t *resourcesTool) Name() string { return resourcesToolName }

func (t *resourcesTool) Description() string { return resourcesToolDescription }

func (t *resourcesTool) Schema() json.RawMessage {
	return append(json.RawMessage(nil), schemaResources...)
}

func (t *resourcesTool) Run(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	var args resourcesArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return tools.Result{Content: "invalid mcp_resources arguments: " + err.Error(), IsError: true}, nil
		}
	}
	if t.provider == nil {
		return tools.Result{Content: "mcp resources are unavailable: no MCP client configured", IsError: true}, nil
	}

	uri := strings.TrimSpace(args.URI)
	if uri == "" {
		return t.list(), nil
	}
	return t.read(ctx, uri), nil
}

// list renders every advertised resource, sorted by URI for stable output.
func (t *resourcesTool) list() tools.Result {
	resources := t.provider.Resources()
	if len(resources) == 0 {
		return tools.Result{Content: "No MCP resources are currently available."}
	}

	sort.Slice(resources, func(i, j int) bool {
		if resources[i].URI != resources[j].URI {
			return resources[i].URI < resources[j].URI
		}
		return resources[i].Server < resources[j].Server
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d MCP resource(s) available:\n", len(resources))
	for _, r := range resources {
		b.WriteString(r.Server)
		b.WriteString("  ")
		b.WriteString(r.URI)
		if r.Name != "" {
			b.WriteString("  ")
			b.WriteString(r.Name)
		}
		if r.Description != "" {
			b.WriteString(" — ")
			b.WriteString(r.Description)
		}
		if r.MimeType != "" {
			b.WriteString(" [")
			b.WriteString(r.MimeType)
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return tools.Result{Content: strings.TrimRight(b.String(), "\n")}
}

// read fetches a single resource and renders its contents for the model.
func (t *resourcesTool) read(ctx context.Context, uri string) tools.Result {
	data, mimeType, err := t.provider.ReadResource(ctx, uri)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(data) == 0 {
		return tools.Result{Content: fmt.Sprintf("Resource %q is empty.", uri)}
	}

	// Binary payloads (images, archives, anything non-UTF-8) are not useful as
	// inline text; summarize them rather than corrupting the model's context.
	if !utf8.Valid(data) {
		label := mimeType
		if label == "" {
			label = "application/octet-stream"
		}
		return tools.Result{
			Content:  fmt.Sprintf("[binary resource %s, %d bytes — not shown]", label, len(data)),
			Metadata: map[string]any{"uri": uri, "mime_type": mimeType, "bytes": len(data)},
		}
	}

	text := string(data)
	truncated := false
	if len(text) > maxResourceBytes {
		text = text[:maxResourceBytes]
		// Avoid splitting a multi-byte rune at the cut boundary.
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		truncated = true
	}

	var b strings.Builder
	if mimeType != "" {
		fmt.Fprintf(&b, "%s [%s]\n", uri, mimeType)
	} else {
		fmt.Fprintf(&b, "%s\n", uri)
	}
	b.WriteString(numberResourceLines(text))
	if truncated {
		fmt.Fprintf(&b, "\n… [truncated at %d bytes]", maxResourceBytes)
	}
	return tools.Result{
		Content:  b.String(),
		Metadata: map[string]any{"uri": uri, "mime_type": mimeType},
	}
}

// numberResourceLines prefixes each line with a right-aligned 1-based line
// number, matching the view tool's "N | text" rendering so the model reads
// resource contents in the same shape as file contents.
func numberResourceLines(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	width := len(fmt.Sprintf("%d", len(lines)))
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%*d | %s", width, i+1, line)
	}
	return b.String()
}
