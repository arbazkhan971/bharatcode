package chat

import (
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/charmbracelet/glamour"
)

// markdownRenderer renders assistant markdown to ANSI for a given width. It
// caches one glamour TermRenderer per width because constructing a renderer is
// expensive and the chat width changes rarely (only on terminal resize).
type markdownRenderer struct {
	mu    sync.Mutex
	style string
	byW   map[int]*glamour.TermRenderer
}

// newMarkdownRenderer returns a renderer using the named glamour style (for
// example "dark" or "light").
func newMarkdownRenderer(style string) *markdownRenderer {
	if style == "" {
		style = "dark"
	}
	return &markdownRenderer{style: style, byW: make(map[int]*glamour.TermRenderer)}
}

// Render renders markdown to ANSI wrapped at width. On any renderer error it
// returns the input unchanged and ok=false so the caller can fall back to plain
// text rather than dropping content.
func (m *markdownRenderer) Render(markdown string, width int) (string, bool) {
	if m == nil || width < 1 {
		return markdown, false
	}
	tr := m.rendererFor(width)
	if tr == nil {
		return markdown, false
	}
	out, err := tr.Render(markdown)
	if err != nil {
		return markdown, false
	}
	return out, true
}

func (m *markdownRenderer) rendererFor(width int) *glamour.TermRenderer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tr, ok := m.byW[width]; ok {
		return tr
	}
	tr, err := newRenderer(m.style, width)
	if err != nil {
		return nil
	}
	m.byW[width] = tr
	return tr
}

// newRenderer builds the glamour renderer for a style at a width. The dark
// (default) style uses the restrained, custom activity-stream theme; other named
// styles (for example "light") fall back to glamour's matching stock style so
// the renderer still follows the active theme.
func newRenderer(style string, width int) (*glamour.TermRenderer, error) {
	if style == "" || style == "dark" {
		return styles.NewMarkdownRenderer(width)
	}
	return glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
	)
}
