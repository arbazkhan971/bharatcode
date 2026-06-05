package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
//
// allFiles is the full workspace listing; files is the currently visible subset
// after the quick-filter is applied, so the cursor, windowing, and selection all
// operate on the narrowed view without special-casing. filter holds the active
// filter token and filtering reports whether the panel is capturing keystrokes
// into it, the way a side-panel quick-filter works in Claude Code and opencode.
type filetree struct {
	visible   bool
	focused   bool
	root      string
	allFiles  []string
	files     []string
	cursor    int
	filter    string
	filtering bool
}

// toggle flips panel visibility. Showing the panel also focuses it, clears any
// stale quick-filter so it opens on the full listing, and refreshes it from the
// workspace root.
func (f *filetree) toggle(root string) {
	f.visible = !f.visible
	f.focused = f.visible
	if f.visible {
		f.root = root
		f.filter = ""
		f.filtering = false
		f.refresh()
	}
}

// refresh re-enumerates the workspace file listing from the panel root, then
// re-applies the active quick-filter so the visible subset stays in sync.
func (f *filetree) refresh() {
	f.allFiles = listWorkspaceFiles(f.root)
	f.applyFilter()
}

// applyFilter recomputes the visible listing from allFiles through the active
// filter token, clamping the cursor so it never points past the narrowed view.
func (f *filetree) applyFilter() {
	f.files = filterFiles(f.filter, f.allFiles)
	if f.cursor >= len(f.files) {
		f.cursor = 0
	}
}

// appendFilter extends the quick-filter with typed text, resets the cursor to the
// top of the new result set, and re-applies the filter.
func (f *filetree) appendFilter(s string) {
	f.filter += s
	f.cursor = 0
	f.applyFilter()
}

// backspaceFilter drops the last rune of the quick-filter (a no-op when empty),
// resets the cursor, and re-applies the filter.
func (f *filetree) backspaceFilter() {
	if f.filter == "" {
		return
	}
	r := []rune(f.filter)
	f.filter = string(r[:len(r)-1])
	f.cursor = 0
	f.applyFilter()
}

// clearFilter discards the quick-filter, leaves filtering mode, and restores the
// full listing.
func (f *filetree) clearFilter() {
	f.filter = ""
	f.filtering = false
	f.cursor = 0
	f.applyFilter()
}

// filterFiles ranks files against a quick-filter token, best-first, reusing the
// same scoring the @-file picker uses so the panel and the picker rank a query
// the same way. An empty token returns the listing unchanged; the input order
// (lexical) breaks ties through the stable sort.
func filterFiles(filter string, files []string) []string {
	if filter == "" {
		return files
	}
	lower := strings.ToLower(filter)
	type scored struct {
		path  string
		score int
	}
	var matched []scored
	for _, f := range files {
		if s, ok := mentionScore(lower, strings.ToLower(f)); ok {
			matched = append(matched, scored{f, s})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].score < matched[j].score })
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.path)
	}
	return out
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
//
// The listing is windowed to keep the cursor on screen no matter how long the
// workspace is: a long listing would otherwise be sliced off the top by the
// final height clamp, hiding the selected file. Files hidden above or below the
// window are summarised by "↑ N more" / "↓ N more" markers, and the diff
// section is clamped to whatever height remains so the listing is never lost.
func (m *model) renderFiletree(width, height int) string {
	f := m.filetree
	if height < 1 {
		height = 1
	}

	var b strings.Builder
	b.WriteString(m.theme.Accent.Render(filetreeTitle(f.cursor, len(f.files))))
	b.WriteByte('\n')

	// Surface the quick-filter on its own row while it is active (or applied) so
	// the user can see what is narrowing the listing; a trailing caret marks live
	// capture, the way an inline filter shows its cursor.
	if f.filtering || f.filter != "" {
		label := "/" + f.filter
		if f.filtering {
			label += "▌"
		}
		b.WriteString(m.theme.Accent.Render(clampLine(label, width)))
		b.WriteByte('\n')
	}

	if len(f.files) == 0 {
		empty := "(no files)"
		if f.filter != "" {
			empty = "(no matches)"
		}
		b.WriteString(m.theme.Muted.Render(empty))
	} else {
		rows := filetreeListingRows(height, len(f.files))
		start := filetreeScrollStart(f.cursor, len(f.files), rows)
		end := start + rows
		if end > len(f.files) {
			end = len(f.files)
		}
		if start > 0 {
			b.WriteString(m.theme.Muted.Render(clampLine(fmt.Sprintf("  ↑ %d more", start), width)))
			b.WriteByte('\n')
		}
		for i := start; i < end; i++ {
			name := f.files[i]
			// Clamp the raw text before styling so ANSI codes are not counted or
			// sliced through.
			clamped := clampLine("  "+name, width)
			row := clamped
			switch {
			case i == f.cursor:
				row = m.theme.Accent.Render(clampLine("> "+name, width))
			case f.filter != "":
				// Emphasize the runes the quick-filter matched, the way the @-file
				// picker does, so a narrowed listing shows why each entry survived.
				// The two-column indent is kept muted and the name is highlighted
				// against the active filter token; a too-narrow clamp that loses the
				// indent falls back to a plain muted row.
				runes := []rune(clamped)
				if len(runes) > 2 {
					row = m.theme.Muted.Render(string(runes[:2])) + m.highlightMatch(string(runes[2:]), f.filter)
				} else {
					row = m.theme.Muted.Render(clamped)
				}
			}
			b.WriteString(row)
			b.WriteByte('\n')
		}
		if end < len(f.files) {
			b.WriteString(m.theme.Muted.Render(clampLine(fmt.Sprintf("  ↓ %d more", len(f.files)-end), width)))
			b.WriteByte('\n')
		}
	}

	b.WriteByte('\n')
	sel := f.selected()
	if sel == "" {
		b.WriteString(m.theme.Muted.Render("No file selected"))
		return clampHeight(b.String(), height)
	}
	b.WriteString(m.theme.Accent.Render("Diff: " + sel))
	b.WriteByte('\n')

	// Reserve the lines already used by the listing and labels so the diff body
	// is clamped to the remaining height rather than the whole panel; this keeps
	// the windowed listing visible even when the diff is long.
	used := strings.Count(b.String(), "\n")
	diffHeight := height - used
	if diffHeight < 1 {
		diffHeight = 1
	}

	diffs := m.filetreeDiffs(sel)
	if len(diffs) == 0 {
		b.WriteString(m.theme.Muted.Render("No recorded edits for this file."))
		return clampHeight(b.String(), height)
	}
	patch := unifiedPatch(diffs)
	body := diff.New(m.theme).RenderUnifiedNumbered(patch, max(1, width))
	b.WriteString(clampHeight(body, diffHeight))
	return clampHeight(b.String(), height)
}

