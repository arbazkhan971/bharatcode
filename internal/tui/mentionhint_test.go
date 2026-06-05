package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// mentionWorkspace writes the given workspace-relative files (empty content) into
// a fresh temp dir and returns its root, for exercising the @-file picker.
func mentionWorkspace(t *testing.T, rels ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, nil, 0o644))
	}
	return root
}

// TestActiveMention_DetectsTrailingToken asserts a trailing "@token" at a mention
// boundary is detected, while a mid-token "@" (an email address) is not.
func TestActiveMention_DetectsTrailingToken(t *testing.T) {
	t.Parallel()

	tok, ok := activeMention("look at @main")
	require.True(t, ok)
	require.Equal(t, "main", tok)

	// A lone trailing "@" is an active mention with an empty token.
	tok, ok = activeMention("@")
	require.True(t, ok)
	require.Equal(t, "", tok)

	// An email address is a mid-token "@", never a mention.
	_, ok = activeMention("ping user@host.com")
	require.False(t, ok)

	// A completed mention followed by a space is no longer active.
	_, ok = activeMention("@main.go done")
	require.False(t, ok)

	// No "@" at all.
	_, ok = activeMention("plain prose")
	require.False(t, ok)
}

// TestMentionMatches_RanksBaseNamePrefixFirst asserts a base-name prefix outranks
// a deeper path containment, and that matching is case-insensitive.
func TestMentionMatches_RanksBaseNamePrefixFirst(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t,
		"main.go",
		"cmd/maintenance/run.go",
		"internal/mainframe.go",
	)
	got := mentionMatches("main", root)
	require.NotEmpty(t, got)
	// main.go and mainframe.go have base names starting with the token, so they
	// rank ahead of cmd/maintenance/run.go (path containment only).
	require.Equal(t, "main.go", got[0])
	require.Contains(t, got, "internal/mainframe.go")
	require.Equal(t, "cmd/maintenance/run.go", got[len(got)-1])
}

// TestMentionMatches_EmptyTokenListsWorkspace asserts a bare "@" offers the head
// of the listing, capped at maxMentionHints.
func TestMentionMatches_EmptyTokenListsWorkspace(t *testing.T) {
	t.Parallel()

	var rels []string
	for i := 0; i < maxMentionHints+5; i++ {
		rels = append(rels, "f"+string(rune('a'+i%26))+string(rune('0'+i))+".txt")
	}
	root := mentionWorkspace(t, rels...)
	got := mentionMatches("", root)
	require.Len(t, got, maxMentionHints, "the bare-@ listing is capped")
}

// TestMentionMatches_EmptyTokenPrefersShallowFiles asserts a bare "@" surfaces
// top-level files ahead of deeply-nested ones that merely sort earlier
// lexically, matching how Claude Code reveals the workspace for a bare "@".
func TestMentionMatches_EmptyTokenPrefersShallowFiles(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t,
		"aaa/bbb/deep.go",
		"README.md",
		"main.go",
	)
	got := mentionMatches("", root)
	require.NotEmpty(t, got)
	// The top-level files come first (depth 0, shorter before longer), with the
	// nested file last, even though "aaa/bbb/deep.go" would sort first lexically.
	require.Equal(t, []string{"main.go", "README.md", "aaa/bbb/deep.go"}, got)
}

// TestMentionMatches_BaseNameSubstringOutranksPathSubstring asserts that when a
// token appears contiguously in a file's base name it ranks ahead of a file
// where the same token only appears inside a directory segment, so the picker
// favours file-name relevance over path location even for substring matches.
func TestMentionMatches_BaseNameSubstringOutranksPathSubstring(t *testing.T) {
	t.Parallel()

	// "log" is a substring of the base name "logger.go", but for
	// "log/server.go" it only appears in the directory segment.
	root := mentionWorkspace(t,
		"log/server.go",
		"internal/logger.go",
	)
	got := mentionMatches("log", root)
	require.Equal(t, []string{"internal/logger.go", "log/server.go"}, got)
}

// TestMentionMatches_SubsequenceFallback asserts a non-contiguous subsequence
// still matches when no prefix or substring does.
func TestMentionMatches_SubsequenceFallback(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t, "internal/tui/mention.go")
	require.Contains(t, mentionMatches("tmg", root), "internal/tui/mention.go")
	require.Empty(t, mentionMatches("zzz", root))
}

