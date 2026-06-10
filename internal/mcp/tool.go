package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// progressSeq supplies a process-unique suffix for per-call progress tokens so
// concurrent and successive tool calls each carry a distinct token, letting a
// server's notifications/progress updates be correlated with the right call.
var progressSeq atomic.Uint64

// newProgressToken returns a string progress token unique within this process
// for the given tool. A string token sidesteps the float64 ambiguity numeric
// JSON tokens acquire over the wire.
func newProgressToken(tool string) string {
	return tool + "#" + strconv.FormatUint(progressSeq.Add(1), 10)
}

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
			// Carry the active turn's session id (stamped on ctx by the agent loop)
			// so session-scoped --yolo bypasses MCP tool calls and a session grant
			// keys under the real session rather than "".
			SessionID: tools.SessionIDFromContext(ctx),
			Args:      decoded,
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

	// Attach a progress token so the server may emit notifications/progress for
	// this call; the client routes those updates to the tool-progress handler,
	// keyed by this token. The server is not obligated to honor it.
	result, err := conn.CallTool(ctx, mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name:      t.remoteName,
			Arguments: decoded,
			Meta:      &mcpsdk.Meta{ProgressToken: newProgressToken(t.name)},
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
