// Package chat renders conversation messages with streaming-cache support.
package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

// List stores rendered chat items and invalidates only changed messages.
type List struct {
	items         []item
	index         map[string]int
	renderRegions int
	md            *markdownRenderer
}

type item struct {
	id          string
	role        message.Role
	body        string
	streaming   bool
	cachedWidth int
	cachedBody  string
}

// New constructs an empty chat list.
func New() *List {
	return &List{index: make(map[string]int)}
}

// EnableMarkdown turns on glamour markdown rendering for finished assistant
// messages using the named style (for example "dark" or "light"). Calling it
// resets the render cache so existing messages re-render. Passing an empty
// string disables markdown rendering.
func (l *List) EnableMarkdown(style string) {
	if style == "" {
		l.md = nil
	} else {
		l.md = newMarkdownRenderer(style)
	}
	for i := range l.items {
		l.items[i].cachedWidth = 0
		l.items[i].cachedBody = ""
	}
}

// Append adds a complete message to the visible list.
func (l *List) Append(msg message.Message) {
	if l.index == nil {
		l.index = make(map[string]int)
	}
	id := msg.ID
	if id == "" {
		id = fmt.Sprintf("msg-%d", len(l.items)+1)
	}
	body := flatten(msg)
	if idx, ok := l.index[id]; ok {
		l.items[idx].role = msg.Role
		l.items[idx].body = body
		l.items[idx].cachedWidth = 0
		l.items[idx].cachedBody = ""
		return
	}
	l.index[id] = len(l.items)
	l.items = append(l.items, item{id: id, role: msg.Role, body: body})
}

// Stream appends delta to a streaming assistant message.
func (l *List) Stream(id string, delta string) {
	if l.index == nil {
		l.index = make(map[string]int)
	}
	if id == "" {
		id = "stream"
	}
	idx, ok := l.index[id]
	if !ok {
		l.index[id] = len(l.items)
		l.items = append(l.items, item{id: id, role: message.RoleAssistant, streaming: true})
		idx = len(l.items) - 1
	}
	l.items[idx].body += delta
	l.items[idx].streaming = true
	l.items[idx].cachedWidth = 0
	l.items[idx].cachedBody = ""
}

// FinishStream marks a streaming message complete.
func (l *List) FinishStream(id string) {
	if idx, ok := l.index[id]; ok {
		l.items[idx].streaming = false
		l.items[idx].cachedWidth = 0
		l.items[idx].cachedBody = ""
	}
}

// Reindex detaches id from the live index so a later Stream or Append with the
// same id begins a fresh item rather than mutating the existing one. The
// already-rendered item is retained in the visible list. Calling Reindex on an
// unknown id is a no-op.
func (l *List) Reindex(id string) {
	if l.index == nil {
		return
	}
	delete(l.index, id)
}

// Clear removes visible messages.
func (l *List) Clear() {
	l.items = nil
	l.index = make(map[string]int)
	l.renderRegions = 0
}

// LastAssistantText returns the raw (unrendered) body of the most recent
// assistant message, or "" when no assistant message is present. It returns the
// source text rather than the ANSI-styled render so copied output is plain and
// paste-friendly.
func (l *List) LastAssistantText() string {
	for i := len(l.items) - 1; i >= 0; i-- {
		if l.items[i].role == message.RoleAssistant {
			return l.items[i].body
		}
	}
	return ""
}

// FirstUserText returns the raw (unrendered) body of the first user message in
// the list, or "" when no user message is present. It backs a content-derived
// session label — the conversation's opening prompt — the way the session
// switchers in Claude Code and opencode title a conversation by how it began,
// rather than by an opaque id. The source text is returned (not the styled
// render) so the caller can trim and truncate it freely.
func (l *List) FirstUserText() string {
	for i := range l.items {
		if l.items[i].role == message.RoleUser {
			return l.items[i].body
		}
	}
	return ""
}

