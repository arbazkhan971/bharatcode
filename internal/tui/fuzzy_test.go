package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFuzzyMatch_EmptyQueryMatchesAll proves a blank query matches any target
// with a zero score and no positions, so a bare trigger lists everything.
func TestFuzzyMatch_EmptyQueryMatchesAll(t *testing.T) {
	t.Parallel()
	score, pos, ok := fuzzyMatch("", "anything")
	require.True(t, ok)
	require.Equal(t, 0, score)
	require.Nil(t, pos)
}

// TestFuzzyMatch_Subsequence proves a scattered subsequence matches and reports
// the matched rune positions in order.
func TestFuzzyMatch_Subsequence(t *testing.T) {
	t.Parallel()
	_, pos, ok := fuzzyMatch("abc", "axbxc")
	require.True(t, ok)
	require.Equal(t, []int{0, 2, 4}, pos)
}

// TestFuzzyMatch_NonMatch proves a query that is not a subsequence fails.
func TestFuzzyMatch_NonMatch(t *testing.T) {
	t.Parallel()
	_, _, ok := fuzzyMatch("xyz", "abc")
	require.False(t, ok)

	// Out-of-order: the runes exist but not in query order.
	_, _, ok = fuzzyMatch("cba", "abc")
	require.False(t, ok)
}

// TestFuzzyMatch_CaseInsensitive proves matching ignores case while the reported
// positions index the original target.
func TestFuzzyMatch_CaseInsensitive(t *testing.T) {
	t.Parallel()
	_, pos, ok := fuzzyMatch("HE", "help")
	require.True(t, ok)
	require.Equal(t, []int{0, 1}, pos)
}

// TestFuzzyMatch_PrefersWordBoundary proves an acronym lands on word-start runes
// rather than the earliest possible positions, so the highlight reads as segment
// initials.
func TestFuzzyMatch_PrefersWordBoundary(t *testing.T) {
	t.Parallel()
	_, pos, ok := fuzzyMatch("sb", "status_bar.go")
	require.True(t, ok)
	// s at index 0, b at index 7 (immediately after the '_').
	require.Equal(t, []int{0, 7}, pos)
}

// TestFuzzyMatch_BoundaryOutranksMidWord proves a boundary-aligned acronym
// outscores a near-contiguous mid-word hit of the same query, the fuzzy-finder
// preference that makes "sb" surface "status_bar.go" ahead of "submarine".
func TestFuzzyMatch_BoundaryOutranksMidWord(t *testing.T) {
	t.Parallel()
	boundary, _, ok1 := fuzzyMatch("sb", "status_bar.go")
	require.True(t, ok1)
	midWord, _, ok2 := fuzzyMatch("sb", "submarine")
	require.True(t, ok2)
	require.Greater(t, boundary, midWord)
}

// TestFuzzyMatch_PrefersContiguous proves a contiguous run outscores the same
// runes scattered across the target.
func TestFuzzyMatch_PrefersContiguous(t *testing.T) {
	t.Parallel()
	tight, _, ok1 := fuzzyMatch("abc", "abcde")
	require.True(t, ok1)
	loose, _, ok2 := fuzzyMatch("abc", "axbxcxe")
	require.True(t, ok2)
	require.Greater(t, tight, loose)
}

// TestFuzzyMatch_PrefersEarlier proves an earlier match outscores a later one of
// equal shape, since the leading gap is penalized.
func TestFuzzyMatch_PrefersEarlier(t *testing.T) {
	t.Parallel()
	early, _, _ := fuzzyMatch("go", "golang")
	late, _, _ := fuzzyMatch("go", "lingo_go")
	require.Greater(t, early, late)
}

// TestFuzzyMatch_Camel proves a camelCase hump counts as a word boundary, so an
// acronym finds the word starts in a camelCase identifier.
func TestFuzzyMatch_Camel(t *testing.T) {
	t.Parallel()
	_, pos, ok := fuzzyMatch("ps", "parseStatus")
	require.True(t, ok)
	require.Equal(t, []int{0, 5}, pos) // p at 0, S at 5
}

// TestFuzzyRank_OrdersByScore proves fuzzyRank drops non-matches and returns the
// rest best-first, mapping each result back to its source index.
func TestFuzzyRank_OrdersByScore(t *testing.T) {
	t.Parallel()
	cands := []string{"submarine", "status_bar.go", "tablet"}
	got := fuzzyRank("sb", cands)
	require.Len(t, got, 2, "tablet has no 'sb' subsequence")
	require.Equal(t, "status_bar.go", cands[got[0].Index], "boundary match ranks first")
	require.Equal(t, "submarine", cands[got[1].Index])
}

// TestFuzzyRank_EmptyQueryKeepsOrder proves a blank query returns every
// candidate in original order with a zero score.
func TestFuzzyRank_EmptyQueryKeepsOrder(t *testing.T) {
	t.Parallel()
	cands := []string{"one", "two", "three"}
	got := fuzzyRank("", cands)
	require.Len(t, got, 3)
	for i, r := range got {
		require.Equal(t, i, r.Index)
		require.Equal(t, 0, r.Score)
	}
}

// TestFuzzyRank_TieBreaksShorter proves equal-scoring candidates order the
// shorter one first, so a query that matches two names identically prefers the
// tighter name.
func TestFuzzyRank_TieBreaksShorter(t *testing.T) {
	t.Parallel()
	// Both begin with "ab" at the same boundary; the shorter should rank first.
	cands := []string{"abcdef", "abc"}
	got := fuzzyRank("ab", cands)
	require.Len(t, got, 2)
	require.Equal(t, "abc", cands[got[0].Index])
}
