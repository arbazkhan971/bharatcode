package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/diff"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
)

// filetreeWidth is the column width reserved for the file-tree side panel when
// it is visible. The chat body shrinks to make room; the panel is hidden by
// default so the default render is unchanged.
const filetreeWidth = 32

// filetreeIgnored is the basic, gitignore-ish skip set applied while walking the
// workspace. It intentionally avoids parsing a real .gitignore to stay minimal:
// it drops version-control metadata, dependency vendor trees, and noise files.
var filetreeIgnored = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	".DS_Store":    {},
}

// filetree holds the side-panel state: whether it is visible, whether it has
// keyboard focus, the enumerated workspace-relative file paths, and the cursor.
type filetree struct {
	visible bool
	focused bool
	root    string
	files   []string
	cursor  int
}

// toggle flips panel visibility. Showing the panel also focuses it and, when the
// listing is empty, refreshes it from the workspace root.
func (f *filetree) toggle(root string) {
	f.visible = !f.visible
	f.focused = f.visible
	if f.visible {
		f.root = root
		f.refresh()
	}
}

// refresh re-enumerates the workspace file listing from the panel root.
func (f *filetree) refresh() {
	f.files = listWorkspaceFiles(f.root)
	if f.cursor >= len(f.files) {
		f.cursor = 0
	}
}

// moveCursor advances the selection by delta, clamped to the listing bounds.
func (f *filetree) moveCursor(delta int) {
	if len(f.files) == 0 {
		f.cursor = 0
		return
	}
	f.cursor += delta
	if f.cursor < 0 {
		f.cursor = 0
	}
	if f.cursor >= len(f.files) {
		f.cursor = len(f.files) - 1
	}
}

// selected returns the workspace-relative path under the cursor, or "" when the
// listing is empty.
func (f *filetree) selected() string {
	if f.cursor < 0 || f.cursor >= len(f.files) {
		return ""
	}
	return f.files[f.cursor]
}

// listWorkspaceFiles walks root and returns workspace-relative file paths sorted
// lexically, honoring the basic ignore set. Directories in filetreeIgnored and
// hidden dot-directories are skipped wholesale; unreadable roots yield nil.
func listWorkspaceFiles(root string) []string {
	if root == "" {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			if _, skip := filetreeIgnored[name]; skip {
				return filepath.SkipDir
			}
			// Skip hidden dot-directories (e.g. .git, .idea) without parsing a
			// real ignore file.
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if _, skip := filetreeIgnored[name]; skip {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files
}

// diffsForPath collects every recorded edit, multiedit, or write diff whose
// touched file matches rel (a workspace-relative path) across all messages,
// oldest-first. A tool path is matched after normalizing it relative to root so
// absolute tool paths and relative listing entries line up.
func diffsForPath(msgs []message.Message, root, rel string) []editDiff {
	var out []editDiff
	for _, msg := range msgs {
		if msg.Role != message.RoleAssistant {
			continue
		}
		for _, block := range msg.Content {
			use, ok := block.(message.ToolUseBlock)
			if !ok {
				continue
			}
			for _, d := range editDiffsFromToolUse(use) {
				if normalizeToolPath(root, d.Path) == rel {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// normalizeToolPath maps a tool-reported path to a workspace-relative, slash-
// separated form so it can be compared against the file listing. Absolute paths
// under root are made relative; paths outside root or relative paths are
// returned cleaned.
func normalizeToolPath(root, path string) string {
	if path == "" {
		return ""
	}
	if root != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

// renderFiletree composes the file-tree panel body: a header, the file listing
// with the cursor marked, and the selected file's before/after diff (or a
// placeholder when the selection has no recorded edits). The diff is rendered
// with the shared diff.Viewer so the panel reuses the same machinery as /diff.
func (m *model) renderFiletree(width, height int) string {
	f := m.filetree
	var b strings.Builder
	b.WriteString(m.theme.Accent.Render("Files"))
	b.WriteByte('\n')

	if len(f.files) == 0 {
		b.WriteString(m.theme.Muted.Render("(no files)"))
	}
	for i, name := range f.files {
		// Clamp the raw text before styling so ANSI codes are not counted or
		// sliced through.
		row := clampLine("  "+name, width)
		if i == f.cursor {
			row = clampLine("> "+name, width)
			row = m.theme.Accent.Render(row)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	sel := f.selected()
	if sel == "" {
		b.WriteString(m.theme.Muted.Render("No file selected"))
		return clampHeight(b.String(), height)
	}
	b.WriteString(m.theme.Accent.Render("Diff: " + sel))
	b.WriteByte('\n')

	diffs := m.filetreeDiffs(sel)
	if len(diffs) == 0 {
		b.WriteString(m.theme.Muted.Render("No recorded edits for this file."))
		return clampHeight(b.String(), height)
	}
	patch := unifiedPatch(diffs)
	b.WriteString(diff.New(m.theme).RenderUnified(patch, max(1, width)))
	return clampHeight(b.String(), height)
}

// filetreeDiffs returns the recorded edit diffs for the selected workspace-
// relative path. It uses the injected editDiffSource when set (tests wire a
// fixed message slice) and otherwise loads the persisted session messages,
// matching the data path used by /diff.
func (m *model) filetreeDiffs(rel string) []editDiff {
	msgs := m.editDiffMessages()
	if len(msgs) == 0 {
		return nil
	}
	return diffsForPath(msgs, m.filetree.root, rel)
}

// editDiffMessages returns the messages scanned for edit diffs. When the
// injectable editDiffSource is set it is used directly (the exportDir-style test
// seam); otherwise the persisted session messages are loaded.
func (m *model) editDiffMessages() []message.Message {
	if m.editDiffSource != nil {
		return m.editDiffSource()
	}
	if !m.sessionPersisted || m.deps.Sessions == nil {
		return nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID)
	if err != nil {
		return nil
	}
	return msgs
}

// handleFiletreeKey processes a key while the panel has focus. It reports
// whether the key was consumed; unconsumed keys fall through to the normal
// input handling. Up/Down move the cursor, Tab returns focus to the input line
// (leaving the panel visible), and Ctrl+F continues to toggle the panel.
func (m *model) handleFiletreeKey(msg keyStringer) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up":
		m.filetree.moveCursor(-1)
		return true, nil
	case "down":
		m.filetree.moveCursor(1)
		return true, nil
	case "tab":
		m.filetree.focused = false
		return true, nil
	default:
		return false, nil
	}
}

// keyStringer is the minimal key interface handleFiletreeKey needs. It is
// satisfied by tea.KeyPressMsg and keeps the helper unit-testable.
type keyStringer interface {
	String() string
}

// joinPanels lays the panel column to the left of body, separated by a single
// space gutter, both clamped to height rows. The panel column is padded to
// panelW display columns per line so the body stays aligned even though body
// lines may carry ANSI styling. It returns the composed block.
func joinPanels(panel, body string, panelW, height int) string {
	if height < 1 {
		height = 1
	}
	panelLines := splitToHeight(panel, height)
	bodyLines := splitToHeight(body, height)
	out := make([]string, height)
	for i := 0; i < height; i++ {
		left := panelLines[i]
		pad := panelW - lipgloss.Width(left)
		if pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		out[i] = left + " " + bodyLines[i]
	}
	return strings.Join(out, "\n")
}

// splitToHeight splits s into exactly height lines, padding with empty strings
// and dropping any overflow from the top so the tail stays visible.
func splitToHeight(s string, height int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

// clampLine truncates s to at most width runes so a single listing entry never
// overflows the panel column.
func clampLine(s string, width int) string {
	if width < 1 {
		width = 1
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width])
}
