package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApproxTokens proves the 4-chars-per-token heuristic produces sensible
// estimates: an empty string is zero, a rune count below four rounds up to one,
// and longer strings are divided by four without rounding, so every case gives
// the caller a number that is cheap to format into the status bar.
func TestApproxTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},                        // 1 rune → floor(1/4)=0 → clamped to 1
		{"abc", 1},                      // 3 runes → floor(3/4)=0 → clamped to 1
		{"abcd", 1},                     // exactly 4 runes
		{"abcdefgh", 2},                 // 8 runes → 2
		{strings.Repeat("a", 100), 25},  // 100 runes → 25
		{strings.Repeat("a", 400), 100}, // 400 runes → 100
	}
	for _, c := range cases {
		require.Equal(t, c.want, approxTokens(c.in), "approxTokens(%q)", c.in)
	}
}

// TestApproxTokens_MultiByte proves the estimate counts runes, not bytes, so a
// string of multi-byte Unicode characters is not over-counted — a 4-rune emoji
// sequence is one token, not more, because the heuristic divides by rune not byte.
func TestApproxTokens_MultiByte(t *testing.T) {
	t.Parallel()

	// Four CJK characters: each is 3 bytes, but 1 rune → 4 runes total → 1 token.
	s := "日本語字"
	require.Equal(t, 1, approxTokens(s), "4 multi-byte runes must produce 1 token, not more")
}

// TestRenderMain_InputTokensAbsentOnEmptyInput proves an empty prompt adds no
// token segment to the status bar, so the idle bar is unchanged.
func TestRenderMain_InputTokensAbsentOnEmptyInput(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, 0, m.input.Len(), "input must start empty")
	require.NotContains(t, stripANSI(m.renderMain()), "tok",
		"an empty input must not surface a token count in the status bar")
}

// TestRenderMain_InputTokensAppearsWhileTyping proves that once the user has
// typed enough text, the status bar shows the estimated token count as "~N tok"
// so they can gauge their prompt size before submitting.
func TestRenderMain_InputTokensAppearsWhileTyping(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// 40 ASCII characters → approxTokens = 40/4 = 10.
	m.input.WriteString(strings.Repeat("x", 40))
	rendered := stripANSI(m.renderMain())
	require.Contains(t, rendered, "~10 tok",
		"40-char input must show '~10 tok' in the status bar")
}

// TestRenderMain_InputTokensClearedAfterReset proves the segment disappears
// once the input is cleared, mirroring the lifecycle of TurnTokens and ContextPct
// which also vanish when their source data is gone.
func TestRenderMain_InputTokensClearedAfterReset(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.input.WriteString(strings.Repeat("x", 40))
	require.Contains(t, stripANSI(m.renderMain()), "~10 tok",
		"input must show the token count while text is present")

	m.input.Reset()
	require.NotContains(t, stripANSI(m.renderMain()), "tok",
		"token count must vanish once the input is cleared")
}
