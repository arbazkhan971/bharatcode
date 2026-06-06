package tools

import (
	"context"
	_ "embed"
	"encoding/json"
)

type thinkTool struct{}

type thinkArgs struct {
	Thought string `json:"thought"`
}

var thinkSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["thought"],
  "properties": {
    "thought": {
      "type": "string",
      "minLength": 1,
      "description": "Your reasoning, analysis, or plan. Write out what you know, what you're uncertain about, and what you intend to do next."
    }
  }
}`)

//go:embed think.md
var thinkDescription string

func newThinkTool(_ Dependencies) Tool {
	return &thinkTool{}
}

func (t *thinkTool) Name() string            { return "think" }
func (t *thinkTool) Description() string     { return thinkDescription }
func (t *thinkTool) Schema() json.RawMessage { return copySchema(thinkSchema) }

func (t *thinkTool) Run(_ context.Context, raw json.RawMessage) (Result, error) {
	args, bad := decodeArgs[thinkArgs](raw)
	if bad != nil {
		return *bad, nil
	}
	if args.Thought == "" {
		return errorResult("thought is required"), nil
	}
	return Result{Content: args.Thought}, nil
}
