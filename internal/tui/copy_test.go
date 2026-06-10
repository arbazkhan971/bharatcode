package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// appendMsg seeds the chat list with a complete message of the given role and
// text so /copy has real content to read.
func appendMsg(m *model, id string, role message.Role, text string) {
	m.chat.Append(message.Message{
		ID:      id,
		Role:    role,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	})
}

// TestSlashCopy_CopiesLastAssistantMessage is the /copy contract test: the
// command must invoke the injected clipboard writer with the most recent
// assistant message's text and surface a confirmation dialog.
func TestSlashCopy_CopiesLastAssistantMessage(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	var copied string
	calls := 0
	m.copyToClipboard = func(text string) error {
		calls++
		copied = text
		return nil
	}

	appendMsg(m, "u1", message.RoleUser, "what is the answer")
	appendMsg(m, "a1", message.RoleAssistant, "the first reply")
	appendMsg(m, "u2", message.RoleUser, "and again")
	appendMsg(m, "a2", message.RoleAssistant, "the final answer is 42")

	m.input.WriteString("/copy")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Equal(t, 1, calls, "/copy must invoke the clipboard writer exactly once")
	require.Equal(t, "the final answer is 42", copied,
		"/copy must copy the most recent assistant message, not an earlier one or a user message")
	require.True(t, m.dialogs.Contains("copy"), "/copy must surface a confirmation dialog")
	require.Contains(t, plainText(m.dialogs.Render(200)), "Copied",
		"the confirmation must report a successful copy")
}

// TestSlashCopy_All_CopiesWholeTranscript asserts "/copy all" writes the full
// visible conversation (both user and assistant turns) to the clipboard.
func TestSlashCopy_All_CopiesWholeTranscript(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	var copied string
	m.copyToClipboard = func(text string) error {
		copied = text
		return nil
	}

	appendMsg(m, "u1", message.RoleUser, "fix the parser")
	appendMsg(m, "a1", message.RoleAssistant, "parser fixed")

	m.input.WriteString("/copy all")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Contains(t, copied, "fix the parser", "transcript copy must include the user turn")
	require.Contains(t, copied, "parser fixed", "transcript copy must include the assistant turn")
	require.True(t, m.dialogs.Contains("copy"))
}

// TestSlashCopy_NothingToCopy asserts /copy is a no-op with an explanatory
// dialog when there is no assistant message yet, and never calls the writer.
func TestSlashCopy_NothingToCopy(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	calls := 0
	m.copyToClipboard = func(string) error { calls++; return nil }

	// Only a user message present: there is no assistant reply to copy.
	appendMsg(m, "u1", message.RoleUser, "hello?")

	m.input.WriteString("/copy")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Equal(t, 0, calls, "/copy must not call the writer when there is nothing to copy")
	require.True(t, m.dialogs.Contains("copy"))
	require.Contains(t, plainText(m.dialogs.Render(200)), "Nothing to copy")
}

// TestSlashCopy_WriterError_Degrades asserts a clipboard-writer failure is
// surfaced as a dialog rather than crashing, and that the no-utility case shows
// the install hint.
func TestSlashCopy_WriterError_Degrades(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.copyToClipboard = func(string) error { return errNoClipboardTool }
	appendMsg(m, "a1", message.RoleAssistant, "some reply")

	m.input.WriteString("/copy")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.True(t, m.dialogs.Contains("copy"))
	require.Contains(t, plainText(m.dialogs.Render(200)), "No clipboard utility found",
		"a missing clipboard utility must degrade to an install hint")
}

// TestSlashCopy_UnknownTarget_ShowsError asserts an unrecognized argument is
// rejected with a helpful dialog and never calls the writer.
func TestSlashCopy_UnknownTarget_ShowsError(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	calls := 0
	m.copyToClipboard = func(string) error { calls++; return nil }
	appendMsg(m, "a1", message.RoleAssistant, "some reply")

	m.input.WriteString("/copy sideways")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Equal(t, 0, calls, "an unknown copy target must not call the writer")
	require.Contains(t, plainText(m.dialogs.Render(200)), "unknown copy target")
}

// TestParseCopyTarget covers the argument grammar directly so the alias set is
// pinned without driving the whole model.
func TestParseCopyTarget(t *testing.T) {
	t.Parallel()

	last := []string{"/copy", "/copy last", "/copy LATEST", "/copy message", "/copy msg"}
	for _, in := range last {
		got, err := parseCopyTarget(in)
		require.NoError(t, err, in)
		require.Equal(t, copyLastAssistant, got, in)
	}
	all := []string{"/copy all", "/copy transcript", "/copy CHAT", "/copy conversation"}
	for _, in := range all {
		got, err := parseCopyTarget(in)
		require.NoError(t, err, in)
		require.Equal(t, copyTranscript, got, in)
	}
	_, err := parseCopyTarget("/copy bogus")
	require.Error(t, err)
}