// TestMentionMatches_BaseNameSubsequenceOutranksPathSubsequence asserts that a
// file whose base name contains the token as a subsequence ranks ahead of one
// where the subsequence only holds when directory segments are included, so the
// picker favours file-name relevance over path location.
func TestMentionMatches_BaseNameSubsequenceOutranksPathSubsequence(t *testing.T) {
	t.Parallel()

	// "hlr" is a subsequence of the base name "handler.go", but for
	// "http/logger.go" it only matches by borrowing from the directory.
	root := mentionWorkspace(t,
		"http/logger.go",
		"handler.go",
	)
	got := mentionMatches("hlr", root)
	require.Equal(t, []string{"handler.go", "http/logger.go"}, got)
}

// TestMentionMatches_AcronymOutranksScatteredSubsequence asserts that a token
// landing on word starts (an acronym of the path's segments) ranks ahead of one
// that only threads scattered letters, the way fzf and opencode reward
// word-start hits.
func TestMentionMatches_AcronymOutranksScatteredSubsequence(t *testing.T) {
	t.Parallel()

	// "sb" spells the initials of "status_bar.go" (s + b at word starts), but in
	// "subscribe.go" it only matches the leading "s...b" as a scattered run.
	root := mentionWorkspace(t,
		"subscribe.go",
		"status_bar.go",
	)
	got := mentionMatches("sb", root)
	require.Equal(t, []string{"status_bar.go", "subscribe.go"}, got)
}

// TestMentionMatches_AcronymAcrossPathSegments asserts an acronym can hop across
// directory segments, so "its" reaches "internal/tui/statusbar.go".
func TestMentionMatches_AcronymAcrossPathSegments(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t, "internal/tui/statusbar.go")
	require.Contains(t, mentionMatches("its", root), "internal/tui/statusbar.go")
}

// TestMentionMatches_TighterSpanRanksFirst asserts that within one score band the
// candidate whose matched runes sit closer together ranks ahead of a looser
// match, even when the looser one is shallower — the fzf-style contiguity
// preference. Both files match the token only as a base-name subsequence (same
// score band), so the span tie-break is what decides the order.
func TestMentionMatches_TighterSpanRanksFirst(t *testing.T) {
	t.Parallel()

	// "az" threads "a..z" loosely across the shallow "axxz.go" (span 4) but
	// tightly across the deeper "sub/dir/axz.go" (span 3). Shape alone would put
	// the shallow file first; the span tie-break promotes the tighter match.
	root := mentionWorkspace(t,
		"axxz.go",
		"sub/dir/axz.go",
	)
	got := mentionMatches("az", root)
	require.Equal(t, []string{"sub/dir/axz.go", "axxz.go"}, got)
}

// TestMatchSpan_MeasuresMatchedRunRange asserts matchSpan reports the inclusive
// rune distance from the first matched rune to the last: exactly the token
// length for a contiguous match, and wider for a scattered subsequence.
func TestMatchSpan_MeasuresMatchedRunRange(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, matchSpan("ax", "axz.go"))  // contiguous "ax" -> span equals token length
	require.Equal(t, 3, matchSpan("az", "axz.go"))  // a..z one gap apart
	require.Equal(t, 4, matchSpan("az", "axxz.go")) // a..z two gaps apart
	require.Equal(t, 0, matchSpan("qq", "axz.go"))  // no match
	require.Equal(t, 0, matchSpan("", "axz.go"))    // empty token
}

// TestInitialsPositions_LightsWordStarts asserts the acronym matcher reports the
// rune indices of the word-start anchors it jumped between, and rejects a token
// whose letters are not all word starts.
func TestInitialsPositions_LightsWordStarts(t *testing.T) {
	t.Parallel()

	// "sb" anchors on the s of "status" (index 0) and the b of "bar" (index 7).
	require.Equal(t, []int{0, 7}, initialsPositions("sb", "status_bar.go"))
	// Case-insensitive against the path's runes.
	require.Equal(t, []int{0, 7}, initialsPositions("SB", "Status_Bar.go"))
	// "sa" is a scattered subsequence of "status_bar.go" but "a" is not a word
	// start, so the acronym matcher declines it.
	require.Nil(t, initialsPositions("sa", "status_bar.go"))
	require.Nil(t, initialsPositions("", "status_bar.go"))
}

// TestMatchPositions_AcronymHighlightsAnchors asserts highlighting lands on the
// word-start anchors for an acronym match rather than the first scattered run.
func TestMatchPositions_AcronymHighlightsAnchors(t *testing.T) {
	t.Parallel()

	// "sb" is not a contiguous substring of "status_bar.go", so the acronym band
	// lights the s (0) and the b (7) at the word starts.
	require.Equal(t, []int{0, 7}, matchPositions("sb", "status_bar.go"))
}

