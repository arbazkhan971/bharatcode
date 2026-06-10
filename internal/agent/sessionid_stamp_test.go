package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// ctxCaptureTool is a tool whose Run records the session id it observes from
// the context via tools.SessionIDFromContext. The recorded id is compared
// against the session id passed to Loop.Run to prove the loop stamps it.
type ctxCaptureTool struct {
	name string
	mu   sync.Mutex
	seen []string // session ids captured from the context
}

func (t *ctxCaptureTool) Name() string        { return t.name }
func (t *ctxCaptureTool) Description() string { return "captures session id from context for testing" }
func (t *ctxCaptureTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *ctxCaptureTool) Run(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
	sid := tools.SessionIDFromContext(ctx)
	t.mu.Lock()
	t.seen = append(t.seen, sid)
	t.mu.Unlock()
	return tools.Result{Content: "captured:" + sid}, nil
}

// TestRunStampsSessionIDOntoToolContext asserts that Loop.Run stamps the
// session id it receives onto the run context so tools invoked during the turn
// can retrieve it via tools.SessionIDFromContext. The registry is constructed
// with an empty Dependencies.SessionID (mirroring production app.go), so the
// only path by which a tool can see the real session id is through the
// context stamp that Run performs. Deleting the WithSessionID call in Run
// would leave the captured id empty and fail this test.
func TestRunStampsSessionIDOntoToolContext(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Build the registry with an empty SessionID, exactly as app.go does at
	// startup before any session is created.
	capturer := &ctxCaptureTool{name: "probe"}
	registry := newFakeRegistry()
	registry.Register(capturer)

	// The scripted provider emits one tool call to our probe, then a final
	// text reply so the loop terminates cleanly.
	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "probe-1", Name: "probe", Input: json.RawMessage(`{}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-stamp-test", 16),
		SystemPrompt: "test prompt",
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("probe the context")))

	// The probe tool must have run exactly once.
	capturer.mu.Lock()
	captured := capturer.seen
	capturer.mu.Unlock()
	require.Len(t, captured, 1, "probe tool must have been called exactly once")

	// The session id the tool observed from its context must equal the session
	// id that was passed to Run — proving the stamp happened, not the empty
	// string that would result if the stamp were absent.
	require.NotEmpty(t, captured[0], "session id seen from context must not be empty")
	require.Equal(t, sessionID, captured[0],
		"session id stamped by Run must reach the tool's context")
}