// TranscriptText returns the whole visible conversation as plain text, one
// message per block separated by blank lines. Each block is prefixed with its
// role (for example "user" or "assistant"). It returns "" when the list is
// empty.
func (l *List) TranscriptText() string {
	if len(l.items) == 0 {
		return ""
	}
	var b strings.Builder
	for i := range l.items {
		if i > 0 {
			b.WriteString("\n\n")
		}
		role := string(l.items[i].role)
		if role == "" {
			role = "message"
		}
		b.WriteString(role)
		b.WriteString(":\n")
		b.WriteString(l.items[i].body)
	}
	return b.String()
}

// SearchLines reports the indices of lines in text that contain term, matched
// case-insensitively. text is split on "\n", so the returned indices address
// lines of that same split (line 0 is the first line). An empty term, or text
// with no match, returns nil. It is a pure helper so both the transcript line
// space and the rendered chat line space can be searched with one implementation
// and the caller positions the viewport against whichever it scrolls.
func SearchLines(text string, term string) []int {
	if term == "" {
		return nil
	}
	needle := strings.ToLower(term)
	var matches []int
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			matches = append(matches, i)
		}
	}
	return matches
}

// Render returns the rendered message list for width.
func (l *List) Render(width int) string {
	if width < 1 {
		width = 1
	}
	var b strings.Builder
	for i := range l.items {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(l.renderItem(i, width))
	}
	return b.String()
}

// RenderRegions returns the number of item render cache misses.
func (l *List) RenderRegions() int {
	return l.renderRegions
}

func (l *List) renderItem(idx int, width int) string {
	it := &l.items[idx]
	if it.cachedWidth == width && it.cachedBody != "" {
		return it.cachedBody
	}

	l.renderRegions++
	prefix := string(it.role)
	if prefix == "" {
		prefix = "message"
	}

	var body string
	// Render assistant prose as markdown once it is complete. While a message
	// is still streaming we keep the fast plain wrap so each delta does not pay
	// the cost of a full markdown re-render (and to avoid flicker on partial,
	// not-yet-valid markdown).
	if l.md != nil && it.role == message.RoleAssistant && !it.streaming && it.body != "" {
		if rendered, ok := l.md.Render(it.body, width-2); ok {
			body = strings.TrimRight(rendered, "\n")
			it.cachedWidth = width
			it.cachedBody = prefix + "\n" + body
			return it.cachedBody
		}
	}

	body = wrap(it.body, width-4)
	if it.streaming {
		body += " ▌"
	}
	it.cachedWidth = width
	it.cachedBody = prefix + "\n" + indent(body, "  ")
	return it.cachedBody
}

func flatten(msg message.Message) string {
	var parts []string
	for _, block := range msg.Content {
		switch b := block.(type) {
		case message.TextBlock:
			parts = append(parts, b.Text)
		case message.ToolUseBlock:
			parts = append(parts, "tool: "+b.Name)
		case message.ToolResultBlock:
			parts = append(parts, b.Content)
		case message.ThinkingBlock:
			parts = append(parts, b.Text)
		case message.ImageBlock:
			parts = append(parts, fmt.Sprintf("image: %s (%s)", b.MimeType, util.HumanBytes(int64(len(b.Data)))))
		default:
			parts = append(parts, string(block.Type()))
		}
	}
	if len(parts) == 0 && !msg.CreatedAt.IsZero() {
		return msg.CreatedAt.Format(time.RFC3339)
	}
	return strings.Join(parts, "\n")
}

func wrap(s string, width int) string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		remaining := line
		for len([]rune(remaining)) > width {
			r := []rune(remaining)
			out = append(out, string(r[:width]))
			remaining = string(r[width:])
		}
		out = append(out, remaining)
	}
	return strings.Join(out, "\n")
}

func indent(s string, prefix string) string {
	if s == "" {
		return prefix
	}
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
