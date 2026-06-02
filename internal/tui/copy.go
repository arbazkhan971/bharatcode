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
