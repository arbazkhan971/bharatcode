package chat

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

// TestRender_NoFramedBlocks asserts the transcript flows like Codex's: an
// assistant turn renders as plain content with no surrounding left-bar frame, so
// none of the box-drawing border glyphs the old AssistantBlock/UserBlock drew
// appear in the output.
func TestRender_NoFramedBlocks(t *testing.T) {
	l := New()
	l.EnableMarkdown("dark")
	l.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "Hello! How can I help you today?"}},
	})
	out := l.Render(80)

	for _, glyph := range []string{"│", "╭", "╰", "▍"} {
		require.NotContains(t, out, glyph,
			"flowing transcript must not draw the %q block-frame glyph, got:\n%s", glyph, out)
	}
	require.Contains(t, out, "Hello!", "assistant prose must still render")
}

// TestRender_TurnsSeparatedByBlankLineNotRule asserts two turns are separated by
// a single blank line and NOT by the old dotted "·" rule, so the conversation
// reads as one continuous stream.
func TestRender_TurnsSeparatedByBlankLineNotRule(t *testing.T) {
	l := New()
	l.Append(message.Message{ID: "u1", Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}})
	l.Append(message.Message{ID: "a1", Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: "there"}}})
	out := l.Render(80)

	require.NotContains(t, out, "·",
		"turns must be separated by a blank line, not the dotted rule, got:\n%s", out)
	require.Contains(t, out, "\n\n", "a blank line should separate the two turns")
}

// TestRender_AssistantProseFullContrast asserts finished assistant prose is
// rendered in the primary body color (full contrast) rather than a recessive
// muted/faint tone, so the answer reads as plain text the way Codex shows it. It
// checks the plain-text (markdown-disabled) path, which the renderer styles with
// styles.Primary.
func TestRender_AssistantProseFullContrast(t *testing.T) {
	primary := styles.Primary.Render("probe")
	require.Contains(t, primary, "\x1b[", "primary style must emit an ANSI color for the assertion to be meaningful")

	l := New() // markdown disabled → plain-text path
	l.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "probe"}},
	})
	out := l.Render(80)
	require.Contains(t, out, primary,
		"assistant prose should render in the primary body color, got:\n%s", out)
}

// TestRender_StreamThenFinishNoColumnShift asserts the assistant body occupies
// the same left column while streaming and after it finishes — both flush, no
// frame indent — so finishing a stream does not visibly shift the text. The body
// text "answer" should appear at the line start (after the header newline) in
// both states.
func TestRender_StreamThenFinishNoColumnShift(t *testing.T) {
	l := New()
	l.EnableMarkdown("dark")

	l.Stream("s1", "answer")
	streaming := bodyIndentOf(t, l.Render(80), "answer")

	l.FinishStream("s1")
	finished := bodyIndentOf(t, l.Render(80), "answer")

	require.Equal(t, streaming, finished,
		"assistant body must not shift columns when streaming finishes")
}

// TestRender_AssistantMarkdownHasNoTrailingPadding asserts the rendered
// assistant markdown does not drag glamour's full-width right-padding: no visible
// line ends in a run of spaces. This is the fix for prose that looked broken
// because every line carried a long tail of blank cells out to the wrap width.
func TestRender_AssistantMarkdownHasNoTrailingPadding(t *testing.T) {
	l := New()
	l.EnableMarkdown("dark")
	l.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "Hello! How can I help you today?"}},
	})
	out := l.Render(80)

	for _, line := range strings.Split(out, "\n") {
		plain := stripANSI(line)
		require.Equal(t, strings.TrimRight(plain, " "), plain,
			"no rendered line may end in trailing space padding, got line %q", plain)
	}
}

// TestTrimLineTrailing_PreservesClosingReset asserts that stripping the padding
// keeps a line's closing reset so its color does not bleed onto the next line:
// the trimmed render of a colored word must still end in an SGR reset.
func TestTrimLineTrailing_PreservesClosingReset(t *testing.T) {
	// A styled word followed by glamour-style padded spaces.
	line := "\x1b[38;2;232;226;214mword\x1b[0m" +
		strings.Repeat("\x1b[38;2;232;226;214m \x1b[0m", 20)
	got := trimLineTrailing(line)
	require.True(t, strings.HasSuffix(got, "\x1b[0m"),
		"trimmed line must end in a reset so color does not bleed, got %q", got)
	require.Equal(t, "word", stripANSI(got), "visible content must be preserved exactly")
}

// bodyIndentOf returns the count of leading spaces on the first line that
// contains needle (with ANSI escapes stripped), so a test can compare where a
// turn's body sits across renders.
func bodyIndentOf(t *testing.T, rendered, needle string) int {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		plain := stripANSI(line)
		if strings.Contains(plain, needle) {
			return len(plain) - len(strings.TrimLeft(plain, " "))
		}
	}
	t.Fatalf("needle %q not found in:\n%s", needle, rendered)
	return -1
}
