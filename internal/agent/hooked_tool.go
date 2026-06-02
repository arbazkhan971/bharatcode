package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

type hookedTool struct {
	inner     tools.Tool
	hooks     hookFirer
	sessionID string
	agentName string
	allowed   bool
}

func (t hookedTool) Name() string {
	return t.inner.Name()
}

func (t hookedTool) Description() string {
	return t.inner.Description()
}

func (t hookedTool) Schema() json.RawMessage {
	return t.inner.Schema()
}

func (t hookedTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	if !t.allowed {
		return tools.Result{
			Content: "tool not allowed for agent: " + t.agentName,
			IsError: true,
		}, nil
	}

	payload := hooks.ToolPayload{
		Tool:      t.Name(),
		Args:      decodeHookArgs(args),
		SessionID: t.sessionID,
	}
	// Guard on a nil firer: the field is an interface, so a Config without Hooks
	// holds a nil interface for which the Engine's nil-receiver guard never runs.
	if t.hooks != nil {
		decision, err := t.hooks.Fire(ctx, hooks.PreToolUse, payload)
		if err != nil {
			return tools.Result{Content: "pre-tool hook failed: " + err.Error(), IsError: true}, nil
		}
		if decision.Block {
			reason := decision.Reason
			if reason == "" {
				reason = "no reason provided"
			}
			return tools.Result{Content: "blocked by hook: " + reason, IsError: true}, nil
		}
	}

	res, runErr := t.inner.Run(ctx, args)
	if t.hooks != nil {
		postPayload := payload
		postPayload.Result = res
		if _, err := t.hooks.Fire(ctx, hooks.PostToolUse, postPayload); err != nil && runErr == nil {
			return tools.Result{Content: "post-tool hook failed: " + err.Error(), IsError: true}, nil
		}
	}
	if runErr != nil {
		return tools.Result{Content: fmt.Sprintf("tool failed: %v", runErr), IsError: true}, nil
	}
	return res, nil
}

func decodeHookArgs(args json.RawMessage) any {
	if len(args) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(args, &decoded); err != nil {
		return string(args)
	}
	return decoded
}
