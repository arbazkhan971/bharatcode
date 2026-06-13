package tui

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// agentEvent builds an agentEventMsg tagged with the given session id so a test
// can feed a background tab's stream through the real Update/demux path exactly
// as listenAgent would after a UIEvent arrives on m.eventCh.
func agentEvent(sessionID string, kind agent.EventKind) agentEventMsg {
	return agentEventMsg(agent.Event{SessionID: sessionID, Kind: kind})
}

// deltaEvent builds an EventLLMDelta agentEventMsg carrying provisional text for
// the given session.
func deltaEvent(sessionID, text string) agentEventMsg {
	ev := agent.Event{SessionID: sessionID, Kind: agent.EventLLMDelta, Delta: text}
	return agentEventMsg(ev)
}

// finishEvent builds an EventTurnFinished agentEventMsg carrying the canonical
// final assistant text for the given session.
func finishEvent(sessionID, text string) agentEventMsg {
	ev := agent.Event{
		SessionID: sessionID,
		Kind:      agent.EventTurnFinished,
		Message:   &message.Message{Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: text}}},
	}
	return agentEventMsg(ev)
}

// TestConcurrent_BackgroundStreamRoutesToOwningTab is the core user-visible
// concurrency contract: a run started in tab A keeps streaming into tab A's chat
// after the user switches to tab B, and B's active state (m.chat, m.running) is
// untouched. It feeds agent events tagged with A's sessionID while B is active
// and asserts the demux routes them to A's tab, not the active chat.
func TestConcurrent_BackgroundStreamRoutesToOwningTab(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		// Tab A, turn 1 (establishes a real persisted session for A).
		{
			llm.DeltaTextEvent{Text: "Tab A first reply."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
		// Tab B, turn 1 (a real turn run while A is "in flight").
		{
			llm.DeltaTextEvent{Text: "Tab B real reply."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 3}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	// Tab A: run a real turn so A owns a persisted session.
	h.submit(t, "tab A prompt")
	h.drain(t, func() bool { return !m.running })
	tabASession := m.sessionID
	require.True(t, m.sessionPersisted)
	require.NotEqual(t, "new", tabASession)
	require.Contains(t, plainText(m.chat.Render(200)), "Tab A first reply.")

	// Simulate a fresh in-flight turn in tab A (as launchTurn would): a new turn
	// number and running set. This is the state a real background run holds.
	m.turn++
	tabATurn := m.turn
	m.running = true
	m.turnStartedAt = m.now

	// Open a second tab and switch to it — now ALLOWED mid-run. A's run-state is
	// frozen into the A slot; B becomes active and idle.
	_ = m.newTab()
	require.Equal(t, 1, m.activeTab)
	require.False(t, m.running, "the freshly active tab B is idle")
	require.True(t, m.tabs[0].running, "tab A's run keeps running in the background")
	require.Equal(t, tabATurn, m.tabs[0].turn, "tab A's in-flight turn number is preserved")

	bChat := m.chat // capture B's distinct chat list pointer
	require.NotSame(t, m.tabs[0].chat, bChat, "each tab owns a distinct chat list")

	// Feed A's stream while B is active: deltas + canonical finish, tagged with
	// A's sessionID. They must land in A's chat, never B's active chat.
	_, _ = m.Update(deltaEvent(tabASession, "Background "))
	_, _ = m.Update(deltaEvent(tabASession, "stream text."))
	_, _ = m.Update(finishEvent(tabASession, "Background stream text."))

	bRender := plainText(m.chat.Render(200))
	require.NotContains(t, bRender, "Background stream text.",
		"a background stream must NOT leak into the active tab B's chat")
	require.NotContains(t, bRender, "tab A prompt",
		"the active tab B must not show tab A's transcript at all")

	aRender := plainText(m.tabs[0].chat.Render(200))
	require.Contains(t, aRender, "Background stream text.",
		"the background stream must land in tab A's own chat while B is active")
	require.Contains(t, aRender, "Tab A first reply.",
		"tab A's earlier transcript is preserved alongside the new background stream")

	// A background EventTurnFinished does not clear the active tab's running flag
	// (m.running tracks B); that only happens on a runDoneMsg, asserted below.
	require.False(t, m.running, "a background turn event must not flip the active tab's running flag")

	// (2) The user can submit in tab B while A is mid-run: run a real turn in B.
	h.submit(t, "tab B prompt")
	h.drain(t, func() bool { return !m.running })
	tabBSession := m.sessionID
	require.NotEqual(t, tabASession, tabBSession, "tab B owns a distinct session")
	bAfter := plainText(m.chat.Render(200))
	require.Contains(t, bAfter, "tab B prompt")
	require.Contains(t, bAfter, "Tab B real reply.", "tab B's own turn streamed independently")
	require.NotContains(t, bAfter, "Background stream text.",
		"tab B's chat never picked up tab A's background stream")

	// Tab A's background run is still marked running and its transcript intact.
	require.True(t, m.tabs[0].running, "tab A is still mid-run after B completes a turn")
	require.Contains(t, plainText(m.tabs[0].chat.Render(200)), "Background stream text.")

	// (4) A runDoneMsg for the BACKGROUND tab A must NOT clear the active tab B's
	// running indicator, and must clear A's own running flag.
	m.running = true // B is now notionally running for this assertion
	_, _ = m.Update(runDoneMsg{sessionID: tabASession, last: &message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "Background stream text."}},
		Usage:   &message.TokenUsage{InputTokens: 7, OutputTokens: 4},
	}})
	require.True(t, m.running, "a background runDoneMsg must NOT clear the active tab's running flag")
	require.False(t, m.tabs[0].running, "the background tab's own running flag is cleared on its run-done")

	// Switching back to tab A rehydrates its (now finished) state and its full
	// transcript, with the background stream and its earlier turn both intact.
	m.running = false
	_ = m.switchTab(0)
	require.Equal(t, tabASession, m.sessionID)
	require.False(t, m.running, "tab A's run finished, so it is idle on return")
	restored := plainText(m.chat.Render(200))
	require.Contains(t, restored, "Tab A first reply.")
	require.Contains(t, restored, "Background stream text.")
	require.NotContains(t, restored, "tab B prompt", "tab A never shows tab B's transcript")
}

