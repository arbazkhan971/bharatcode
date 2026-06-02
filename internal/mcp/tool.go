package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

type toolAdapter struct {
	server      *Server
	perms       *permission.Checker
	name        string
	remoteName  string
	description string
	schema      json.RawMessage
}

func (t *toolAdapter) Name() string {
	return t.name
}

func (t *toolAdapter) Description() string {
	return t.description
}

func (t *toolAdapter) Schema() json.RawMessage {
	return append(json.RawMessage(nil), t.schema...)
}

func (t *toolAdapter) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	var decoded map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &decoded); err != nil {
			return tools.Result{Content: "invalid tool arguments: " + err.Error(), IsError: true}, nil
		}
	}
	if decoded == nil {
		decoded = map[string]any{}
	}

	if t.perms != nil {
		decision, err := t.perms.Check(ctx, permission.Request{
			ToolName: t.name,
			Args:     decoded,
		})
		if err != nil {
			return tools.Result{Content: "permission check failed: " + err.Error(), IsError: true}, nil
		}
		if decision == permission.DecisionDeny {
			return tools.Result{Content: "permission denied for " + t.name, IsError: true}, nil
		}
	}

	state, conn := t.server.snapshot()
	if state != StateConnected || conn == nil {
		return tools.Result{
			Content: "mcp server disconnected: " + t.server.Name(),
			IsError: true,
		}, ErrToolUnavailable
	}

	ctx, cancel := withServerTimeout(ctx, t.server.cfg.Timeout)
	defer cancel()

	result, err := conn.CallTool(ctx, mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name:      t.remoteName,
			Arguments: decoded,
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tools.Result{}, fmt.Errorf("calling mcp tool %q: %w", t.name, err)
		}
		return tools.Result{Content: err.Error(), IsError: true}, nil
	}

	out := tools.Result{
		Content: contentText(result.Content),
		IsError: result.IsError,
	}
	if result.StructuredContent != nil {
		out.Metadata = map[string]any{"structured_content": result.StructuredContent}
	}
	t.server.logger.DebugContext(ctx, "MCP tool call completed", "tool", t.name, "is_error", out.IsError)
	return out, nil
}

func contentText(content []mcpsdk.Content) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch v := item.(type) {
		case mcpsdk.TextContent:
			parts = append(parts, v.Text)
		case mcpsdk.ImageContent:
			parts = append(parts, "[image: "+v.MIMEType+"]")
		case mcpsdk.AudioContent:
			parts = append(parts, "[audio: "+v.MIMEType+"]")
		case mcpsdk.EmbeddedResource:
			data, mimeType, err := resourceBytes(v.Resource)
			if err != nil {
				parts = append(parts, "[resource: "+err.Error()+"]")
			} else if mimeType != "" {
				parts = append(parts, "[resource "+mimeType+"]\n"+string(data))
			} else {
				parts = append(parts, string(data))
			}
		case mcpsdk.ResourceLink:
			parts = append(parts, "[resource link: "+v.URI+"]")
		default:
			slog.Debug("Unknown MCP content type", "type", fmt.Sprintf("%T", item))
		}
	}
	return strings.Join(parts, "\n")
}
