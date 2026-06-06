package app

import (
	"context"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/audit"
)

// llmAuditLogger adapts the append-only audit.Store to the agent's LLMAuditor
// interface, recording every model-provider turn the agent runs as an immutable
// audit entry. It is the egress half of the sovereignty "proof" layer: where the
// tool and permission loggers record what happened locally, this records what
// left the machine and to whom. Like the other loggers it drops a record on
// write failure rather than letting a failing audit log block or break the agent
// loop, and it records only metadata — provider, model, sizes — never the prompt
// or completion text.
type llmAuditLogger struct {
	store *audit.Store
}

// LogLLM records one completed model-provider turn as a TypeLLM audit event.
func (l llmAuditLogger) LogLLM(ctx context.Context, rec agent.LLMAuditRecord) {
	if l.store == nil {
		return
	}
	verb := "sent prompt to"
	if rec.IsError {
		verb = "failed sending prompt to"
	}
	provider := rec.Provider
	if provider == "" {
		provider = "unknown"
	}
	detail := map[string]any{
		"provider":      provider,
		"model":         rec.Model,
		"messages":      rec.Messages,
		"input_tokens":  rec.InputTokens,
		"output_tokens": rec.OutputTokens,
		"error":         rec.IsError,
	}
	if rec.Agent != "" {
		detail["agent"] = rec.Agent
	}
	_, _ = l.store.Append(ctx, audit.Event{
		Type:    audit.TypeLLM,
		Actor:   rec.SessionID,
		Summary: fmt.Sprintf("%s %s/%s", verb, provider, rec.Model),
		Detail:  detail,
	})
}