// TestCompleteMention_CyclesMatches asserts the first Tab replaces the token with
// the best match and subsequent Tabs cycle, mirroring slash completion.
func TestCompleteMention_CyclesMatches(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t, "main.go", "internal/mainframe.go")
	var st inputState

	out, ok := st.completeMention("see @main", root)
	require.True(t, ok)
	require.Equal(t, "see @main.go", out, "the token is replaced with the best match")

	out2, ok := st.completeMention(out, root)
	require.True(t, ok)
	require.Equal(t, "see @internal/mainframe.go", out2, "a second Tab cycles forward")

	// Cycling past the end wraps back to the first match.
	out3, ok := st.completeMention(out2, root)
	require.True(t, ok)
	require.Equal(t, "see @main.go", out3)
}

// TestCompleteMention_NoMatchLeavesBuffer asserts an unmatched token applies no
// completion and leaves the buffer unchanged.
func TestCompleteMention_NoMatchLeavesBuffer(t *testing.T) {
	t.Parallel()

	root := mentionWorkspace(t, "main.go")
	var st inputState
	out, ok := st.completeMention("@zzz", root)
	require.False(t, ok)
	require.Equal(t, "@zzz", out)
}

// TestMentionHintFiles_ActiveCycleMarksSelection asserts that during a Tab cycle
// the menu returns bare paths with the active index marked.
func TestMentionHintFiles_ActiveCycleMarksSelection(t *testing.T) {
	t.Parallel()

	st := inputState{
		completionMatches: []string{"see @main.go", "see @internal/mainframe.go"},
		completionIndex:   1,
	}
	files, active := mentionHintFiles("see @internal/mainframe.go", "", &st)
	require.Equal(t, []string{"main.go", "internal/mainframe.go"}, files)
	require.Equal(t, 1, active, "the buffer equals the cycle's second match")
}

// TestRenderMentionHint_VisibleAfterTyping is the end-to-end contract: typing an
// @-file prefix surfaces matching paths in the rendered view, and Tab completes
// the buffer to the first match.
func TestRenderMentionHint_VisibleAfterTyping(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t, "main.go", "internal/mainframe.go")

	typeString(t, m, "@main")
	// Strip ANSI before matching: the picker accents the matched runes, so the
	// styled "main.go" is split across spans in the raw output.
	view := stripANSI(m.viewString())
	require.Contains(t, view, "main.go")

	// Tab completes the in-progress mention to the best match.
	_, _ = m.Update(keyTab())
	require.Equal(t, "@main.go", m.input.String())
}

// TestRenderMentionHint_FitsOneRow asserts the menu never spills past a single
// row, truncating with an ellipsis when matches overflow the width.
func TestRenderMentionHint_FitsOneRow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.width = 20
	m.workspaceRoot = mentionWorkspace(t,
		"alpha.go", "alphabet.go", "alphanumeric.go", "alphasort.go",
	)
	typeString(t, m, "@alpha")
	hint := m.renderMentionHint(m.width)
	require.NotEmpty(t, hint)
	require.NotContains(t, hint, "\n", "the menu must stay on one row")
	require.Contains(t, hint, "…", "an over-long match set is truncated")
	require.Regexp(t, `\+\d+`, hint, "truncation reports how many matches are hidden")
}

// TestRenderMentionHint_OverflowReportsTrueTotal asserts the overflow count
// reflects every match, not just the displayed cap: when more than
// maxMentionHints files match a token but the displayed subset fits the width,
// the menu still appends "+N" for the matches beyond the cap, so a broad token in
// a large workspace does not silently imply only maxMentionHints files qualify.
func TestRenderMentionHint_OverflowReportsTrueTotal(t *testing.T) {
	t.Parallel()

	// Build more matches than the cap, with short names so the capped display
	// still fits comfortably on one wide row (no width truncation in play).
	const extra = 5
	var rels []string
	for i := 0; i < maxMentionHints+extra; i++ {
		rels = append(rels, "ma"+string(rune('a'+i/10))+string(rune('0'+i%10))+".go")
	}
	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t, rels...)
	typeString(t, m, "@ma")

	hint := stripANSI(m.renderMentionHint(400))
	require.NotEmpty(t, hint)
	require.NotContains(t, hint, "\n", "the menu must stay on one row")
	// maxMentionHints are shown; the remaining `extra` are reported as hidden.
	require.Contains(t, hint, "+5", "overflow must count matches beyond the displayed cap")
}

