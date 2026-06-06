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

// TestSearchLines_SmartCase asserts the smart-case rule: an all-lower-case term
// matches regardless of case, while a term carrying an upper-case letter matches
// only the exact-case occurrences — so typing a capital narrows the search the
// way ripgrep and fzf do, without a separate toggle.
func TestSearchLines_SmartCase(t *testing.T) {
	t.Parallel()

	text := "go Error\nlowercase error\nERROR shout\nError exact"

	// All-lower-case: case-insensitive, so every variant matches.
	require.Equal(t, []int{0, 1, 2, 3}, SearchLines(text, "error"),
		"a lower-case term must match every casing")

	// Upper-case present: case-sensitive, so only the exact "Error" lines match.
	require.Equal(t, []int{0, 3}, SearchLines(text, "Error"),
		"a term with an upper-case letter must match only the exact casing")
}

// TestSearchFold asserts the smart-case decision the highlighter shares with the
// search: fold case only when the term carries no upper-case letter.
func TestSearchFold(t *testing.T) {
	t.Parallel()

	require.True(t, SearchFold("error"), "an all-lower-case term folds case")
	require.True(t, SearchFold("err_no.2"), "digits and punctuation do not force case")
	require.False(t, SearchFold("Error"), "an upper-case letter makes the term case-sensitive")
	require.False(t, SearchFold("rpcERR"), "a trailing upper-case run is still case-sensitive")
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
