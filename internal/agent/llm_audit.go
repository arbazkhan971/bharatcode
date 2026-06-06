package agent

import (
	"context"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// LLMAuditRecord describes one completed model-provider turn for the audit log.
// It is the egress record at the heart of BharatCode's sovereignty "proof"
// layer: it names exactly which provider and model the agent's prompt was sent
// to, how large the exchange was, and whether it failed — so a user can later
// prove what (if anything) left the machine. Like ToolAuditRecord it carries
// only metadata, never the prompt or completion text, so the append-only log
// stays compact and never mirrors source code into a second store.
type LLMAuditRecord struct {
	// SessionID is the session the request was made in.
	SessionID string
	// Agent is the name of the agent loop that made the request (e.g. "coder").
	Agent string
	// Provider is the model provider the request was sent to (e.g. "anthropic",
	// "ollama"). It is the destination of the egress.
	Provider string
	// Model is the model the request targeted (e.g. "claude-opus-4-8").
	Model string
	// Messages is the number of conversation messages sent in the request.
	Messages int
	// InputTokens and OutputTokens are the provider-reported token counts, zero
	// when the provider did not report usage (e.g. a failed call).
	InputTokens  int
	OutputTokens int
	// IsError reports whether the provider turn ultimately failed.
	IsError bool
}

// LLMAuditor records every model-provider turn the agent loop executes. It
// complements ToolAuditor: where the tool auditor records what ran locally, the
// LLM auditor records what was sent off the machine and to whom — the one event
// the sovereignty story most depends on. Implementations must be safe for
// concurrent use and must never block or fail the loop; a recording failure is
// dropped, not propagated.
type LLMAuditor interface {
	LogLLM(ctx context.Context, rec LLMAuditRecord)
}

// auditLLM records a completed provider turn when an auditor is configured. It
// is called once per turn after callProviderWithRetry returns, so both
// successful and failed turns are captured uniformly. usage is nil when the
// provider reported no token counts (typically a failure).
func (l *Loop) auditLLM(ctx context.Context, sessionID string, history []message.Message, usage *llm.Usage, callErr error) {
	if l.cfg.LLMAuditor == nil {
		return
	}
	rec := LLMAuditRecord{
		SessionID: sessionID,
		Agent:     l.name,
		Model:     l.activeModel,
		Messages:  len(history),
		IsError:   callErr != nil,
	}
	if l.cfg.Provider != nil {
		rec.Provider = l.cfg.Provider.Name()
	}
	if usage != nil {
		rec.InputTokens = usage.InputTokens
		rec.OutputTokens = usage.OutputTokens
	}
	l.cfg.LLMAuditor.LogLLM(ctx, rec)
}
