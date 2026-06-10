package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestToolCallArgsAreRepaired asserts the streaming JSON repair is wired into the
// tool-call argument parse path: a model that streams tool arguments with a raw
// control character (illegal inside a JSON string) and a truncated tail still
// reaches the tool with decodable JSON instead of failing the call outright.
func TestToolCallArgsAreRepaired(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	tool := &recordingTool{name: "edit", result: "edited"}
	registry := newFakeRegistry()
	registry.Register(tool)

	// A raw newline inside the string value and a missing closing brace — both
	// rejected by encoding/json — are exactly what RepairToolCallJSON rewrites.
	broken := json.RawMessage("{\"path\":\"a\nb\"")

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: broken},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("agent-repair-test", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit it")))

	tool.mu.Lock()
	require.Len(t, tool.calls, 1, "the tool must have run once with repaired args")
	got := tool.calls[0]
	tool.mu.Unlock()

	// The arguments the tool received must be valid JSON and carry the path.
	var args struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &args), "repaired args must be valid JSON: %q", got)
	require.Equal(t, "a\nb", args.Path)
}
