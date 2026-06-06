package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// fakeLLMAuditor captures the records the loop reports so a test can assert
// exactly which provider turns were audited.
type fakeLLMAuditor struct {
	mu      sync.Mutex
	records []LLMAuditRecord
}

func (a *fakeLLMAuditor) LogLLM(_ context.Context, rec LLMAuditRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, rec)
}

func (a *fakeLLMAuditor) snapshot() []LLMAuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]LLMAuditRecord, len(a.records))
	copy(out, a.records)
	return out
}

// TestRunAuditsEveryProviderTurn pins the egress half of the sovereignty proof
// layer: every model-provider turn the agent makes is reported to the configured
// LLMAuditor, with the destination provider/model and the reported token usage.
func TestRunAuditsEveryProviderTurn(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", result: "ok"})

	// Two provider turns: the first emits a tool call, the second the final text.
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("call-1", "view", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	auditor := &fakeLLMAuditor{}
	loop := New(Config{
		Name:       "coder",
		Model:      "fake-model",
		Provider:   provider,
		Tools:      registry,
		Sessions:   repo,
		Bus:        pubsub.NewTopic[Event]("agent-test", 16),
		LLMAuditor: auditor,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("do a thing")))

	records := auditor.snapshot()
	require.Len(t, records, 2, "expected one audit record per provider turn")

	for _, rec := range records {
		require.Equal(t, "fake", rec.Provider, "the egress destination must be recorded")
		require.Equal(t, "fake-model", rec.Model)
		require.Equal(t, sessionID, rec.SessionID)
		require.Equal(t, "coder", rec.Agent)
		require.False(t, rec.IsError)
		require.Positive(t, rec.Messages, "the request message count must be recorded")
	}

	require.Equal(t, 10, records[0].InputTokens)
	require.Equal(t, 5, records[0].OutputTokens)
	require.Equal(t, 8, records[1].InputTokens)
	require.Equal(t, 4, records[1].OutputTokens)
}

// TestRunAuditsFailedProviderTurn confirms a provider failure is still recorded —
// the proof layer must capture attempted egress, not just successful exchanges.
func TestRunAuditsFailedProviderTurn(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// A non-retryable provider error fails the turn on the first attempt.
	provider := &alwaysErrorProvider{err: fmt.Errorf("provider rejected the request")}

	auditor := &fakeLLMAuditor{}
	loop := New(Config{
		Name:       "coder",
		Model:      "fake-model",
		Provider:   provider,
		Tools:      newFakeRegistry(),
		Sessions:   repo,
		Bus:        pubsub.NewTopic[Event]("agent-test", 16),
		LLMAuditor: auditor,
	})

	// The run fails, but the egress attempt must still have been audited.
	_ = loop.Run(ctx, sessionID, userMessage("trigger a failure"))

	records := auditor.snapshot()
	require.Len(t, records, 1)
	require.True(t, records[0].IsError, "a failed provider turn must be recorded as an error")
	require.Equal(t, "fake", records[0].Provider)
	require.Equal(t, "fake-model", records[0].Model)
}

// TestRunWithoutLLMAuditorDoesNotPanic confirms LLM auditing is opt-in: a Loop
// with no LLMAuditor runs normally and records nothing.
func TestRunWithoutLLMAuditorDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 1}},
		},
	}}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    newFakeRegistry(),
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("agent-test", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("hi")))
}
