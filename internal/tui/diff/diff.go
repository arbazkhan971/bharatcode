// Package diff renders unified diffs for tool results and edit previews.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
)

// Viewer renders unified diff text.
type Viewer struct {
	theme styles.Theme
}

// New constructs a diff viewer.
func New(theme styles.Theme) *Viewer {
	return &Viewer{theme: theme}
}

// hunkHeaderPattern captures the starting line numbers of a unified-diff hunk
// header, "@@ -<old>[,<count>] +<new>[,<count>] @@". Only the two start numbers
// are needed to number the lines that follow.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// RenderUnified returns a width-clamped unified diff view.
func (v *Viewer) RenderUnified(patch string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	for i, line := range lines {
		lines[i] = v.styleLine(clampWidth(line, width))
	}
	return strings.Join(lines, "\n")
}

// RenderUnifiedNumbered renders a unified diff like RenderUnified but prefixes
// each line with a two-column gutter showing the old- and new-file line
// numbers, matching the diff-review display of Claude Code and opencode. Hunk
// and file-boundary headers get a blank gutter; added lines show only the new
// number, removed lines only the old number, and context lines show both. The
// gutter width is reserved from width so the total stays within it.
func (v *Viewer) RenderUnifiedNumbered(patch string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")

	// Size the gutter once up front from the widest line number any row will
	// show, so every column aligns regardless of where the hunks start.
	digits := len(strconv.Itoa(lineNumberCeiling(lines)))
	gutterWidth := digits*2 + 2
	blankGutter := strings.Repeat(" ", gutterWidth)
	contentWidth := width - gutterWidth
	if contentWidth < 1 {
		contentWidth = 1
	}

	var oldLn, newLn int
	inHunk := false
	out := make([]string, len(lines))
	for i, line := range lines {
		styled := v.styleLine(clampWidth(line, contentWidth))

		if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			inHunk = true
			out[i] = blankGutter + styled
			continue
		}

		switch {
		case isDiffHeader(line) || strings.HasPrefix(line, "@@") || !inHunk:
			out[i] = blankGutter + styled
		case strings.HasPrefix(line, "+"):
			out[i] = v.gutter(oldLn, newLn, digits, false, true) + styled
			newLn++
		case strings.HasPrefix(line, "-"):
			out[i] = v.gutter(oldLn, newLn, digits, true, false) + styled
			oldLn++
		default:
			out[i] = v.gutter(oldLn, newLn, digits, true, true) + styled
			oldLn++
			newLn++
		}
	}
	return strings.Join(out, "\n")
}

// Stat summarizes a unified diff as a one-line "N file(s) changed, +A -R"
// header, matching the diffstat line Claude Code and opencode show above a
// reviewed diff. Files are counted from "+++" path headers; a bare single-file
// hunk with no headers still counts as one changed file once it has content.
// Added and removed counts are the "+"/"-" content lines, excluding the
// "+++"/"---" file-boundary metadata. The "+A" segment is rendered with the
// add style and "-R" with the remove style; the rest is muted. An empty or
// content-free patch returns "".
func (v *Viewer) Stat(patch string) string {
	files, added, removed := diffStat(patch)
	if files == 0 {
		return ""
	}

	noun := "files"
	if files == 1 {
		noun = "file"
	}
	out := v.theme.Muted.Render(fmt.Sprintf("%d %s changed, ", files, noun))
	out += v.theme.DiffAdd.Render(fmt.Sprintf("+%d", added))
	out += v.theme.Muted.Render(" ")
	out += v.theme.DiffRemove.Render(fmt.Sprintf("-%d", removed))
	return out
}

// StatLines renders a multi-line diffstat: the aggregate Stat header followed,
// when more than one file changed, by one indented row per file showing its path
// and its own "+A -B" counts, matching the per-file breakdown Claude Code and
// opencode show above a multi-file review. File paths are left-aligned in a
// shared column so the counts line up. A single-file or content-free patch
// returns just the Stat header (or "" when empty), so the common case is
// unchanged. Unnamed bare hunks are labelled with a muted placeholder.
func (v *Viewer) StatLines(patch string) string {
	header := v.Stat(patch)
	if header == "" {
		return ""
	}
	files := fileStats(patch)
	if len(files) <= 1 {
		return header
	}

	width := 0
	for _, f := range files {
		if n := len([]rune(displayPath(f.Path))); n > width {
			width = n
		}
	}

	out := header
	for _, f := range files {
		name := displayPath(f.Path)
		pad := strings.Repeat(" ", width-len([]rune(name)))
		row := "  " + v.theme.Muted.Render(name+pad+"  ")
		row += v.theme.DiffAdd.Render(fmt.Sprintf("+%d", f.Added))
		row += v.theme.Muted.Render(" ")
		row += v.theme.DiffRemove.Render(fmt.Sprintf("-%d", f.Removed))
		out += "\n" + row
	}
	return out
}

// displayPath returns the file name to show in a per-file diffstat row, falling
// back to a placeholder for an unnamed bare hunk.
func displayPath(p string) string {
	if p == "" {
		return "(unnamed)"
	}
	return p
}

