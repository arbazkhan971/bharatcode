package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// chatScrollStep is the number of lines one mouse-wheel notch scrolls the chat
// viewport. A small step keeps wheel scrolling readable on dense transcripts.
const chatScrollStep = 3

// handleMouseWheel scrolls the chat viewport in response to a wheel event. Wheel
// up reveals older content (increasing the scroll offset); wheel down returns
// toward the newest content. The offset is bound-checked at render time, so this
// only needs to nudge it and never produces an out-of-range value the view must
// reject. Horizontal wheel events are ignored.
func (m *model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.chatScroll += chatScrollStep
	case tea.MouseWheelDown:
		m.chatScroll -= chatScrollStep
		if m.chatScroll < 0 {
			m.chatScroll = 0
		}
	}
	return m, nil
}

// scrollTopSentinel is an intentionally over-large scroll offset used to request
// the top of the scrollback. clampChat bounds it to the true maximum at render
// time and writes the clamped value back, so Home need not know the rendered
// line count to land exactly on the oldest line.
const scrollTopSentinel = 1 << 30

// chatViewportHeight returns the number of chat rows currently visible: the
// laid-out chat height, less the one row the tab bar borrows when more than one
// tab is open. It mirrors the height clampChat renders into, so a page scroll
// moves by exactly what the user sees. It never returns less than one.
func (m *model) chatViewportHeight() int {
	h := m.layout.chat.H
	if len(m.tabs) > 1 {
		h--
	}
	if h < 1 {
		return 1
	}
	return h
}

// chatPageStep returns the lines one PageUp/PageDown moves the chat viewport: a
// full page minus one row of overlap, so a landmark line carries across the jump
// and the reader keeps their place, matching the page behaviour of a pager. It
// never returns less than one.
func (m *model) chatPageStep() int {
	if step := m.chatViewportHeight() - 1; step > 0 {
		return step
	}
	return 1
}

// scrollChatPageUp reveals an older page of the transcript. The offset is
// bound-checked at render time, so this only nudges it.
func (m *model) scrollChatPageUp() {
	m.chatScroll += m.chatPageStep()
}

// scrollChatPageDown returns one page toward the newest content, clamped so it
// never underflows past the bottom.
func (m *model) scrollChatPageDown() {
	m.chatScroll -= m.chatPageStep()
	if m.chatScroll < 0 {
		m.chatScroll = 0
	}
}

// scrollChatTop jumps to the oldest content; clampChat pins the sentinel to the
// real top at render time.
func (m *model) scrollChatTop() {
	m.chatScroll = scrollTopSentinel
}

// scrollChatBottom re-anchors to the newest content.
func (m *model) scrollChatBottom() {
	m.chatScroll = 0
}

// copyTarget selects what /copy writes to the clipboard.
type copyTarget int

const (
	// copyLastAssistant copies the most recent assistant message (the default).
	copyLastAssistant copyTarget = iota
	// copyTranscript copies the entire visible conversation.
	copyTranscript
)

// handleCopy copies chat content to the system clipboard. With no argument (or
// "last") it copies the most recent assistant message; "all"/"transcript" copies
// the whole visible conversation. It surfaces a confirmation dialog naming what
// was copied, an explanatory dialog when there is nothing to copy, and a
// graceful hint when no clipboard utility is available. The actual write goes
// through the injectable copyToClipboard seam so tests observe the copied text.
func (m *model) handleCopy(text string) (tea.Model, tea.Cmd) {
	target, err := parseCopyTarget(text)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "copy", Title: "Copy", Body: err.Error(), Theme: m.theme})
		return m, nil
	}

	var (
		content string
		label   string
	)
	switch target {
	case copyTranscript:
		content = m.chat.TranscriptText()
		label = "Copied transcript to the clipboard."
	default:
		content = m.chat.LastAssistantText()
		label = "Copied the last assistant message to the clipboard."
	}

	if strings.TrimSpace(content) == "" {
		m.dialogs.Push(&dialog.Text{DialogID: "copy", Title: "Copy", Body: "Nothing to copy yet.", Theme: m.theme})
		return m, nil
	}

	copyFn := m.copyToClipboard
	if copyFn == nil {
		copyFn = systemClipboardCopy
	}
	if err := copyFn(content); err != nil {
		body := "Could not copy: " + err.Error()
		if errors.Is(err, errNoClipboardTool) {
			body = "No clipboard utility found. Install pbcopy, wl-copy, xclip, or xsel to enable /copy."
		}
		m.dialogs.Push(&dialog.Text{DialogID: "copy", Title: "Copy", Body: body, Theme: m.theme})
		return m, nil
	}

	m.dialogs.Push(&dialog.Text{DialogID: "copy", Title: "Copied", Body: label, Theme: m.theme})
	return m, nil
}

// parseCopyTarget reads the optional argument of a "/copy" line. An empty
// argument (or "last"/"latest"/"message") copies the last assistant message;
// "all"/"transcript"/"chat" copies the whole conversation. Any other argument is
// an error.
func parseCopyTarget(text string) (copyTarget, error) {
	_, args := splitSlash(text)
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "", "last", "latest", "message", "msg":
		return copyLastAssistant, nil
	case "all", "transcript", "chat", "conversation":
		return copyTranscript, nil
	default:
		return copyLastAssistant, fmt.Errorf("unknown copy target %q (use \"last\" or \"all\")", args)
	}
}
