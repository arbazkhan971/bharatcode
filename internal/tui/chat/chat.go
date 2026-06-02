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
	body := wrap(it.body, width-4)
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
