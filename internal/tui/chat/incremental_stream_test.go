package chat

// Tests for the incremental streaming markdown render path.
//
// Three properties are verified:
//  (a) Streaming a message in N deltas produces the SAME final visible output
//      as a single full glamour render of the complete body.
//  (b) A message with an open code fence (no safe boundary) falls back to the
//      plain-text streaming path and still produces correct, non-empty output.
//  (c) The stable prefix is actually reused: when only the tail grows, the
//      prefix render counter stays constant across additional deltas.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// ── (a) Incremental output matches full render ─────────────────────────────

// TestIncrementalStream_MatchesFullRender sends a multi-paragraph markdown body
// as small character-level deltas and verifies that, once FinishStream is
// called, the final rendered output is identical to a single full-render of the
// complete body.
func TestIncrementalStream_MatchesFullRender(t *testing.T) {
	t.Parallel()

	// A body with multiple closed markdown blocks separated by blank lines.
	body := "## Introduction\n\nThis is the first paragraph with **bold** and *italic* text.\n\n" +
		"## Details\n\nHere is a fenced code block:\n\n```go\nfmt.Println(\"hello\")\n```\n\n" +
		"And a final paragraph with a [link](https://example.com).\n"

	// Build the "reference" render: full body at once, no streaming.
	ref := New()
	ref.EnableMarkdown("dark")
	ref.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: body}},
	})
	fullRender := ref.Render(80)

	// Build the "streaming" render: send one character at a time.
	streamed := New()
	streamed.EnableMarkdown("dark")
	for _, ch := range body {
		streamed.Stream("s1", string(ch))
		// Force a render on each delta (as the TUI viewport would do) so the
		// incremental cache is exercised at every step.
		_ = streamed.Render(80)
	}
	streamed.FinishStream("s1")
	streamedFinal := streamed.Render(80)

	// After FinishStream the canonical full render fires, so the output must
	// be byte-identical to the reference (ignoring the streaming cursor which
	// is gone after finish).
	require.Equal(t, stripANSI(fullRender), stripANSI(streamedFinal),
		"incremental streaming must produce the same visible output as a full render")
}

// TestIncrementalStream_LargerDeltas verifies the same identity property when
// deltas arrive as whole sentences rather than character-by-character.
func TestIncrementalStream_LargerDeltas(t *testing.T) {
	t.Parallel()

	sentences := []string{
		"# Heading\n\n",
		"First paragraph text.\n\n",
		"Second paragraph with `inline code`.\n\n",
		"```python\nprint('hi')\n```\n\n",
		"Third paragraph after the code block.\n",
	}

	body := strings.Join(sentences, "")

	// Reference: full render.
	ref := New()
	ref.EnableMarkdown("dark")
	ref.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: body}},
	})
	fullRender := ref.Render(80)

	// Incremental streaming.
	streamed := New()
	streamed.EnableMarkdown("dark")
	for _, s := range sentences {
		streamed.Stream("s1", s)
		_ = streamed.Render(80)
	}
	streamed.FinishStream("s1")
	streamedFinal := streamed.Render(80)

	require.Equal(t, stripANSI(fullRender), stripANSI(streamedFinal),
		"sentence-level streaming must produce the same visible output as a full render")
}

// ── (b) Open code fence falls back correctly ───────────────────────────────

// TestIncrementalStream_OpenFenceFallback streams a message that has an open
// code fence with NO preceding blank line (so stablePrefixBoundary returns 0
// for the whole body) and checks:
//   - The render does not panic or return empty output.
//   - The visible text (ANSI stripped) is present and correct.
//   - After FinishStream the final render is non-empty.
//
// A body whose ONLY content is an open fence forces the boundary to zero,
// exercising the plain-text fallback code path.
func TestIncrementalStream_OpenFenceFallback(t *testing.T) {
	t.Parallel()

	// A body that is entirely an open fenced code block with no preceding
	// blank lines — stablePrefixBoundary must return 0 because every
	// "\n\n" (if any) is inside the open fence.
	// We use a body that starts immediately with an open fence so there is
	// no stable prefix at all.
	onlyOpenFence := "```go\nfmt.Println(\"still open\"\n"

	// Confirm the helper returns 0 for this body.
	require.Equal(t, 0, stablePrefixBoundary(onlyOpenFence),
		"a body that starts with an open fence must have boundary 0")

	l := New()
	l.EnableMarkdown("dark")

	deltas := []string{
		"```go\n",
		"fmt.Println(\"still open\"\n",
	}
	for _, d := range deltas {
		l.Stream("s1", d)
	}

	// Must render without panic and contain the visible text (plain-text path).
	out := l.Render(80)
	plain := stripANSI(out)
	require.Contains(t, plain, "still open",
		"fallback render must still show the body text; got:\n%s", plain)

	// The streaming cursor must still be visible (we haven't finished).
	require.Contains(t, out, "▌", "streaming cursor must be present during open-fence stream")

	// After finishing, we get the final render — must still be non-empty.
	l.FinishStream("s1")
	finished := l.Render(80)
	require.NotEmpty(t, stripANSI(finished), "finished open-fence message must render non-empty output")
}

