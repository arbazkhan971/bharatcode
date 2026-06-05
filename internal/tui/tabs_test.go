package tui

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// TestTabs_SwitchPreservesSessionAndChat is the multi-tab contract test. It
// runs a real agent turn in the first tab, opens a second tab and runs a
// different turn there, then switches between them and asserts:
//
//   - each tab has its own persisted sessionID,
//   - switching swaps the active sessionID and the visible chat content,
//   - the first tab's transcript is preserved across the round trip.
//
// It drives the genuine integration surface (real session repo, real agent
// loop with a scripted provider) and is deterministic: each turn is drained on
// the real run-done condition rather than a timed wait.
func TestTabs_SwitchPreservesSessionAndChat(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		// Tab 1, turn 1.
		{
			llm.DeltaTextEvent{Text: "Reply for the first tab."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
		// Tab 2, turn 1.
		{
			llm.DeltaTextEvent{Text: "Reply for the second tab."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
	}}

	h := newAgentHarness(t, provider)
	m := h.model

	// Default launch holds exactly one tab, so the tab bar is hidden.
	require.Len(t, m.tabs, 1, "a fresh model starts with one tab")
	require.Equal(t, 0, m.activeTab)
	require.Empty(t, m.renderTabBar(m.width), "the tab bar is hidden with a single tab")

	// Tab 1: run a real turn.
	h.submit(t, "first tab prompt")
	h.drain(t, func() bool { return !m.running })
	tab1Session := m.sessionID
	require.True(t, m.sessionPersisted, "the first prompt persists a real session")
	require.NotEmpty(t, tab1Session)
	require.NotEqual(t, "new", tab1Session)
	tab1Render := plainText(m.chat.Render(200))
	require.Contains(t, tab1Render, "first tab prompt")
	require.Contains(t, tab1Render, "Reply for the first tab.")

	// Open a second tab. The active session becomes a fresh, unpersisted one and
	// the chat is empty; the previous tab's content must not leak in.
	_, cmd := m.Update(keyCtrl('t'))
	h.run(t, cmd)
	require.Len(t, m.tabs, 2, "Ctrl+T opens a second tab")
	require.Equal(t, 1, m.activeTab, "the new tab is active")
	require.Equal(t, "new", m.sessionID, "the new tab starts unpersisted")
	require.False(t, m.sessionPersisted)
	require.NotContains(t, plainText(m.chat.Render(200)), "first tab prompt",
		"the new tab's chat must be empty, not the previous tab's transcript")
	require.NotEmpty(t, m.renderTabBar(m.width), "the tab bar shows once a second tab exists")

	// Tab 2: run a different real turn.
	h.submit(t, "second tab prompt")
	h.drain(t, func() bool { return !m.running })
	tab2Session := m.sessionID
	require.NotEmpty(t, tab2Session)
	require.NotEqual(t, tab1Session, tab2Session, "each tab owns a distinct session")
	tab2Render := plainText(m.chat.Render(200))
	require.Contains(t, tab2Render, "second tab prompt")
	require.Contains(t, tab2Render, "Reply for the second tab.")
	require.NotContains(t, tab2Render, "first tab prompt",
		"the second tab must not show the first tab's transcript")

	// The tab bar must reflect the ACTIVE tab's freshly created session
	// immediately, before any switch re-snapshots it. ensureSession mutates the
	// live session fields without saving the tab, so the bar/list must read the
	// live fields for the active tab rather than the stale snapshot.
	bar := plainText(m.renderTabBar(m.width))
	require.Contains(t, bar, shortSessionID(tab2Session),
		"the tab bar must show the active tab's real session, not 'new'")
	m.input.WriteString("/tabs")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Contains(t, m.dialogs.Render(120), shortSessionID(tab2Session),
		"/tabs must list the active tab's real session, not 'new'")
	m.dialogs.Pop()

	// Switch back to tab 1: sessionID and chat content must be restored.
	require.Nil(t, m.switchTab(1), "switching to the already-active tab is a no-op")
	_ = m.switchTab(0)
	require.Equal(t, 0, m.activeTab)
	require.Equal(t, tab1Session, m.sessionID, "switching restores the first tab's session")
	restored := plainText(m.chat.Render(200))
	require.Contains(t, restored, "first tab prompt", "the first tab's transcript is preserved")
	require.Contains(t, restored, "Reply for the first tab.")
	require.NotContains(t, restored, "second tab prompt",
		"the first tab must not contain the second tab's transcript")

	// Switch forward to tab 2: its content is still intact.
	_ = m.switchTab(1)
	require.Equal(t, tab2Session, m.sessionID)
	require.Contains(t, plainText(m.chat.Render(200)), "second tab prompt",
		"the second tab's transcript survives a round trip")
}

// TestTabs_CycleAndClose covers the next/prev cycling, the last-tab close guard,
// and that closing a tab returns focus to a neighbor.
func TestTabs_CycleAndClose(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// Cycling and closing are no-ops with a single tab.
	require.Nil(t, m.nextTab())
	require.Nil(t, m.prevTab())
	require.Nil(t, m.closeTab())
	require.True(t, m.dialogs.Contains("tabs"), "closing the last tab surfaces a note")
	require.Contains(t, m.dialogs.Render(120), "Cannot close the last tab")
	m.dialogs.Pop()

	// Open two more tabs (3 total): assign identifiable sessions so switching is
	// observable even without running a turn.
	_ = m.newTab()
	_ = m.newTab()
	require.Len(t, m.tabs, 3)
	m.tabs[0].sessionID = "sessAAAA1111"
	m.tabs[1].sessionID = "sessBBBB2222"
	m.tabs[2].sessionID = "sessCCCC3333"
	// Reload the active (third) tab so its mirrored sessionID matches the slot.
	m.loadTab(2)
	require.Equal(t, "sessCCCC3333", m.sessionID)

	// nextTab wraps from the last tab back to the first.
	_ = m.nextTab()
	require.Equal(t, 0, m.activeTab)
	require.Equal(t, "sessAAAA1111", m.sessionID)

	// prevTab wraps from the first tab to the last.
	_ = m.prevTab()
	require.Equal(t, 2, m.activeTab)
	require.Equal(t, "sessCCCC3333", m.sessionID)

	// Close the active (third) tab: focus falls back to the new last tab.
	_ = m.closeTab()
	require.Len(t, m.tabs, 2)
	require.Equal(t, 1, m.activeTab)
	require.Equal(t, "sessBBBB2222", m.sessionID)
}

// TestTabs_CtrlTabAliasesSwitch proves the Ctrl+Tab / Ctrl+Shift+Tab aliases
// drive the same next/prev cycling as Ctrl+Right/Left, through the real Update
// key path, and that the /keys help documents the aliases so they are
// discoverable rather than hidden.
func TestTabs_CtrlTabAliasesSwitch(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model

	_ = m.newTab()
	_ = m.newTab()
	require.Len(t, m.tabs, 3)
	m.tabs[0].sessionID = "sessAAAA1111"
	m.tabs[1].sessionID = "sessBBBB2222"
	m.tabs[2].sessionID = "sessCCCC3333"
	m.loadTab(0)
	require.Equal(t, 0, m.activeTab)

	// Ctrl+Tab cycles forward, exactly like Ctrl+Right.
	_, cmd := m.Update(keyCtrlTab(false))
	h.run(t, cmd)
	require.Equal(t, 1, m.activeTab, "Ctrl+Tab advances to the next tab")
	require.Equal(t, "sessBBBB2222", m.sessionID)

	// Ctrl+Shift+Tab cycles backward, exactly like Ctrl+Left, wrapping past the
	// first tab to the last.
	_, cmd = m.Update(keyCtrlTab(true))
	h.run(t, cmd)
	require.Equal(t, 0, m.activeTab, "Ctrl+Shift+Tab steps to the previous tab")
	require.Equal(t, "sessAAAA1111", m.sessionID)

	_, cmd = m.Update(keyCtrlTab(true))
	h.run(t, cmd)
	require.Equal(t, 2, m.activeTab, "Ctrl+Shift+Tab wraps from the first tab to the last")
	require.Equal(t, "sessCCCC3333", m.sessionID)

	// The aliases must be documented in the /keys overlay, the only in-app place
	// the Ctrl-key shortcuts are surfaced.
	help := keybindingHelpBody()
	require.Contains(t, help, "Ctrl+Tab")
	require.Contains(t, help, "Ctrl+Shift+Tab")
}

// keyCtrlTab builds the Ctrl+Tab (or Ctrl+Shift+Tab when shift is set) key press
// the model dispatches on by its String() form.
func keyCtrlTab(shift bool) tea.KeyPressMsg {
	mod := tea.ModCtrl
	if shift {
		mod |= tea.ModShift
	}
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: mod})
}

