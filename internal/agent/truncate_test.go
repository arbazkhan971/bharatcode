package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// toolResultContentFor returns the tool-result content carried in req for the
// given tool_use ID, asserting exactly one such block exists.
func toolResultContentFor(t *testing.T, req llm.Request, toolUseID string) (string, bool) {
	t.Helper()
	var found *message.ToolResultBlock
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolResultBlock); ok && b.ToolUseID == toolUseID {
				rb := b
				require.Nil(t, found, "expected exactly one tool-result block for %s", toolUseID)
				found = &rb
			}
		}
	}
	if found == nil {
		return "", false
	}
	return found.Content, found.IsError
}

// TestRunTruncatesOversizedToolResult proves that a tool returning a result far
// larger than the cap is truncated — with the marker, bounded to ~the cap, and
// preserving head and tail — before it is fed to the next provider turn, while a
// small result on the next call passes through verbatim.
func TestRunTruncatesOversizedToolResult(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	const maxBytes = 1024
	// Distinctive head and tail markers bracket filler that dwarfs the cap, so
	// we can prove both ends survive and the middle is elided.
	const headMarker = "HEAD-MARKER-7c2f"
	const tailMarker = "TAIL-MARKER-9b3e"
	huge := headMarker + " " + strings.Repeat("x", 50_000) + " " + tailMarker

	registry := newFakeRegistry()
	big := &recordingTool{name: "view", result: huge}
	small := &recordingTool{name: "edit", result: "tiny output"}
	registry.Register(big)
	registry.Register(small)

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-big", Name: "view", Input: json.RawMessage(`{"path":"big.txt"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			llm.ToolUseEndEvent{ID: "call-small", Name: "edit", Input: json.RawMessage(`{"path":"big.txt"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 6, OutputTokens: 3}},
		},
	}}

	loop := New(Config{
		Name:               "coder",
		Model:              "fake-model",
		Provider:           provider,
		Tools:              registry,
		Sessions:           repo,
		Bus:                pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt:       "test prompt",
		ToolResultMaxBytes: maxBytes,
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("inspect it")))

	// Three provider turns ran: the truncated big result is visible in the
	// request that drives turn 2 (index 1), the small result in turn 3 (index 2).
	require.Len(t, provider.reqs, 3)

	bigContent, bigIsErr := toolResultContentFor(t, provider.reqs[1], "call-big")
	require.False(t, bigIsErr)

	// The marker is present and reports the real number of dropped bytes: the
	// reported count equals len(huge) minus the bytes actually kept (everything
	// outside the marker), so head + marker + tail reconstructs the original
	// length exactly.
	require.Contains(t, bigContent, "bytes truncated")
	require.Less(t, len(bigContent), len(huge), "result must shrink")
	// Bounded to ~the cap: never larger than cap plus the marker's own length.
	require.LessOrEqual(t, len(bigContent), maxBytes+len(truncateMarker)+8)
	// Both ends survive the truncation.
	require.Contains(t, bigContent, headMarker)
	require.Contains(t, bigContent, tailMarker)
	dropped := reportedDropped(t, bigContent)
	keptOutsideMarker := len(bigContent) - len(formatMarker(dropped))
	require.Equal(t, len(huge), dropped+keptOutsideMarker,
		"reported dropped count plus kept bytes must reconstruct the original length")

	// The small result on the next turn passes through verbatim.
	smallContent, smallIsErr := toolResultContentFor(t, provider.reqs[2], "call-small")
	require.False(t, smallIsErr)
	require.Equal(t, "tiny output", smallContent)
}

// TestTruncateToolResultUnit exercises the pure truncation logic directly across
// the boundary cases the agent loop relies on.
func TestTruncateToolResultUnit(t *testing.T) {
	t.Run("under cap passes through", func(t *testing.T) {
		in := "small"
		require.Equal(t, in, truncateToolResult(in, 1024, false))
	})

	t.Run("exactly at cap passes through", func(t *testing.T) {
		in := strings.Repeat("a", 100)
		require.Equal(t, in, truncateToolResult(in, 100, false))
	})

	t.Run("error result is never truncated", func(t *testing.T) {
		in := strings.Repeat("e", 5000)
		require.Equal(t, in, truncateToolResult(in, 100, true))
	})

	t.Run("non-positive cap passes through", func(t *testing.T) {
		in := strings.Repeat("a", 5000)
		require.Equal(t, in, truncateToolResult(in, 0, false))
		require.Equal(t, in, truncateToolResult(in, -5, false))
	})

	t.Run("oversized keeps head and tail with marker", func(t *testing.T) {
		in := "START" + strings.Repeat("m", 5000) + "END"
		out := truncateToolResult(in, 200, false)
		require.True(t, strings.HasPrefix(out, "START"))
		require.True(t, strings.HasSuffix(out, "END"))
		require.Contains(t, out, "bytes truncated")
		require.LessOrEqual(t, len(out), 200+len(truncateMarker))
	})

	t.Run("does not split multi-byte runes", func(t *testing.T) {
		// Each "世" is 3 bytes; an odd cap would split one without boundary care.
		in := strings.Repeat("世", 1000)
		out := truncateToolResult(in, 301, false)
		require.True(t, utf8.ValidString(out), "truncated output must be valid UTF-8")
	})
}

// formatMarker renders the truncation marker for a given dropped-byte count,
// mirroring the production format so tests can measure its exact length.
func formatMarker(dropped int) string {
	return strings.Replace(truncateMarker, "%d", strconv.Itoa(dropped), 1)
}

// reportedDropped extracts the dropped-byte count the marker advertises in s.
func reportedDropped(t *testing.T, s string) int {
	t.Helper()
	const open = "["
	const close = " bytes truncated]"
	start := strings.Index(s, open)
	end := strings.Index(s, close)
	require.GreaterOrEqual(t, start, 0)
	require.Greater(t, end, start)
	n, err := strconv.Atoi(s[start+len(open) : end])
	require.NoError(t, err)
	return n
}
