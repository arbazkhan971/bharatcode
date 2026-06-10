package chat

// Tests for TruncateStreamTail, the rollback primitive the agent-event handler
// uses to reconcile provisional streamed deltas against the canonical response
// text (and to rewind a failed attempt's partial text before a retry).

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// TestTruncateStreamTail_ReconcileMatchesCanonical replays the exact sequence
// the TUI performs on EventLLMResponse: stream deltas (rendering after each so
// the incremental prefix cache is populated), truncate them all, append the
// canonical text, finish. The result must be byte-identical to rendering the
// canonical text directly.
func TestTruncateStreamTail_ReconcileMatchesCanonical(t *testing.T) {
	t.Parallel()

	deltas := []string{"## Head", "ing\n\nFirst para", "graph with **bold**.\n\n", "Second paragraph."}
	canonical := "## Heading\n\nFirst paragraph with **bold**.\n\nSecond paragraph.\n"

	ref := New()
	ref.EnableMarkdown("dark")
	ref.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: canonical}},
	})
	fullRender := ref.Render(80)

	l := New()
	l.EnableMarkdown("dark")
	streamed := 0
	for _, d := range deltas {
		l.Stream("s1", d)
		streamed += len(d)
		_ = l.Render(80)
	}
	l.TruncateStreamTail("s1", streamed)
	l.Stream("s1", canonical)
	l.FinishStream("s1")

	require.Equal(t, stripANSI(fullRender), stripANSI(l.Render(80)),
		"delta-reconcile must converge on the canonical render")
}

// TestTruncateStreamTail_RewindThenRestream models a retried provider attempt:
// partial text is streamed, fully rewound, and the second attempt streams the
// complete text. No residue of the first attempt may survive.
func TestTruncateStreamTail_RewindThenRestream(t *testing.T) {
	t.Parallel()

	l := New()
	partial := "An answer that fails mid-"
	l.Stream("s1", partial)
	_ = l.Render(80)
	l.TruncateStreamTail("s1", len(partial))

	full := "A different answer from the retry."
	l.Stream("s1", full)
	l.FinishStream("s1")

	out := stripANSI(l.Render(80))
	require.Contains(t, out, full)
	require.NotContains(t, out, "fails mid-")
}

// TestTruncateStreamTail_ResetsPrefixCache verifies that truncating past the
// cached stable-prefix boundary clears the incremental cache, so a stale
// prefix render can never resurface text the truncate removed.
func TestTruncateStreamTail_ResetsPrefixCache(t *testing.T) {
	t.Parallel()

	l := New()
	l.EnableMarkdown("dark")
	// Two closed paragraphs: the blank line gives the renderer a stable
	// boundary, so rendering caches a non-empty prefix.
	l.Stream("s1", "First paragraph.\n\nSecond paragraph grows")
	_ = l.Render(80)
	idx := l.index["s1"]
	require.Positive(t, l.items[idx].streamPrefixSrcLen, "test premise: prefix cache populated")

	// Truncate back into the first paragraph — shorter than the cached prefix.
	l.TruncateStreamTail("s1", len(l.items[idx].body)-5)
	require.Zero(t, l.items[idx].streamPrefixSrcLen)
	require.Empty(t, l.items[idx].streamPrefixAnsi)

	out := stripANSI(l.Render(80))
	require.NotContains(t, out, "Second paragraph")
}

// TestTruncateStreamTail_ClampAndNoops covers the defensive edges: n larger
// than the body clamps to empty, n <= 0 and unknown ids change nothing.
func TestTruncateStreamTail_ClampAndNoops(t *testing.T) {
	t.Parallel()

	l := New()
	l.Stream("s1", "abc")

	l.TruncateStreamTail("missing", 2) // unknown id: no-op
	l.TruncateStreamTail("s1", 0)      // n == 0: no-op
	l.TruncateStreamTail("s1", -4)     // n < 0: no-op
	require.Equal(t, "abc", l.items[l.index["s1"]].body)

	l.TruncateStreamTail("s1", 99) // clamps to the whole body
	require.Empty(t, l.items[l.index["s1"]].body)
}