// FileStat is the per-file portion of a diffstat: the new-file path and the
// added/removed content-line counts attributed to it.
type FileStat struct {
	Path    string
	Added   int
	Removed int
}

// diffStat counts the changed files and the added/removed content lines in a
// unified diff. It mirrors the line classification used by the renderers so the
// summary always agrees with what is shown. A patch carrying added or removed
// content but no "+++" header (a bare hunk) is reported as one changed file.
func diffStat(patch string) (files, added, removed int) {
	for _, f := range fileStats(patch) {
		files++
		added += f.Added
		removed += f.Removed
	}
	return files, added, removed
}

// fileStats breaks a unified diff into one FileStat per changed file, attributing
// added/removed content lines to the file named by the most recent "+++" header.
// The "a/"/"b/" prefix and any trailing tab metadata on the path are stripped so
// the reported name matches the working-tree path. Content that appears before
// any "+++" header (a bare hunk) is attributed to a single unnamed file so it is
// still counted once, mirroring diffStat's bare-hunk handling.
func fileStats(patch string) []FileStat {
	var files []FileStat
	cur := -1
	ensureBare := func() {
		if cur < 0 {
			files = append(files, FileStat{})
			cur = 0
		}
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
			files = append(files, FileStat{Path: cleanDiffPath(line[len("+++"):])})
			cur = len(files) - 1
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "@@"):
			// File-boundary or hunk metadata: not counted as content.
		case strings.HasPrefix(line, "+"):
			ensureBare()
			files[cur].Added++
		case strings.HasPrefix(line, "-"):
			ensureBare()
			files[cur].Removed++
		}
	}
	return files
}

// cleanDiffPath normalizes the path captured from a "---"/"+++" header: it trims
// surrounding spaces, drops any trailing tab-delimited timestamp, and removes a
// leading "a/" or "b/" prefix so the name matches the working-tree path.
func cleanDiffPath(raw string) string {
	p := strings.TrimSpace(raw)
	if tab := strings.IndexByte(p, '\t'); tab >= 0 {
		p = strings.TrimSpace(p[:tab])
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	return p
}

// gutter renders the muted two-column "old new " line-number prefix. A column
// is left blank (spaces) when its side does not apply to the line, so an added
// line shows no old number and a removed line shows no new number.
func (v *Viewer) gutter(oldN, newN, digits int, showOld, showNew bool) string {
	return v.theme.Muted.Render(numberCell(oldN, digits, showOld) + " " + numberCell(newN, digits, showNew) + " ")
}

// numberCell right-aligns n in a digits-wide field, or returns that many spaces
// when the number does not apply to the line.
func numberCell(n, digits int, show bool) string {
	if !show {
		return strings.Repeat(" ", digits)
	}
	return fmt.Sprintf("%*d", digits, n)
}

// lineNumberCeiling returns the largest old- or new-file line number that
// numbering the given unified-diff lines will produce, so the gutter can be
// sized once before rendering. It mirrors the counting in RenderUnifiedNumbered
// and returns at least 1 so the gutter is never zero-width.
func lineNumberCeiling(lines []string) int {
	var oldLn, newLn, max int
	inHunk := false
	for _, line := range lines {
		if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			inHunk = true
			continue
		}
		if isDiffHeader(line) || strings.HasPrefix(line, "@@") || !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			max = maxInt(max, newLn)
			newLn++
		case strings.HasPrefix(line, "-"):
			max = maxInt(max, oldLn)
			oldLn++
		default:
			max = maxInt(max, oldLn)
			max = maxInt(max, newLn)
			oldLn++
			newLn++
		}
	}
	if max < 1 {
		return 1
	}
	return max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// styleLine applies the per-line diff styling shared by the plain and numbered
// renderers. File-boundary metadata is matched before the +/- content cases so
// a "+++"/"---" path line is styled as a header rather than as added/removed
// content.
func (v *Viewer) styleLine(line string) string {
	switch {
	case strings.HasPrefix(line, "@@"):
		return v.theme.DiffHunk.Render(line)
	case isDiffHeader(line):
		return v.theme.DiffHeader.Render(line)
	case strings.HasPrefix(line, "+"):
		return v.theme.DiffAdd.Render(line)
	case strings.HasPrefix(line, "-"):
		return v.theme.DiffRemove.Render(line)
	default:
		return line
	}
}

// clampWidth truncates line to at most width runes.
func clampWidth(line string, width int) string {
	if len([]rune(line)) > width {
		return string([]rune(line)[:width])
	}
	return line
}

// isDiffHeader reports whether line is unified-diff file-boundary metadata
// rather than content: the old/new path lines (---/+++), the git "diff --git"
// banner, or the "index" blob line. These delimit one file from the next in a
// multi-file patch and are styled distinctly from added/removed content.
func isDiffHeader(line string) bool {
	return strings.HasPrefix(line, "+++") ||
		strings.HasPrefix(line, "---") ||
		strings.HasPrefix(line, "diff --git") ||
		strings.HasPrefix(line, "index ")
}