// filetreeTitle renders the panel heading, appending a "cursor/total" position
// indicator once the workspace has files so the user can tell where the cursor
// sits in a long listing.
func filetreeTitle(cursor, count int) string {
	if count == 0 {
		return "Files"
	}
	return "Files (" + strconv.Itoa(cursor+1) + "/" + strconv.Itoa(count) + ")"
}

// filetreeListingRows returns how many file rows the listing may occupy in a
// panel of the given height. It reserves the header and a roughly equal share
// for the diff section below, so neither half starves the other, and never
// returns more rows than there are files (so a short listing wastes no space).
func filetreeListingRows(height, count int) int {
	avail := height - 1 // header row
	if avail < 1 {
		avail = 1
	}
	rows := avail / 2
	if rows < 1 {
		rows = 1
	}
	if rows > count {
		rows = count
	}
	return rows
}

// filetreeScrollStart returns the first listing index to render so a window of
// rows entries keeps cursor visible and roughly centred, clamped so the window
// never runs past either end of the listing.
func filetreeScrollStart(cursor, count, rows int) int {
	if rows <= 0 || count <= rows {
		return 0
	}
	start := cursor - rows/2
	if start < 0 {
		start = 0
	}
	if start > count-rows {
		start = count - rows
	}
	return start
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
// (leaving the panel visible), "/" starts a quick-filter, and Esc clears an
// active filter before falling through to close the panel. Ctrl+F continues to
// toggle the panel.
func (m *model) handleFiletreeKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	if m.filetree.filtering {
		return m.handleFiletreeFilterKey(msg)
	}
	switch msg.String() {
	case "up":
		m.filetree.moveCursor(-1)
		return true, nil
	case "down":
		m.filetree.moveCursor(1)
		return true, nil
	case "/":
		// Enter quick-filter mode; subsequent keystrokes narrow the listing.
		m.filetree.filtering = true
		return true, nil
	case "esc":
		// A live filter is cleared first; only an unfiltered panel lets Esc fall
		// through to the panel-closing handler.
		if m.filetree.filter != "" {
			m.filetree.clearFilter()
			return true, nil
		}
		return false, nil
	case "tab":
		m.filetree.focused = false
		return true, nil
	default:
		return false, nil
	}
}

// handleFiletreeFilterKey processes a key while the quick-filter is capturing
// input. Printable text extends the filter, Backspace trims it, Up/Down still
// move the cursor through the narrowed listing, Enter/Tab confirm the filter and
// leave capture mode (keeping the results), and Esc clears the filter entirely.
// Every other key is consumed so a stray keystroke never leaks to the prompt
// while the panel owns the keyboard.
func (m *model) handleFiletreeFilterKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filetree.clearFilter()
		return true, nil
	case "enter", "tab":
		m.filetree.filtering = false
		return true, nil
	case "up":
		m.filetree.moveCursor(-1)
		return true, nil
	case "down":
		m.filetree.moveCursor(1)
		return true, nil
	case "backspace":
		m.filetree.backspaceFilter()
		return true, nil
	default:
		// Plain printable text extends the filter; modified or unprintable keys
		// (Ctrl+F to toggle the panel, PgUp to scroll, …) fall through so the
		// global shortcuts still reach their handlers while the filter is active.
		if k := msg.Key(); k.Text != "" && k.Mod == 0 {
			m.filetree.appendFilter(k.Text)
			return true, nil
		}
		return false, nil
	}
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
