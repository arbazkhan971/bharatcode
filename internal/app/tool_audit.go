package app

import (
	"context"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/audit"
)

// toolAuditLogger adapts the append-only audit.Store to the agent's ToolAuditor
// interface, recording every tool invocation the agent runs as an immutable
// audit entry. It completes the sovereignty "proof" layer: the permission
// logger already records what the agent was authorized to do, and this records
// what it actually did. Like the permission logger, it drops a record on write
// failure rather than letting a failing audit log block or break the agent loop.
type toolAuditLogger struct {
	store *audit.Store
}

// LogTool records one completed tool invocation as a TypeTool audit event.
func (l toolAuditLogger) LogTool(ctx context.Context, rec agent.ToolAuditRecord) {
	if l.store == nil {
		return
	}
	verb := "ran"
	if rec.IsError {
		verb = "failed"
	}
	detail := map[string]any{
		"tool":  rec.Tool,
		"error": rec.IsError,
	}
	if rec.Agent != "" {
		detail["agent"] = rec.Agent
	}
	_, _ = l.store.Append(ctx, audit.Event{
		Type:    audit.TypeTool,
		Actor:   rec.SessionID,
		Summary: verb + " " + rec.Tool,
		Detail:  detail,
	})
}
