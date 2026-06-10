package chat

import (
	"strings"
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

// stablePrefixBoundary returns the byte offset into src up to which the
// markdown structure is provably closed, making that prefix safe to render
// once and cache while the message continues to grow.
//
// The algorithm finds the last blank line ("\n\n") in src that does NOT fall
// inside an open fenced code block (``` or ~~~). Before that point every
// markdown construct is closed and the rendered output will not change when
// more text is appended, so the caller can render the prefix once, cache the
// ANSI, and only re-render the short tail that follows it.
//
// Returns 0 when no safe boundary exists — callers must fall back to a full
// render rather than an empty render. This covers: a message with no blank
// lines, a message whose every blank line sits inside a code fence, and any
// other case where confidence cannot be established. Correctness is always
// preferred over performance.
func stablePrefixBoundary(src string) int {
	if len(src) < 2 {
		return 0
	}

	// Collect the byte offset just after every "\n\n" in the text. Each is a
	// candidate stable boundary if it is not inside an open fence.
	type candidate struct{ end int }
	var candidates []candidate
	for i := 1; i < len(src); i++ {
		if src[i] == '\n' && src[i-1] == '\n' {
			candidates = append(candidates, candidate{end: i + 1})
		}
	}
	if len(candidates) == 0 {
		return 0
	}

	// Scan the text once to record each fence-toggle event: the byte offset
	// just after the fence line's trailing newline, and whether we are now
	// INSIDE a fence after that line. A fence line is one whose trimmed
	// content begins with ``` or ~~~.
	type fenceEvent struct {
		byteEnd int  // byte position just after the fence line's '\n'
		open    bool // true when we are inside a fence after this line
	}
	var fenceEvents []fenceEvent
	inFence := false
	lineStart := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			line := src[lineStart:i]
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = !inFence
				byteAfterNL := i + 1
				if byteAfterNL > len(src) {
					byteAfterNL = len(src)
				}
				fenceEvents = append(fenceEvents, fenceEvent{byteEnd: byteAfterNL, open: inFence})
			}
			lineStart = i + 1
		}
	}

	// insideFenceAt reports whether byte position pos is inside an open fence
	// by finding the last fence event before pos and checking its open state.
	insideFenceAt := func(pos int) bool {
		last := -1
		for j, fe := range fenceEvents {
			if fe.byteEnd <= pos {
				last = j
			}
		}
		if last < 0 {
			return false
		}
		return fenceEvents[last].open
	}

	// Return the latest candidate that is not inside a fence.
	for i := len(candidates) - 1; i >= 0; i-- {
		if !insideFenceAt(candidates[i].end) {
			return candidates[i].end
		}
	}
	return 0
}
