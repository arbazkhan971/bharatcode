package tui

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func keyPgUp() tea.KeyPressMsg   { return keySpecial("pgup", tea.KeyPgUp) }
func keyPgDown() tea.KeyPressMsg { return keySpecial("pgdown", tea.KeyPgDown) }
func keyHome() tea.KeyPressMsg   { return keySpecial("home", tea.KeyHome) }
func keyEnd() tea.KeyPressMsg    { return keySpecial("end", tea.KeyEnd) }

// seedScrollableChat fills the chat with enough distinct lines to overflow the
// viewport several times and returns the markers for the oldest and newest line,
// so a test can assert which end of the scrollback is visible. The lines are sent
// as a user message: user content is plain-wrapped (not run through glamour), so
// each marker survives verbatim in the rendered window.
func seedScrollableChat(m *model) (firstLine, lastLine string) {
	chatH := m.chatViewportHeight()
	var lines []string
	for i := 0; i < chatH*3; i++ {
		lines = append(lines, uniqueLine(i))
	}
	appendMsg(m, "u1", message.RoleUser, strings.Join(lines, "\n"))
	return uniqueLine(0), uniqueLine(len(lines) - 1)
}

// TestPageScroll_RevealsOlderThenReturns asserts PageUp walks the chat viewport
// toward older content a page at a time and PageDown returns it to the bottom,
// mirroring the mouse-wheel scroll path from the keyboard.
func TestPageScroll_RevealsOlderThenReturns(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Greater(t, m.chatViewportHeight(), 0)
	firstLine, lastLine := seedScrollableChat(m)
	rendered := func() string { return stripANSI(m.renderMain()) }

	// At rest the newest line is shown and the oldest is off-screen.
	atRest := rendered()
	require.Contains(t, atRest, lastLine, "the bottom-anchored view must show the newest line")
	require.NotContains(t, atRest, firstLine, "the oldest line must be off-screen before scrolling")

	// PageUp a bounded number of times walks to the very top.
	for i := 0; i < 10; i++ {
		_, _ = m.Update(keyPgUp())
		if strings.Contains(rendered(), firstLine) {
			break
		}
	}
	require.Contains(t, rendered(), firstLine, "PageUp must reveal the oldest line")
	require.Greater(t, m.chatScroll, 0, "PageUp must increase the scroll offset")

	// PageDown returns toward the newest content.
	for i := 0; i < 10; i++ {
		_, _ = m.Update(keyPgDown())
		if m.chatScroll == 0 {
			break
		}
	}
	require.Equal(t, 0, m.chatScroll, "PageDown must return the viewport to the bottom")
	require.Contains(t, rendered(), lastLine, "after paging back down the newest line must be visible")
}

// TestPageStep_AdvancesByAPage asserts a single PageUp moves the offset by a
// whole page (less one overlap row), distinguishing it from the smaller
// mouse-wheel notch.
func TestPageStep_AdvancesByAPage(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	seedScrollableChat(m)
	step := m.chatPageStep()
	require.Greater(t, step, 1, "a page step must exceed a single mouse-wheel notch")
	require.Greater(t, step, chatScrollStep, "PageUp must move farther than one wheel notch")

	_, _ = m.Update(keyPgUp())
	require.Equal(t, step, m.chatScroll, "one PageUp must advance the offset by exactly one page")
}

// TestHomeEnd_JumpToTopAndBottom asserts Home lands on the oldest line in one
// keystroke and End re-anchors to the newest, the way a pager binds the keys.
func TestHomeEnd_JumpToTopAndBottom(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	firstLine, lastLine := seedScrollableChat(m)
	rendered := func() string { return stripANSI(m.renderMain()) }

	_, _ = m.Update(keyHome())
	top := rendered()
	require.Contains(t, top, firstLine, "Home must jump to the oldest line")
	require.NotContains(t, top, lastLine, "at the top the newest line must be off-screen")

	_, _ = m.Update(keyEnd())
	require.Equal(t, 0, m.chatScroll, "End must re-anchor the viewport to the bottom")
	bottom := rendered()
	require.Contains(t, bottom, lastLine, "End must show the newest line")
	require.NotContains(t, bottom, firstLine, "at the bottom the oldest line must be off-screen")
}

// TestPageDown_AtBottom_NoUnderflow asserts PageDown at the bottom keeps the
// offset pinned at zero rather than driving it negative.
func TestPageDown_AtBottom_NoUnderflow(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	appendMsg(m, "a1", message.RoleAssistant, "short content")
	require.Equal(t, 0, m.chatScroll)

	_, _ = m.Update(keyPgDown())
	require.Equal(t, 0, m.chatScroll, "PageDown at the bottom must not underflow below zero")
}
