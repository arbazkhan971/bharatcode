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
