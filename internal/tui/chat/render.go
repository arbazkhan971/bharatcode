package chat

import (
	"sync"

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
	tr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(m.style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	m.byW[width] = tr
	return tr
}
