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
	view := m.viewString()
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