// TestIncrementalStream_NoBlankLineFallback ensures a message with no blank
// lines (stablePrefixBoundary == 0) falls back to the plain path and still
// shows the text content with the streaming cursor.
func TestIncrementalStream_NoBlankLineFallback(t *testing.T) {
	t.Parallel()

	l := New()
	l.EnableMarkdown("dark")
	l.Stream("s1", "Single line without any blank lines yet.")

	out := l.Render(80)
	require.Contains(t, stripANSI(out), "Single line",
		"no-blank-line body must still render its text")
	require.Contains(t, out, "▌", "streaming cursor must be present")
}

// ── (c) Stable prefix is actually reused ──────────────────────────────────

// TestIncrementalStream_PrefixReuse instruments the render-region counter to
// verify that once a stable prefix has been rendered and cached, subsequent
// deltas that only grow the tail do NOT re-render the prefix. The item render
// counter (RenderRegions) will still increment on each delta (the top-level
// cachedBody is always invalidated by Stream), but we can directly inspect the
// item's streamPrefixSrcLen field to confirm the cached boundary doesn't move
// once it's set, and that the streamPrefixAnsi is populated.
//
// We also verify that adding tail-only text doesn't change streamPrefixSrcLen.
func TestIncrementalStream_PrefixReuse(t *testing.T) {
	t.Parallel()

	l := New()
	l.EnableMarkdown("dark")

	// Build up a body with a stable prefix (two paragraphs with a blank line
	// between them) and then keep appending tail text.
	l.Stream("s1", "# Paragraph One\n\nThis paragraph is complete and closed.\n\n")
	_ = l.Render(80)

	// After this render the prefix boundary should be set.
	it := &l.items[l.index["s1"]]
	firstBoundary := it.streamPrefixSrcLen
	require.Greater(t, firstBoundary, 0,
		"after a render with a stable blank-line boundary, streamPrefixSrcLen must be > 0")
	require.NotEmpty(t, it.streamPrefixAnsi,
		"streamPrefixAnsi must be populated once a prefix is cached")
	firstAnsi := it.streamPrefixAnsi

	// Now append tail text (no new blank lines → boundary stays at firstBoundary).
	for i := 0; i < 5; i++ {
		l.Stream("s1", fmt.Sprintf("tail word %d ", i))
		_ = l.Render(80)
		require.Equal(t, firstBoundary, it.streamPrefixSrcLen,
			"streamPrefixSrcLen must not change when tail-only text is added (iteration %d)", i)
		require.Equal(t, firstAnsi, it.streamPrefixAnsi,
			"streamPrefixAnsi must not change when tail-only text is added (iteration %d)", i)
	}
}

// TestIncrementalStream_PrefixAdvancesWhenNewBlankLine checks that when a new
// blank line appears in the tail, stablePrefixBoundary advances, the prefix
// cache is extended, and the new streamPrefixAnsi differs from the old one.
func TestIncrementalStream_PrefixAdvancesWhenNewBlankLine(t *testing.T) {
	t.Parallel()

	l := New()
	l.EnableMarkdown("dark")

	// First stable prefix.
	l.Stream("s1", "# First\n\nParagraph one complete.\n\n")
	_ = l.Render(80)

	it := &l.items[l.index["s1"]]
	firstBoundary := it.streamPrefixSrcLen
	firstAnsi := it.streamPrefixAnsi
	require.Greater(t, firstBoundary, 0)

	// Add a second paragraph with a new blank line.
	l.Stream("s1", "# Second\n\nParagraph two complete.\n\n")
	_ = l.Render(80)

	require.Greater(t, it.streamPrefixSrcLen, firstBoundary,
		"stable prefix boundary must advance when a new blank line is added to the tail")
	require.NotEqual(t, firstAnsi, it.streamPrefixAnsi,
		"streamPrefixAnsi must be updated when the boundary advances")
}

// ── boundary helper unit tests ─────────────────────────────────────────────

// TestStablePrefixBoundary_BasicCases exercises the boundary finder directly.
func TestStablePrefixBoundary_BasicCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		wantGT   int // boundary must be > wantGT (0 = any positive value is fine)
		wantZero bool
	}{
		{
			name:   "two paragraphs",
			src:    "Para one.\n\nPara two.\n",
			wantGT: 0,
		},
		{
			name:     "no blank lines",
			src:      "No blank lines here.",
			wantZero: true,
		},
		{
			name:     "blank line inside open fence",
			src:      "```go\nfunc f() {\n\n  return\n}\n",
			wantZero: true,
		},
		{
			name:   "closed fence then blank line",
			src:    "```go\nfmt.Println()\n```\n\nParagraph after fence.\n",
			wantGT: 0,
		},
		{
			name:   "multiple blank lines returns latest safe one",
			src:    "A.\n\nB.\n\nC.\n",
			wantGT: 5, // boundary is after the second "\n\n"
		},
		{
			name:     "entire body is open fence",
			src:      "```\nsome code\n",
			wantZero: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stablePrefixBoundary(tc.src)
			if tc.wantZero {
				require.Equal(t, 0, got,
					"expected zero boundary for %q, got %d", tc.name, got)
			} else {
				require.Greater(t, got, tc.wantGT,
					"expected positive boundary > %d for %q, got %d", tc.wantGT, tc.name, got)
				// Boundary must not exceed the source length.
				require.LessOrEqual(t, got, len(tc.src),
					"boundary must not exceed source length for %q", tc.name)
			}
		})
	}
}