// TestConcurrent_StragglerForClosedSessionIsDropped proves an agent event whose
// session has no open tab (it was closed) is dropped rather than misrouted into
// the active chat, and does not panic.
func TestConcurrent_StragglerForClosedSessionIsDropped(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model

	// A second tab so there is an active tab with a known chat.
	_ = m.newTab()
	require.Equal(t, 1, m.activeTab)
	before := plainText(m.chat.Render(200))

	// An event for a session no tab owns must be dropped.
	_, cmd := m.Update(deltaEvent("ghost-session-xyz", "orphaned text"))
	require.NotNil(t, cmd, "the listen command is re-issued even when the event is dropped")
	_, _ = m.Update(finishEvent("ghost-session-xyz", "orphaned text"))

	after := plainText(m.chat.Render(200))
	require.Equal(t, before, after, "a straggler for a closed session must not alter the active chat")
	require.NotContains(t, after, "orphaned text")
}

// TestConcurrent_RunDoneForUnknownSessionIsNoop proves a runDoneMsg for a closed
// session is a harmless no-op (no panic, active state untouched).
func TestConcurrent_RunDoneForUnknownSessionIsNoop(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model
	m.running = true
	_, _ = m.Update(runDoneMsg{sessionID: "ghost-session-xyz"})
	require.True(t, m.running, "a run-done for an unknown session must not touch active run-state")
}

// TestConcurrent_TabBarMarksRunningTabs proves the tab bar surfaces a running
// marker for a background tab, so concurrent work is visible from any tab.
func TestConcurrent_TabBarMarksRunningTabs(t *testing.T) {
	h := newAgentHarness(t, &scriptedProvider{})
	m := h.model
	_ = m.newTab()
	require.Len(t, m.tabs, 2)
	require.Equal(t, 1, m.activeTab)

	// Mark the background tab A as running.
	m.tabs[0].running = true
	require.True(t, m.tabRunning(0), "tab 0 reads as running from its snapshot")
	require.True(t, m.anyRunning(), "anyRunning sees the background tab")

	// The marker renders into tab 0's label (the running glyph).
	require.Contains(t, m.tabLabel(0), "●", "a running background tab shows the running marker")
	require.NotContains(t, m.tabLabel(1), "●", "the idle active tab shows no marker")

	// The bar still fits within width and includes the marker.
	require.Contains(t, plainText(m.renderTabBar(120)), "●")
}
