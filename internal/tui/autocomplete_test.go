package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// candidateValues extracts the Value of each candidate for order-sensitive
// assertions.
func candidateValues(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Value
	}
	return out
}

// containsValue reports whether any candidate has the given Value.
func containsValue(cands []Candidate, value string) bool {
	for _, c := range cands {
		if c.Value == value {
			return true
		}
	}
	return false
}

// --- slash provider ---

// TestSlashProvider_Match proves the slash provider claims a bare slash word and
// declines a prose line where the command is already chosen.
func TestSlashProvider_Match(t *testing.T) {
	t.Parallel()
	p := newSlashProvider([]string{"/help", "/clear"}, nil)

	token, start, ok := p.Match("/he")
	require.True(t, ok)
	require.Equal(t, "he", token)
	require.Equal(t, 0, start)

	_, _, ok = p.Match("/help me")
	require.False(t, ok, "a slash command followed by prose is not re-completed")

	_, _, ok = p.Match("hello")
	require.False(t, ok)
}

// TestSlashProvider_FuzzyRanks proves the slash provider ranks commands by fuzzy
// score and returns whole "/name" values with the leading slash.
func TestSlashProvider_FuzzyRanks(t *testing.T) {
	t.Parallel()
	p := newSlashProvider([]string{"/help", "/clear", "/export"}, map[string]string{"/help": "list commands"})

	cands := p.Suggest("hl") // subsequence of "help"
	require.NotEmpty(t, cands)
	require.Equal(t, "/help", cands[0].Value)
	require.Equal(t, "list commands", cands[0].Detail)

	// Empty token lists everything in declared order.
	all := p.Suggest("")
	require.Equal(t, []string{"/help", "/clear", "/export"}, candidateValues(all))
}

// TestSlashProvider_PositionsAccountForSlash proves the highlight positions are
// re-based onto the "/name" display, so they index past the leading slash.
func TestSlashProvider_PositionsAccountForSlash(t *testing.T) {
	t.Parallel()
	p := newSlashProvider([]string{"/help"}, nil)
	cands := p.Suggest("h")
	require.Len(t, cands, 1)
	require.Equal(t, []int{1}, cands[0].Positions, "the 'h' of /help is at display index 1")
}

// --- file provider ---

// TestFileProvider_MatchAndSuggest proves the @-file provider claims an
// in-progress @-token, points the splice offset at the "@", and ranks workspace
// files honoring .gitignore.
func TestFileProvider_MatchAndSuggest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "*.log\n")
	writeFile(t, root, "main.go", "package main")
	writeFile(t, root, "internal/parser.go", "package internal")
	writeFile(t, root, "debug.log", "noise")

	p := newFileProvider(root)

	token, start, ok := p.Match("see @par")
	require.True(t, ok)
	require.Equal(t, "par", token)
	require.Equal(t, 4, start, "splice begins at the '@'")

	cands := p.Suggest("par")
	require.NotEmpty(t, cands)
	require.Equal(t, "@internal/parser.go", cands[0].Value)
	require.False(t, containsValue(cands, "@debug.log"), "gitignored files are excluded")

	// An ignored file never appears even when its name is typed.
	require.Empty(t, p.Suggest("debug"))
}

// TestFileProvider_BareAtListsFiles proves a bare "@" offers the workspace head.
func TestFileProvider_BareAtListsFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "a.go", "x")
	writeFile(t, root, "b.go", "y")

	p := newFileProvider(root)
	token, _, ok := p.Match("@")
	require.True(t, ok)
	require.Equal(t, "", token)
	require.NotEmpty(t, p.Suggest(token))
}

// --- mention provider ---

// TestMentionProvider_RanksRoster proves the mention provider fuzzy-ranks its
// name roster and returns "@name" values.
func TestMentionProvider_RanksRoster(t *testing.T) {
	t.Parallel()
	p := newMentionProvider([]string{"coder", "reviewer", "planner"})

	cands := p.Suggest("rev")
	require.NotEmpty(t, cands)
	require.Equal(t, "@reviewer", cands[0].Value)

	all := p.Suggest("")
	require.Equal(t, []string{"@coder", "@reviewer", "@planner"}, candidateValues(all))
}

// --- composite ---

// TestAutocomplete_RoutesSlash proves the composer routes a slash buffer to the
// slash provider only.
func TestAutocomplete_RoutesSlash(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "help.go", "x")
	ac := newAutocomplete(
		newSlashProvider([]string{"/help", "/clear"}, nil),
		newFileProvider(root),
		newMentionProvider([]string{"helper"}),
	)

	cands, start := ac.suggest("/hel")
	require.Equal(t, 0, start)
	require.NotEmpty(t, cands)
	for _, c := range cands {
		require.Equal(t, "/", string(c.Value[0]), "only slash commands for a slash buffer")
	}
}

// TestAutocomplete_MergesFileAndMention proves an "@" buffer merges the file and
// mention providers (which share the @-token) into one ranked list spliced at the
// "@".
func TestAutocomplete_MergesFileAndMention(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "review.go", "x")
	ac := newAutocomplete(
		newSlashProvider([]string{"/help"}, nil),
		newFileProvider(root),
		newMentionProvider([]string{"reviewer"}),
	)

	cands, start := ac.suggest("ping @rev")
	require.Equal(t, 5, start, "splice begins at the '@'")
	require.True(t, containsValue(cands, "@review.go"), "file candidate present")
	require.True(t, containsValue(cands, "@reviewer"), "mention candidate present")
}

// TestAutocomplete_NoMatch proves a plain prose buffer yields no completions.
func TestAutocomplete_NoMatch(t *testing.T) {
	t.Parallel()
	ac := newAutocomplete(
		newSlashProvider([]string{"/help"}, nil),
		newMentionProvider([]string{"coder"}),
	)
	cands, start := ac.suggest("just some prose")
	require.Nil(t, cands)
	require.Equal(t, -1, start)
}

// TestAutocomplete_ModelWiring proves the model builds a working composer from
// its command set and workspace, so suggestCompletions answers for the live
// prompt buffer.
func TestAutocomplete_ModelWiring(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.input.setText("/he")
	cands, start := m.suggestCompletions()
	require.Equal(t, 0, start)
	require.True(t, containsValue(cands, "/help"))
}
