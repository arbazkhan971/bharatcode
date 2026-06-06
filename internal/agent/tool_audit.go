package agent

import (
	"context"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// ToolAuditRecord describes one completed tool invocation for the audit log. It
// carries only the metadata needed to prove what ran — the tool name, the agent
// and session that ran it, and whether it errored — not the full tool output, so
// the append-only log stays compact and never mirrors source code or command
// output into a second store.
type ToolAuditRecord struct {
	// SessionID is the session the tool ran in.
	SessionID string
	// Agent is the name of the agent loop that invoked the tool (e.g. "coder").
	Agent string
	// Tool is the canonical tool name (e.g. "bash", "edit").
	Tool string
	// IsError reports whether the tool returned an error result.
	IsError bool
}

// ToolAuditor records every tool invocation the agent loop executes. It backs
// BharatCode's sovereignty "proof" layer: alongside the permission decisions
// already captured, it records the tool calls the agent actually performed, so a
// user can later prove exactly what happened on their machine — not just what it
// was authorized to do. Implementations must be safe for concurrent use and must
// never block or fail the loop; a recording failure is dropped, not propagated.
type ToolAuditor interface {
	LogTool(ctx context.Context, rec ToolAuditRecord)
}

// auditTool records a completed tool invocation when an auditor is configured.
// It runs from runTool's deferred path so every outcome — a successful result,
// an error result, an unknown tool, or a recovered panic — is captured uniformly.
func (l *Loop) auditTool(ctx context.Context, sessionID, tool string, result tools.Result) {
	if l.cfg.ToolAuditor == nil {
		return
	}
	l.cfg.ToolAuditor.LogTool(ctx, ToolAuditRecord{
		SessionID: sessionID,
		Agent:     l.name,
		Tool:      tool,
		IsError:   result.IsError,
	})
}