// TestChatViewportHeight_MatchesRenderMain asserts that chatViewportHeight()
// returns the same effective chat area that renderMain carves out for the
// transcript. The two must agree so page-scroll steps match what is visible.
// It exercises the M2 fix: the info strip row is now deducted in both paths
// through the shared headerExtraRows helper.
func TestChatViewportHeight_MatchesRenderMain(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// Set a known terminal size so the arithmetic is predictable.
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Seed enough content to overflow the viewport so clampChat actually uses
	// the full chatH budget (it clamps to min(content, chatH)).
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, uniqueLine(i))
	}
	appendMsg(m, "u1", message.RoleUser, strings.Join(lines, "\n"))

	// chatViewportHeight must agree with what renderMain renders.
	vpH := m.chatViewportHeight()
	require.Greater(t, vpH, 0, "chatViewportHeight must be positive")

	// Count the visible content lines in the rendered output. renderMain wraps
	// the chat in a clampChat call that limits to exactly chatH lines of the
	// body. The header-extra deduction is the focus: if the two paths disagree,
	// the frame will be one row too tall and the transcript will be clipped.
	rendered := stripANSI(m.renderMain())
	chatContent := 0
	for _, ln := range strings.Split(rendered, "\n") {
		if strings.Contains(ln, "LINE-MARK-") {
			chatContent++
		}
	}
	require.GreaterOrEqual(t, chatContent, 1, "rendered chat must contain at least one content line")
	// The viewport height must be positive and equal to what clampChat allocated;
	// a mismatch (off-by-one) would cause chatContent to exceed vpH.
	require.LessOrEqual(t, chatContent, vpH,
		"rendered content lines must not exceed chatViewportHeight (off-by-one check)")
}

// TestSystemClipboardCopy_NoUtility asserts the real shell-out path degrades
// gracefully: with PATH emptied so no clipboard utility resolves, it returns
// errNoClipboardTool rather than running anything.
func TestSystemClipboardCopy_NoUtility(t *testing.T) {
	t.Setenv("PATH", "")
	err := systemClipboardCopy("anything")
	require.ErrorIs(t, err, errNoClipboardTool,
		"with no clipboard utility on PATH the copy must report the missing-utility error")
}

// TestMouseWheel_ScrollsChat is the mouse-scroll contract test: a wheel-up
// message must scroll the chat viewport so the rendered window reveals older
// lines, and a subsequent wheel-down must return toward the newest content.
// Behavior is asserted on the actual rendered window, not on internal counters.
func TestMouseWheel_ScrollsChat(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// Size the window so the chat viewport is a known, small height and seed more
	// lines than it can show, guaranteeing the content is scrollable.
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	chatH := m.layout.chat.H
	require.Greater(t, chatH, 0)

	// Seed the lines as a user message: user content is plain-wrapped (not run
	// through glamour markdown), so each marker survives verbatim and the scroll
	// window can be asserted exactly. The scroll path under test is identical.
	var lines []string
	for i := 0; i < chatH*3; i++ {
		lines = append(lines, uniqueLine(i))
	}
	appendMsg(m, "u1", message.RoleUser, strings.Join(lines, "\n"))

	firstLine := uniqueLine(0)
	lastLine := uniqueLine(len(lines) - 1)
	rendered := func() string { return stripANSI(m.renderMain()) }

	// At rest the viewport is anchored to the bottom: newest line visible, oldest
	// not.
	atRest := rendered()
	require.Contains(t, atRest, lastLine, "the bottom-anchored view must show the newest line")
	require.NotContains(t, atRest, firstLine, "the oldest line must be off-screen before scrolling")

	// Wheel up scrolls toward older content. Several notches walk the window to
	// the very top, where the oldest line becomes visible.
	for i := 0; i < len(lines); i++ {
		_, _ = m.Update(wheel(tea.MouseWheelUp))
		if strings.Contains(rendered(), firstLine) {
			break
		}
	}
	require.Contains(t, rendered(), firstLine,
		"wheel-up must scroll the chat far enough to reveal the oldest line")
	require.Greater(t, m.chatScroll, 0, "wheel-up must increase the scroll offset")
	require.NotContains(t, rendered(), lastLine,
		"once scrolled to the top, the newest line must have moved off-screen")

	// Wheel down returns toward the newest content. Enough notches re-anchor to
	// the bottom where the newest line is visible again.
	for i := 0; i < len(lines); i++ {
		_, _ = m.Update(wheel(tea.MouseWheelDown))
		if m.chatScroll == 0 {
			break
		}
	}
	require.Equal(t, 0, m.chatScroll, "wheel-down must return the viewport to the bottom")
	require.Contains(t, rendered(), lastLine,
		"after scrolling back down the newest line must be visible again")
}

// TestMouseWheelDown_AtBottom_NoUnderflow asserts wheel-down at the bottom does
// not drive the scroll offset negative (which would corrupt the render window).
func TestMouseWheelDown_AtBottom_NoUnderflow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	appendMsg(m, "a1", message.RoleAssistant, "short content")
	require.Equal(t, 0, m.chatScroll)

	_, _ = m.Update(wheel(tea.MouseWheelDown))
	require.Equal(t, 0, m.chatScroll, "wheel-down at the bottom must not underflow below zero")
}

// uniqueLine returns a distinct, easily searchable line for index i.
func uniqueLine(i int) string {
	return "LINE-MARK-" + lineSuffix(i)
}

func lineSuffix(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

// wheel builds a mouse-wheel message for the given button.
func wheel(button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg(tea.Mouse{Button: button})
}

// anyANSI matches any CSI escape sequence so line-structure assertions survive
// lipgloss styling without collapsing newlines the way plainText would.
var anyANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// stripANSI removes ANSI escape sequences while preserving newlines, so the
// rendered chat window can be searched line by line.
func stripANSI(s string) string {
	return anyANSI.ReplaceAllString(s, "")
}