// TestRenderMentionHint_ShowsCyclePosition asserts that while the user Tabs
// through the @-file picker the rendered hint reports the selected position
// within the cycle, mirroring the slash-command menu so both pickers expose how
// far a Tab cycle has walked.
func TestRenderMentionHint_ShowsCyclePosition(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t, "alpha.go", "alphabet.go", "alphasort.go")

	// Seed the Tab cycle on "@alpha", then advance to the second match.
	m.setInput("@alpha")
	c1, ok := m.inputHistory.completeMention(m.input.String(), m.workspaceRoot)
	require.True(t, ok, "the first Tab must seed the cycle")
	m.setInput(c1)
	c2, ok := m.inputHistory.completeMention(m.input.String(), m.workspaceRoot)
	require.True(t, ok, "the second Tab must advance the cycle")
	m.setInput(c2)

	hint := stripANSI(m.renderMentionHint(400))
	require.NotEmpty(t, hint)
	require.Contains(t, hint, "(2/3)", "the picker reports the selected position within the cycle")
}

// TestRenderMentionHint_NoMatchingFiles asserts that an in-progress @-token with
// no matching file surfaces a "no matching files" note, so the picker reports an
// empty search instead of silently rendering nothing.
func TestRenderMentionHint_NoMatchingFiles(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t, "main.go", "internal/mainframe.go")

	typeString(t, m, "@zzzzz")
	hint := stripANSI(m.renderMentionHint(m.width))
	require.Contains(t, hint, "no matching files")
	require.NotContains(t, hint, "\n", "the note must stay on one row")
}

// TestRenderMentionHint_BareAtNoNote asserts a bare "@" (empty token) never shows
// the "no matching files" note: it lists the workspace, and an empty workspace is
// not a failed search. It also confirms a no-match note is dropped when it would
// not fit the available width.
func TestRenderMentionHint_BareAtNoNote(t *testing.T) {
	t.Parallel()

	// Empty workspace, bare "@": no listing, but no note either.
	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t)
	typeString(t, m, "@")
	require.Empty(t, m.renderMentionHint(m.width), "a bare @ stays silent")

	// A non-empty token with no match would show the note, but not when the
	// width cannot hold it.
	typeString(t, m, "zzzzz")
	require.Empty(t, m.renderMentionHint(5), "the note is dropped when it does not fit")
}

// TestMatchPositions_ContiguousAndSubsequence asserts the picker locates the
// runes a token matched: a case-insensitive contiguous run is preferred, and a
// scattered subsequence is reported rune-by-rune when no contiguous run exists.
func TestMatchPositions_ContiguousAndSubsequence(t *testing.T) {
	t.Parallel()

	// Contiguous, case-insensitive: "Main" matches "main" in "cmd/main.go".
	require.Equal(t, []int{4, 5, 6, 7}, matchPositions("Main", "cmd/main.go"))

	// The first contiguous run wins when several exist.
	require.Equal(t, []int{0, 1}, matchPositions("aa", "aaa.go"))

	// Subsequence fallback: "mng" is scattered through "mention.go" — m at 0,
	// the second n at 2, g at 8.
	require.Equal(t, []int{0, 2, 8}, matchPositions("mng", "mention.go"))

	// No match and an empty token both yield nil.
	require.Nil(t, matchPositions("zzz", "main.go"))
	require.Nil(t, matchPositions("", "main.go"))
}

// TestHighlightMention_PreservesVisibleText asserts that highlighting changes
// only styling, never the visible characters: the rune sequence of the rendered
// candidate is identical to the input path regardless of which runes matched.
func TestHighlightMention_PreservesVisibleText(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// A contiguous match, a subsequence match, and a no-match all round-trip the
	// path's visible text intact once the highlight styling is stripped.
	require.Equal(t, "internal/main.go", stripANSI(m.highlightMention("internal/main.go", "main")))
	require.Equal(t, "internal/main.go", stripANSI(m.highlightMention("internal/main.go", "itl")))
	require.Equal(t, "internal/main.go", stripANSI(m.highlightMention("internal/main.go", "zzz")))
}

// TestTab_MentionDoesNotToggleFocus asserts Tab on an active mention completes it
// rather than moving focus to the chat pane.
func TestTab_MentionDoesNotToggleFocus(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.workspaceRoot = mentionWorkspace(t, "main.go")
	typeString(t, m, "@mai")
	require.Equal(t, focusInput, m.focus)

	_, _ = m.Update(keyTab())
	require.Equal(t, focusInput, m.focus, "Tab completed the mention, focus stays on input")
	require.Equal(t, "@main.go", m.input.String())
}
