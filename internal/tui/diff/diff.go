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
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			lines[i] = v.theme.DiffAdd.Render(line)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			lines[i] = v.theme.DiffRemove.Render(line)
		default:
			lines[i] = line
		}
	}
	return strings.Join(lines, "\n")
}
