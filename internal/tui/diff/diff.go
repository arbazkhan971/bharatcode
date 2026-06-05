// Package diff renders unified diffs for tool results and edit previews.
package diff

import (
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

// RenderUnified returns a width-clamped unified diff view.
func (v *Viewer) RenderUnified(patch string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	for i, line := range lines {
		if len([]rune(line)) > width {
			line = string([]rune(line)[:width])
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			lines[i] = v.theme.DiffHunk.Render(line)
		case isDiffHeader(line):
			// File-boundary metadata (---/+++ paths, diff --git, index) is
			// matched before the +/- content cases so a "+++" or "---" path
			// line is styled as a header rather than as an added/removed line.
			lines[i] = v.theme.DiffHeader.Render(line)
		case strings.HasPrefix(line, "+"):
			lines[i] = v.theme.DiffAdd.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = v.theme.DiffRemove.Render(line)
		default:
			lines[i] = line
		}
	}
	return strings.Join(lines, "\n")
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