// TestTabs_SwitchBlockedDuringRun proves a tab switch is refused while an agent
// turn is in flight, so streamed output never lands in the wrong tab.
func TestTabs_SwitchBlockedDuringRun(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model

	_ = m.newTab()
	require.Len(t, m.tabs, 2)
	require.Equal(t, 1, m.activeTab)

	// Simulate an in-flight turn.
	m.running = true
	require.Nil(t, m.switchTab(0), "switching is refused mid-run")
	require.Equal(t, 1, m.activeTab, "the active tab is unchanged while running")
	require.True(t, m.dialogs.Contains("tabs"))
	require.Contains(t, m.dialogs.Render(120), "before switching tabs")
}

// TestTabs_SlashCommand exercises the /tab command family end to end through the
// real input + enter path.
func TestTabs_SlashCommand(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model

	// /tab opens a new tab.
	m.input.WriteString("/tab")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Len(t, m.tabs, 2)
	require.Equal(t, 1, m.activeTab)

	// /tab 1 switches to the first tab by number.
	m.input.WriteString("/tab 1")
	_, cmd := m.Update(keySpecial("enter", tea.KeyEnter))
	h.run(t, cmd)
	require.Equal(t, 0, m.activeTab)

	// /tab next cycles forward.
	m.input.WriteString("/tab next")
	_, cmd = m.Update(keySpecial("enter", tea.KeyEnter))
	h.run(t, cmd)
	require.Equal(t, 1, m.activeTab)

	// /tab close drops back to a single tab.
	m.input.WriteString("/tab close")
	_, cmd = m.Update(keySpecial("enter", tea.KeyEnter))
	h.run(t, cmd)
	require.Len(t, m.tabs, 1)

	// /tabs lists the open tabs.
	m.input.WriteString("/tabs")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.dialogs.Contains("tabs"))
	require.Contains(t, m.dialogs.Render(120), "session")
}

