package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// writeFile creates path under root, making parent directories as needed.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl})
}

func TestFiletreePanel_TogglesAndListsFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "pkg/util.go", "package pkg\n")
	// An ignored entry that must not appear in the listing.
	writeFile(t, root, "node_modules/dep/index.js", "module.exports = {}\n")
	writeFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")

	m := newSizedModel(t)
	m.workspaceRoot = root

	// Hidden by default: the panel header is absent from the render.
	require.NotContains(t, m.renderMain(), "Files")

	// Ctrl+F toggles the panel on.
	_, _ = m.Update(ctrlKey('f'))
	require.True(t, m.filetree.visible)
	require.True(t, m.filetree.focused)

	out := m.renderMain()
	require.Contains(t, out, "Files")
	require.Contains(t, out, "main.go")
	require.Contains(t, out, "pkg/util.go")
	// Ignored trees are skipped.
	require.NotContains(t, out, "node_modules")
	require.NotContains(t, out, "HEAD")

	// Ctrl+F toggles the panel back off; the header disappears.
	_, _ = m.Update(ctrlKey('f'))
	require.False(t, m.filetree.visible)
	require.NotContains(t, m.renderMain(), "Files")
}

func TestFiletreePanel_SelectingEditedFile_RendersDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "package main\n")
	writeFile(t, root, "beta.go", "package main\n")

	m := newSizedModel(t)
	m.workspaceRoot = root

	// Wire a fixed edit-diff source (the exportDir-style test seam): an edit of
	// beta.go from "old line" to "new line".
	editInput, err := json.Marshal(map[string]string{
		"path":       filepath.Join(root, "beta.go"),
		"old_string": "old line",
		"new_string": "new line",
	})
	require.NoError(t, err)
	m.editDiffSource = func() []message.Message {
		return []message.Message{{
			Role: message.RoleAssistant,
			Content: []message.ContentBlock{
				message.ToolUseBlock{ID: "t1", Name: "edit", Input: editInput},
			},
		}}
	}

	// Open the panel.
	_, _ = m.Update(ctrlKey('f'))
	require.Equal(t, []string{"alpha.go", "beta.go"}, m.filetree.files)

	// Cursor starts on alpha.go, which has no recorded edits.
	require.Equal(t, "alpha.go", m.filetree.selected())
	require.Contains(t, m.renderMain(), "No recorded edits for this file.")

	// Move the cursor down to beta.go and assert its diff renders.
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, "beta.go", m.filetree.selected())

	// Strip ANSI before matching: the diff viewer highlights the changed run of a
	// modified line as a separate styled span, so the line's text is no longer one
	// contiguous escape-free substring in the raw render.
	out := plainText(m.renderMain())
	require.Contains(t, out, "Diff: beta.go")
	require.Contains(t, out, "old line")
	require.Contains(t, out, "new line")
}

func TestFiletreePanel_MarksEditedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "package main\n")
	writeFile(t, root, "beta.go", "package main\n")

	m := newSizedModel(t)
	m.workspaceRoot = root

	editInput, err := json.Marshal(map[string]string{
		"path":       filepath.Join(root, "beta.go"),
		"old_string": "old line",
		"new_string": "new line",
	})
	require.NoError(t, err)
	m.editDiffSource = func() []message.Message {
		return []message.Message{{
			Role: message.RoleAssistant,
			Content: []message.ContentBlock{
				message.ToolUseBlock{ID: "t1", Name: "edit", Input: editInput},
			},
		}}
	}

	// Open the panel; the cursor starts on the unedited alpha.go.
	_, _ = m.Update(ctrlKey('f'))
	require.Equal(t, "alpha.go", m.filetree.selected())

	out := plainText(m.renderMain())
	// The edited, non-cursor beta.go carries the "●" marker in place of its
	// indent; the unedited alpha.go (under the cursor) shows the "> " indicator
	// and no marker.
	require.Contains(t, out, "● beta.go")
	require.Contains(t, out, "> alpha.go")
	require.NotContains(t, out, "● alpha.go")

	// Move the cursor onto beta.go: it loses the marker to the cursor indicator,
	// and the now-unselected alpha.go still has no marker.
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, "beta.go", m.filetree.selected())
	out = plainText(m.renderMain())
	require.Contains(t, out, "> beta.go")
	require.NotContains(t, out, "● alpha.go")
}

// TestFiletreePanel_NoMarkerWithoutEdits proves an untouched workspace lists
// every file with its plain indent, so the marker only ever flags real edits.
func TestFiletreePanel_NoMarkerWithoutEdits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "package main\n")
	writeFile(t, root, "beta.go", "package main\n")

	m := newSizedModel(t)
	m.workspaceRoot = root
	m.editDiffSource = func() []message.Message { return nil }

	_, _ = m.Update(ctrlKey('f'))
	out := plainText(m.renderMain())
	require.Contains(t, out, "beta.go")
	require.NotContains(t, out, "●")
}

