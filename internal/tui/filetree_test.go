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
