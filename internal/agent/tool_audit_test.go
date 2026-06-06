package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// fakeToolAuditor captures the records the loop reports so a test can assert
// exactly which tool invocations were audited.
type fakeToolAuditor struct {
	mu      sync.Mutex
	records []ToolAuditRecord
}

func (a *fakeToolAuditor) LogTool(_ context.Context, rec ToolAuditRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, rec)
}

func (a *fakeToolAuditor) snapshot() []ToolAuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ToolAuditRecord, len(a.records))
	copy(out, a.records)
	return out
}

// TestRunAuditsEveryToolInvocation pins the sovereignty proof layer: every tool
// the agent runs — both a successful call and an error result — is reported to
// the configured ToolAuditor, with the tool name, agent, session, and error flag
// the audit log records.
func TestRunAuditsEveryToolInvocation(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", result: "ok"})
	registry.Register(&recordingTool{name: "edit", isError: true, result: "boom"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Working."},
			toolCall("call-1", "view", `{}`),
			toolCall("call-2", "edit", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	auditor := &fakeToolAuditor{}
	loop := New(Config{
		Name:        "coder",
		Model:       "fake-model",
		Provider:    provider,
		Tools:       registry,
		Sessions:    repo,
		Bus:         pubsub.NewTopic[Event]("agent-test", 16),
		ToolAuditor: auditor,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("do two things")))

	records := auditor.snapshot()
	require.Len(t, records, 2, "expected one audit record per tool invocation")

	byTool := map[string]ToolAuditRecord{}
	for _, rec := range records {
		byTool[rec.Tool] = rec
	}

	view, ok := byTool["view"]
	require.True(t, ok, "expected the view invocation to be audited")
	require.False(t, view.IsError)
	require.Equal(t, sessionID, view.SessionID)
	require.Equal(t, "coder", view.Agent)

	edit, ok := byTool["edit"]
	require.True(t, ok, "expected the edit invocation to be audited")
	require.True(t, edit.IsError, "an error result must be recorded as an error")
}

// TestRunAuditsUnknownTool confirms a call to a tool the registry does not know
// is still recorded — as an error — so the proof layer captures attempted, not
// just successful, invocations.
func TestRunAuditsUnknownTool(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("call-1", "ghost", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 1}},
		},
	}}

	auditor := &fakeToolAuditor{}
	loop := New(Config{
		Name:        "coder",
		Model:       "fake-model",
		Provider:    provider,
		Tools:       registry,
		Sessions:    repo,
		Bus:         pubsub.NewTopic[Event]("agent-test", 16),
		ToolAuditor: auditor,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("call a ghost")))

	records := auditor.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "ghost", records[0].Tool)
	require.True(t, records[0].IsError, "an unknown tool must be recorded as an error")
}

// TestRunWithoutAuditorDoesNotPanic confirms tool auditing is opt-in: a Loop with
// no ToolAuditor runs tools normally and records nothing.
func TestRunWithoutAuditorDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "view", result: "ok"})
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			toolCall("call-1", "view", `{}`),
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 1}},
		},
	}}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("agent-test", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("just view")))
}