func TestFiletreeScrollStart_KeepsCursorVisible(t *testing.T) {
	t.Parallel()

	// Few files relative to the window: no scrolling, always start at the top.
	require.Equal(t, 0, filetreeScrollStart(0, 3, 10))
	require.Equal(t, 0, filetreeScrollStart(2, 3, 10))

	// A cursor near the top pins the window to the top.
	require.Equal(t, 0, filetreeScrollStart(1, 100, 10))

	// A mid-listing cursor centres the window.
	require.Equal(t, 45, filetreeScrollStart(50, 100, 10))

	// A cursor near the bottom clamps the window to the end (it never runs past
	// the final entry).
	require.Equal(t, 90, filetreeScrollStart(99, 100, 10))
	require.Equal(t, 90, filetreeScrollStart(95, 100, 10))
}

func TestFiletreeListingRows_ReservesRoomForDiff(t *testing.T) {
	t.Parallel()

	// The listing takes roughly half the panel, leaving the rest for the diff.
	require.Equal(t, 9, filetreeListingRows(20, 100))
	// A short listing never claims more rows than it has files.
	require.Equal(t, 3, filetreeListingRows(20, 3))
	// Tiny panels still yield at least one row.
	require.Equal(t, 1, filetreeListingRows(1, 100))
}

func TestFiletreeTitle_ShowsPosition(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Files", filetreeTitle(0, 0))
	require.Equal(t, "Files (1/5)", filetreeTitle(0, 5))
	require.Equal(t, "Files (5/5)", filetreeTitle(4, 5))
}

func TestFiletreePanel_LongListing_KeepsCursorOnScreen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Enough files that the listing must be windowed within the panel.
	for i := 0; i < 80; i++ {
		writeFile(t, root, fmt.Sprintf("file%02d.go", i), "package main\n")
	}

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))
	require.Len(t, m.filetree.files, 80)

	// Walk the cursor deep into the listing, past where an unwindowed render
	// would have pushed it off the top.
	for i := 0; i < 60; i++ {
		_, _ = m.Update(keySpecial("down", tea.KeyDown))
	}
	require.Equal(t, 60, m.filetree.cursor)

	out := m.renderMain()
	sel := m.filetree.selected()
	// The selected file (marked with "> ") is still rendered, and the position
	// indicator and a "more above" marker confirm the listing scrolled.
	require.Contains(t, out, "> "+sel)
	require.Contains(t, out, "Files (61/80)")
	require.Contains(t, out, "more")
}

func TestFiletreePanel_FocusedNavigation_DoesNotEditInput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "a.go", "")
	writeFile(t, root, "b.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))

	// While the panel is focused, Down moves the panel cursor rather than walking
	// input history, and the input buffer is untouched.
	require.Equal(t, 0, m.filetree.cursor)
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 1, m.filetree.cursor)
	require.Empty(t, m.input.String())

	// Tab returns focus to the input line without hiding the panel.
	_, _ = m.Update(keySpecial("tab", tea.KeyTab))
	require.True(t, m.filetree.visible)
	require.False(t, m.filetree.focused)
}

func TestFiletreeFilter_NarrowsListingAndConsumesKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "")
	writeFile(t, root, "beta.go", "")
	writeFile(t, root, "pkg/gamma.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))
	require.Equal(t, []string{"alpha.go", "beta.go", "pkg/gamma.go"}, m.filetree.files)

	// "/" enters quick-filter mode without editing the input buffer.
	_, _ = m.Update(keyText("/"))
	require.True(t, m.filetree.filtering)
	require.Empty(t, m.input.String())

	// Typed characters narrow the listing instead of reaching the prompt.
	_, _ = m.Update(keyText("a"))
	_, _ = m.Update(keyText("l"))
	require.Equal(t, "al", m.filetree.filter)
	require.Equal(t, []string{"alpha.go"}, m.filetree.files)
	require.Empty(t, m.input.String())

	// The active filter is shown in the rendered panel.
	require.Contains(t, m.renderMain(), "/al")

	// Backspace trims the filter and widens the result set again.
	_, _ = m.Update(keySpecial("backspace", tea.KeyBackspace))
	require.Equal(t, "a", m.filetree.filter)
	require.Equal(t, []string{"alpha.go", "beta.go", "pkg/gamma.go"}, m.filetree.files)
}

func TestFiletreeFilter_EnterConfirmsAndArrowsNavigate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "")
	writeFile(t, root, "album.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))

	_, _ = m.Update(keyText("/"))
	_, _ = m.Update(keyText("al"))
	require.Equal(t, []string{"album.go", "alpha.go"}, m.filetree.files)

	// Enter leaves capture mode but keeps the filter applied.
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.False(t, m.filetree.filtering)
	require.Equal(t, "al", m.filetree.filter)
	require.Equal(t, []string{"album.go", "alpha.go"}, m.filetree.files)

	// The panel still owns navigation: Down moves the cursor within the filtered
	// view rather than submitting or editing the prompt.
	require.Equal(t, 0, m.filetree.cursor)
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 1, m.filetree.cursor)
	require.Equal(t, "alpha.go", m.filetree.selected())
}

