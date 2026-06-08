// Package chat renders conversation messages with streaming-cache support.
//
// The transcript is drawn as an activity stream: each turn is led by an accent
// bullet, tool and command turns by a bold action verb, their sub-output indented
// under a muted "└" connector with long output elided to "… +N lines", faint
// horizontal rules separate turns, and added/removed lines inside command output
// are tinted green/red. The rendered string is the content the main model pushes
// into its scrollable viewport.
package chat

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

// subOutputElideOver is the line count past which a turn's sub-output (command
// or tool result) is collapsed: the first subOutputHead lines are kept and the
// remainder is replaced with a faint "… +N lines" hint, so a long log does not
// bury the conversation while the head still shows what happened. Output at or
// below the threshold renders in full.
const (
	subOutputElideOver = 12
	subOutputHead      = 10
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
	createdAt   time.Time
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
		if !msg.CreatedAt.IsZero() {
			l.items[idx].createdAt = msg.CreatedAt
		}
		l.items[idx].cachedWidth = 0
		l.items[idx].cachedBody = ""
		return
	}
	l.index[id] = len(l.items)
	l.items = append(l.items, item{id: id, role: msg.Role, body: body, createdAt: msg.CreatedAt})
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

// SetRole overrides the role of the streamed item with the given id. Stream
// creates items as assistant turns; the user-prompt echo uses this to relabel
// its item as a user turn so it renders with the "user" header and accent rather
// than masquerading as the assistant.
func (l *List) SetRole(id string, role message.Role) {
	if idx, ok := l.index[id]; ok {
		l.items[idx].role = role
		l.items[idx].cachedWidth = 0
		l.items[idx].cachedBody = ""
	}
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
// with smart case: a term typed in all lower case matches case-insensitively,
// while a term carrying any upper-case letter matches case-sensitively, the way
// ripgrep, fzf, and opencode's search disambiguate intent without a separate
// toggle. text is split on "\n", so the returned indices address lines of that
// same split (line 0 is the first line). An empty term, or text with no match,
// returns nil. It is a pure helper so both the transcript line space and the
// rendered chat line space can be searched with one implementation and the
// caller positions the viewport against whichever it scrolls.
func SearchLines(text string, term string) []int {
	if term == "" {
		return nil
	}
	fold := SearchFold(term)
	needle := term
	if fold {
		needle = strings.ToLower(term)
	}
	var matches []int
	for i, line := range strings.Split(text, "\n") {
		hay := line
		if fold {
			hay = strings.ToLower(line)
		}
		if strings.Contains(hay, needle) {
			matches = append(matches, i)
		}
	}
	return matches
}

// SearchFold reports whether term should be matched case-insensitively under the
// smart-case rule SearchLines applies: a term with no upper-case letter folds
// case (matches insensitively), while one carrying any upper-case letter is
// matched exactly. Exposed so the highlighter can mark exactly the occurrences
// SearchLines counted, keeping the visible emphasis aligned with the navigated
// matches.
func SearchFold(term string) bool {
	for _, r := range term {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

// SearchLinesRe reports the indices of lines in text that match re, following
// the same "\n"-split line space as SearchLines so the returned indices can be
// used interchangeably with those from SearchLines to scroll and highlight. A
// pattern that matches nothing, or an empty text, returns nil.
func SearchLinesRe(text string, re *regexp.Regexp) []int {
	if re == nil {
		return nil
	}
	var matches []int
	for i, line := range strings.Split(text, "\n") {
		if re.MatchString(line) {
			matches = append(matches, i)
		}
	}
	return matches
}

// Render returns the rendered activity-stream transcript for width. Turns are
// separated by a soft dotted rule so the eye can tell where one turn ends and
// the next begins without the heavy visual weight of a solid full-width line.
func (l *List) Render(width int) string {
	if width < 1 {
		width = 1
	}
	var b strings.Builder
	for i := range l.items {
		if i > 0 {
			// A blank line, a faint dotted rule, and a blank line create generous
			// breathing room between turns without a heavy solid separator.
			b.WriteString("\n")
			b.WriteString(styles.SoftRule(width))
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
	header := l.itemHeader(it)

	// A turn whose body reads as a tool or command action ("tool: edit", a
	// "$ go test" command line, or a tool-role result) renders in the command
	// style: a bold action-verb header and its output indented under a muted
	// connector, with long output elided and added/removed lines tinted. The
	// block uses UserBlock chrome (muted left bar) since tool/command turns are
	// not assistant prose and shouldn't claim the saffron accent.
	if verb, rest, ok := commandTurn(it); ok {
		raw := renderCommandTurn(header, verb, rest, width)
		it.cachedWidth = width
		it.cachedBody = applyTurnBlock(it.role, raw, width)
		return it.cachedBody
	}

	// Render assistant prose as markdown once it is complete. While a message
	// is still streaming we keep the fast plain wrap so each delta does not pay
	// the cost of a full markdown re-render (and to avoid flicker on partial,
	// not-yet-valid markdown). Markdown content is rendered within the block
	// width minus the left bar + padding so it clips within the frame.
	if l.md != nil && it.role == message.RoleAssistant && !it.streaming && it.body != "" {
		// Reserve 2 columns for the left bar + padding drawn by AssistantBlock.
		if rendered, ok := l.md.Render(it.body, width-2); ok {
			body := strings.TrimRight(rendered, "\n")
			raw := header + "\n" + body
			it.cachedWidth = width
			it.cachedBody = applyTurnBlock(it.role, raw, width)
			return it.cachedBody
		}
	}

	body := wrap(it.body, width-4)
	if it.streaming {
		body += " ▌"
	}
	raw := header + "\n" + indent(body, "  ")
	it.cachedWidth = width
	it.cachedBody = applyTurnBlock(it.role, raw, width)
	return it.cachedBody
}

// applyTurnBlock wraps the rendered turn body in the role-appropriate left-bar
// block style: saffron AccentBar + slight indent for assistant, muted faint bar
// for user/tool turns. The block gives every turn a framed left edge so the
// reader instantly tells who is speaking. width is the available pane width;
// the block sets its own width so the left bar clips with the pane.
func applyTurnBlock(role message.Role, body string, width int) string {
	// The left border + padding takes 2 columns; leave the rest for content.
	contentW := width - 2
	if contentW < 1 {
		contentW = 1
	}
	switch role {
	case message.RoleAssistant:
		return styles.AssistantBlock.Width(contentW).Render(body)
	default:
		return styles.UserBlock.Width(contentW).Render(body)
	}
}

// itemHeader returns the bullet-led header line for a turn: an accent bullet, a
// role label styled by who is speaking (saffron-bold for the assistant, muted for
// the user), and an optional "· HH:MM" timestamp suffix in the faintest chrome.
func (l *List) itemHeader(it *item) string {
	ts := ""
	if !it.createdAt.IsZero() {
		ts = formatTimestamp(it.createdAt)
	}
	return styles.Bullet() + " " + styles.RoleLabel(string(it.role), ts)
}

// commandTurn reports whether a turn's flattened body reads as a tool or command
// action and, if so, returns the bold verb to lead it with and the remaining body
// (the action's output). A turn qualifies when its first line is a "tool: <name>"
// marker (as flatten emits for a ToolUseBlock), a shell prompt ("$ ", "❯ "), or
// when it is a tool-role result. The verb is the human-readable action; rest is
// the sub-output rendered under the connector. A streaming turn never qualifies —
// it is prose being typed live, not a finished command block.
func commandTurn(it *item) (verb, rest string, ok bool) {
	if it.streaming {
		return "", "", false
	}
	first, tail := splitFirstLine(it.body)
	trimmed := strings.TrimSpace(first)
	switch {
	case strings.HasPrefix(trimmed, "tool: "):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "tool: "))
		return verbForTool(name), tail, true
	case strings.HasPrefix(trimmed, "$ "), strings.HasPrefix(trimmed, "❯ "):
		// Keep the command line itself as the first output line so the reader
		// sees what ran above its output.
		return "Running", it.body, true
	case it.role == message.RoleTool:
		return "Result", it.body, true
	default:
		return "", "", false
	}
}

// verbForTool maps a tool name to the bold action verb that leads its turn. A
// known name gets an imperative present participle ("Editing", "Reading"); an
// unknown name is shown verbatim so a new tool still reads sensibly.
func verbForTool(name string) string {
	switch strings.ToLower(name) {
	case "edit", "write", "apply_patch", "str_replace":
		return "Editing"
	case "read", "view", "cat":
		return "Reading"
	case "bash", "shell", "exec", "run":
		return "Running"
	case "search", "grep", "glob", "find":
		return "Searching"
	case "":
		return "tool"
	default:
		return name
	}
}

// renderCommandTurn renders a tool/command turn: the bullet header with the bold
// verb appended, then the output indented under a muted "└" connector. Long
// output is elided to its head with a faint "… +N lines" hint, and added/removed
// lines are tinted so a diff in the output reads at a glance. Empty output
// renders the header alone.
func renderCommandTurn(header, verb, rest string, width int) string {
	var b strings.Builder
	b.WriteString(header)
	if verb != "" {
		b.WriteString(" ")
		b.WriteString(styles.Verb.Render(verb))
	}

	out := strings.TrimRight(rest, "\n")
	if strings.TrimSpace(out) == "" {
		return b.String()
	}

	indentW := width - 4
	for _, line := range elide(strings.Split(out, "\n"), subOutputElideOver, subOutputHead) {
		b.WriteString("\n")
		b.WriteString(styles.Connector())
		b.WriteString(" ")
		b.WriteString(styleOutputLine(line, indentW))
	}
	return b.String()
}

// styleOutputLine wraps a single sub-output line and tints it when it reads as a
// diff addition or removal, so an inline diff in command output carries the
// green/red the activity stream reserves for changes. The elision hint is drawn
// faint; every other line renders muted, the recessive weight sub-output takes.
func styleOutputLine(line string, width int) string {
	wrapped := wrap(line, width)
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return styles.DiffAdd.Render(wrapped)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return styles.DiffDel.Render(wrapped)
	case isElisionHint(line):
		return styles.Faint.Render(wrapped)
	default:
		return styles.Muted.Render(wrapped)
	}
}

// elide collapses a long block of output lines: when there are more than over
// lines, the first head are kept and the rest replaced with a single
// "… +N lines" hint, so a long log shows its head without burying the transcript.
// Shorter blocks are returned unchanged.
func elide(lines []string, over, head int) []string {
	if len(lines) <= over {
		return lines
	}
	hidden := len(lines) - head
	out := make([]string, 0, head+1)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("… +%d lines", hidden))
	return out
}

// isElisionHint reports whether a sub-output line is the "… +N lines" hint elide
// inserts, so the renderer can draw it faint rather than muted.
func isElisionHint(line string) bool {
	return strings.HasPrefix(line, "… +") && strings.HasSuffix(line, " lines")
}

// splitFirstLine splits s into its first line and the remainder (everything after
// the first newline). A string with no newline returns itself and "".
func splitFirstLine(s string) (first, rest string) {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
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

// formatTimestamp returns a compact time string for a message header. Messages
// from today show only HH:MM; older messages include the date so the reader
// always knows when a turn happened without cluttering recent conversation.
func formatTimestamp(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}