// TestTabs_BarRendersActiveAndOverflow checks that the tab bar marks the active
// tab and never exceeds the terminal width even with the maximum tabs open.
func TestTabs_BarRendersActiveAndOverflow(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model

	for i := 1; i < maxTabs; i++ {
		require.NotNil(t, m.newTabForTest(t))
	}
	require.Len(t, m.tabs, maxTabs)

	for _, width := range []int{80, 100, 120, 200} {
		bar := m.renderTabBar(width)
		require.LessOrEqual(t, lipgloss.Width(bar), width,
			"tab bar must fit within width=%d", width)
	}
}

// TestTabs_ListTitleFromFirstPrompt proves the /tabs listing labels a tab by the
// first line of its opening user prompt — so tabs are distinguishable by what
// they are about — while still surfacing the short session id of a persisted tab.
func TestTabs_ListTitleFromFirstPrompt(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "On it."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	// A multi-line prompt collapses to its first non-blank line in the title.
	h.submit(t, "Refactor the parser\nand add tests")
	h.drain(t, func() bool { return !m.running })
	require.True(t, m.sessionPersisted)
	sessID := m.sessionID

	body := m.tabTitle(0)
	require.Contains(t, body, "Refactor the parser",
		"the tab title must come from the first line of the opening prompt")
	require.NotContains(t, body, "add tests",
		"only the first line of a multi-line prompt is used")
	require.Contains(t, body, shortSessionID(sessID),
		"a persisted tab still shows its short session id alongside the title")

	// A fresh, prompt-less tab falls back to the plain "new session" label.
	_, cmd := m.Update(keyCtrl('t'))
	h.run(t, cmd)
	require.Equal(t, "new session", m.tabTitle(m.activeTab),
		"an unpersisted tab with no prompt yet is labelled 'new session'")

	// The /tabs dialog reflects the derived title for the first tab.
	m.input.WriteString("/tabs")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Contains(t, m.dialogs.Render(120), "Refactor the parser",
		"/tabs lists each tab by its content-derived title")
}

// TestFirstLineSnippet covers the title reduction: first non-blank line, inner
// whitespace collapsed, and rune-safe truncation with an ellipsis.
func TestFirstLineSnippet(t *testing.T) {
	require.Equal(t, "", firstLineSnippet("", 40))
	require.Equal(t, "", firstLineSnippet("   \n\t\n", 40))
	require.Equal(t, "hello world", firstLineSnippet("\n\n  hello   world  \nsecond", 40))
	require.Equal(t, "keep it", firstLineSnippet("keep it", 40))

	// Truncation cuts to maxLen runes, replacing the final rune with an ellipsis.
	got := firstLineSnippet("abcdefghij", 5)
	require.Equal(t, "abcd…", got)
	require.Equal(t, 5, len([]rune(got)), "truncation is measured in runes")

	// Multi-byte runes are never split mid-character.
	require.Equal(t, "héllo…", firstLineSnippet("héllo wörld", 6))
}

// newTabForTest opens a tab and fails the test if the limit blocks it. It keeps
// the overflow test readable.
func (m *model) newTabForTest(t *testing.T) tea.Cmd {
	t.Helper()
	before := len(m.tabs)
	cmd := m.newTab()
	require.Greater(t, len(m.tabs), before, "newTab must add a tab")
	return cmd
}

// keyCtrl builds a Ctrl+<rune> key press for the tab key bindings.
func keyCtrl(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl})
}