func TestFiletreeFilter_HighlightsMatchedRunes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "album.go", "")
	writeFile(t, root, "alpha.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))

	_, _ = m.Update(keyText("/"))
	_, _ = m.Update(keyText("al"))
	require.Equal(t, []string{"album.go", "alpha.go"}, m.filetree.files)

	render := m.renderMain()

	// The visible text of each surviving entry round-trips intact: highlighting
	// changes only styling, never the characters shown.
	stripped := stripANSI(render)
	require.Contains(t, stripped, "album.go")
	require.Contains(t, stripped, "alpha.go")

	// A non-cursor match (alpha.go sits below the cursor) accents exactly the
	// runes the filter matched, reusing the @-file picker's highlighter, so the
	// "al" prefix is rendered as its own accent span.
	require.Equal(t, 0, m.filetree.cursor)
	require.Contains(t, render, m.theme.Accent.Render("al"),
		"matched filter runes are highlighted in the listing")
}

func TestFiletreeFilter_EscClearsBeforeClosingPanel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "")
	writeFile(t, root, "beta.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))

	_, _ = m.Update(keyText("/"))
	_, _ = m.Update(keyText("alpha"))
	require.Equal(t, []string{"alpha.go"}, m.filetree.files)

	// First Esc leaves capture mode and clears the filter, restoring the full
	// listing while keeping the panel open.
	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.filetree.filtering)
	require.Empty(t, m.filetree.filter)
	require.Equal(t, []string{"alpha.go", "beta.go"}, m.filetree.files)
	require.True(t, m.filetree.visible)

	// A second Esc, with no filter to clear, falls through to close the panel.
	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.filetree.visible)
}

func TestFiletreeFilter_NoMatchShowsNote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))

	_, _ = m.Update(keyText("/"))
	_, _ = m.Update(keyText("zzz"))
	require.Empty(t, m.filetree.files)
	require.Contains(t, m.renderMain(), "(no matches)")
}

func TestFiletreeFilter_ReopeningClearsStaleFilter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "alpha.go", "")
	writeFile(t, root, "beta.go", "")

	m := newSizedModel(t)
	m.workspaceRoot = root
	_, _ = m.Update(ctrlKey('f'))
	_, _ = m.Update(keyText("/"))
	_, _ = m.Update(keyText("alpha"))
	require.Equal(t, []string{"alpha.go"}, m.filetree.files)

	// Close and reopen: the panel comes back on the full, unfiltered listing.
	_, _ = m.Update(ctrlKey('f'))
	_, _ = m.Update(ctrlKey('f'))
	require.Empty(t, m.filetree.filter)
	require.False(t, m.filetree.filtering)
	require.Equal(t, []string{"alpha.go", "beta.go"}, m.filetree.files)
}

func TestFilterFiles_RanksAndPreservesOrder(t *testing.T) {
	t.Parallel()

	files := []string{"alpha.go", "beta.go", "pkg/alarm.go"}

	// Empty filter returns the listing unchanged.
	require.Equal(t, files, filterFiles("", files))

	// A base-name prefix ("al" → alpha.go, alarm.go) outranks a non-match (beta).
	got := filterFiles("al", files)
	require.Equal(t, []string{"alpha.go", "pkg/alarm.go"}, got)

	// No match yields an empty slice.
	require.Empty(t, filterFiles("zzz", files))
}

// TestFilterFiles_AppliesPickerTieBreaks proves the quick-filter shares the
// @-file picker's full ordering, not just its coarse score band: within one
// score band a tighter matched span and a shallower path win, so filterFiles and
// rankedMentions agree on a query rather than diverging on ties.
func TestFilterFiles_AppliesPickerTieBreaks(t *testing.T) {
	t.Parallel()

	// All three match "ae" only as a scattered base-name subsequence (score band
	// 5), so the coarse score alone cannot separate them — the old score-only sort
	// would keep them in input order. The shared ordering instead breaks the tie by
	// span tightness, then shallower path: "cake.go" and "abe.go" (span 3) come
	// before the looser "apple.go" (span 5), and within the span-3 pair the
	// top-level "cake.go" outranks the nested "dir/abe.go".
	files := []string{"apple.go", "dir/abe.go", "cake.go"}
	got := filterFiles("ae", files)
	require.Equal(t, []string{"cake.go", "dir/abe.go", "apple.go"}, got)

	// The shared helper means the quick-filter and the @-file picker rank the same
	// candidate set identically.
	require.Equal(t, rankFilesByToken("ae", files), got)
}
