package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearchLines_FindsCaseInsensitiveMatches asserts SearchLines returns the
// indices of every line containing the term, matched without regard to case,
// against the same "\n" split the caller scrolls.
func TestSearchLines_FindsCaseInsensitiveMatches(t *testing.T) {
	t.Parallel()

	text := "alpha needle\nbeta\ngamma NEEDLE here\ndelta\nNeEdLe again"
	got := SearchLines(text, "needle")
	require.Equal(t, []int{0, 2, 4}, got,
		"every line containing the term (any case) must be reported by line index")
}

// TestSearchLines_NoMatchAndEmptyTerm asserts the empty cases: an empty term and
// a term absent from the text both yield no matches.
func TestSearchLines_NoMatchAndEmptyTerm(t *testing.T) {
	t.Parallel()

	require.Nil(t, SearchLines("alpha\nbeta", ""), "an empty term matches nothing")
	require.Nil(t, SearchLines("alpha\nbeta", "zeta"), "an absent term matches nothing")
}

// TestSearchLines_AgainstTranscriptText asserts the helper composes with
// TranscriptText: searching the transcript of a seeded list finds the lines that
// carry the term, including role-prefix lines.
func TestSearchLines_AgainstTranscriptText(t *testing.T) {
	t.Parallel()

	list := New()
	list.Append(msg("u1", "user", "please fix the parser"))
	list.Append(msg("a1", "assistant", "the parser is fixed now"))

	matches := SearchLines(list.TranscriptText(), "parser")
	require.Len(t, matches, 2, "both turns mention the parser, so both lines match")
}
