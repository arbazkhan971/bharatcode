package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/skills"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// skillToolName is the canonical name of the skill-loading tool.
const skillToolName = "skill"

// skillToolSchema is the JSON Schema for the skill tool's arguments.
var skillToolSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "The name of the skill to load, as listed under \"Available skills\"."
    }
  },
  "required": ["name"]
}`)

// skillTool lets the agent lazily load a skill's full SKILL.md body on
// demand. The system prompt advertises only each skill's name and
// description; calling this tool with a skill name injects that skill's
// complete instruction body into the conversation as the tool result, so
// the base prompt stays lean while the full instructions are available
// when the model decides to invoke a skill.
type skillTool struct {
	set *skills.SkillSet
}

// newSkillTool constructs a skillTool over the given skill set. The set
// may be nil or empty; the tool is still registered so agents can call
// it (and receive a clear "unknown skill" result), which keeps tool
// construction and allow-list validation stable regardless of how many
// skills are installed.
func newSkillTool(set *skills.SkillSet) *skillTool {
	return &skillTool{set: set}
}

// Name returns the canonical tool name used in tool-call requests.
func (t *skillTool) Name() string {
	return skillToolName
}

// Description returns the markdown description shown to the model.
func (t *skillTool) Description() string {
	return "Load the full instructions for a skill by name. The system prompt lists " +
		"available skills with only a one-line summary; call this tool with a skill's " +
		"name to retrieve its complete SKILL.md body before following its instructions."
}

// Schema returns the JSON Schema for the tool's arguments.
func (t *skillTool) Schema() json.RawMessage {
	return append(json.RawMessage(nil), skillToolSchema...)
}

// skillArgs is the decoded argument shape for the skill tool.
type skillArgs struct {
	Name string `json:"name"`
}

// Run resolves the named skill and returns its body as the tool result.
// An unknown or unnamed skill yields an error result rather than a Go
// error, so the agent loop can surface it to the model and continue.
func (t *skillTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	var decoded skillArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &decoded); err != nil {
			return tools.Result{Content: "invalid skill arguments: " + err.Error(), IsError: true}, nil
		}
	}
	name := strings.TrimSpace(decoded.Name)
	if name == "" {
		return tools.Result{Content: "skill name is required", IsError: true}, nil
	}
	skill, ok := t.set.Get(name)
	if !ok {
		return tools.Result{Content: fmt.Sprintf("unknown skill: %s", name), IsError: true}, nil
	}
	body := strings.TrimSpace(skill.Body)
	if body == "" {
		return tools.Result{Content: fmt.Sprintf("skill %q has no instructions", name), IsError: true}, nil
	}
	return tools.Result{Content: body}, nil
}
