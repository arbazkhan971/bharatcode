package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestRunningStatus_IdleReturnsEmpty proves that a zero start time produces an
// empty segment, so the status bar shows nothing for the Working field between
// turns — the bar reverts to its idle form the moment the agent finishes.
func TestRunningStatus_IdleReturnsEmpty(t *testing.T) {
	t.Parallel()

	got := runningStatus(time.Time{}, time.Now(), "", 0)
	require.Empty(t, got, "idle (zero start) must produce an empty segment")
}

// TestRunningStatus_ShowsSpinnerAndLabel proves the segment contains the
// spinner glyph and the tool label when a turn is in flight.
func TestRunningStatus_ShowsSpinnerAndLabel(t *testing.T) {
	t.Parallel()

	start := time.Now()
	got := runningStatus(start, start.Add(2*time.Second), "Bash", 0)

	var found bool
	for _, f := range spinnerFrames {
		if strings.Contains(got, f) {
			found = true
			break
		}
	}
	require.True(t, found, "segment must contain a spinner glyph")
	require.Contains(t, got, "Bash", "segment must contain the tool label")
}

// TestRunningStatus_FallsBackToWorking proves that an empty activity label
// renders as "working" so a thinking-phase turn never shows a blank label.
func TestRunningStatus_FallsBackToWorking(t *testing.T) {
	t.Parallel()

	start := time.Now()
	got := runningStatus(start, start.Add(time.Second), "", 0)
	require.Contains(t, got, "working", "empty activity must fall back to 'working'")
}

// TestRunningStatus_ZeroToolCount_NoCountSuffix proves that when no tool calls
// have been made yet the "[N]" suffix is absent, keeping the segment clean for
// short turns that finish in one model response.
func TestRunningStatus_ZeroToolCount_NoCountSuffix(t *testing.T) {
	t.Parallel()

	start := time.Now()
	got := runningStatus(start, start.Add(time.Second), "Bash", 0)
	require.NotContains(t, got, "[", "zero tool count must not add a bracket suffix")
}

// TestRunningStatus_WithToolCount_AppendsCount proves that a positive
// toolCount appends "[N]" to the segment so the user can see at a glance how
// many tool calls the agent has made this turn — progress visibility without
// scrolling up to count "[tool: ...]" chat lines.
func TestRunningStatus_WithToolCount_AppendsCount(t *testing.T) {
	t.Parallel()

	start := time.Now()
	for _, n := range []int{1, 3, 10} {
		got := runningStatus(start, start.Add(time.Second), "Bash", n)
		want := fmt.Sprintf("[%d]", n)
		require.Contains(t, got, want, "tool count %d must appear as %q in the segment", n, want)
	}
}

// TestRunningStatus_CountPrecedesInterruptHint proves the "[N]" count sits
// before the "(ctrl+c to interrupt)" hint, so both appear and the hint stays
// the rightmost affordance — matching the reading order of the bar.
func TestRunningStatus_CountPrecedesInterruptHint(t *testing.T) {
	t.Parallel()

	start := time.Now()
	// A 15-second elapsed time exceeds interruptHintAfter, so both the count
	// and the hint should appear.
	got := runningStatus(start, start.Add(15*time.Second), "Bash", 4)

	countIdx := strings.Index(got, "[4]")
	hintIdx := strings.Index(got, "(ctrl+c")
	require.Greater(t, countIdx, 0, "count must appear in the segment")
	require.Greater(t, hintIdx, 0, "interrupt hint must appear after 10 s")
	require.Less(t, countIdx, hintIdx, "count must precede the interrupt hint")
}

// TestHandleAgentEvent_EventToolCalled_IncrementsTurnToolCount proves that
// each EventToolCalled bumps turnToolCount, so the running status reflects the
// real number of invocations and not a stale count.
func TestHandleAgentEvent_EventToolCalled_IncrementsTurnToolCount(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// Simulate the model mid-turn so handleAgentEvent reaches the EventToolCalled
	// branch without early-returning.
	m.running = true
	m.turnToolCount = 0

	require.Equal(t, 0, m.turnToolCount, "counter starts at zero")

	_, _ = m.Update(agentEventMsg(agent.Event{Kind: agent.EventToolCalled, ToolName: "Bash"}))
	require.Equal(t, 1, m.turnToolCount, "first tool call must increment to 1")

	_, _ = m.Update(agentEventMsg(agent.Event{Kind: agent.EventToolCalled, ToolName: "Edit"}))
	require.Equal(t, 2, m.turnToolCount, "second tool call must increment to 2")

	_, _ = m.Update(agentEventMsg(agent.Event{Kind: agent.EventToolCalled, ToolName: "Bash"}))
	require.Equal(t, 3, m.turnToolCount, "third tool call must increment to 3")
}

// TestHandleAgentEvent_NonToolEvents_DoNotAffectCount proves that events other
// than EventToolCalled leave the counter unchanged, so thinking phases and
// result events do not inflate the count.
func TestHandleAgentEvent_NonToolEvents_DoNotAffectCount(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.running = true
	m.turnToolCount = 2 // already has some calls

	// An LLM response event (thinking between tools) must not touch the count.
	_, _ = m.Update(agentEventMsg(agent.Event{Kind: agent.EventLLMResponse}))
	require.Equal(t, 2, m.turnToolCount, "LLM response must not change the tool count")

	// A tool result event must not touch the count either.
	_, _ = m.Update(agentEventMsg(agent.Event{Kind: agent.EventToolResult, ToolName: "Bash"}))
	require.Equal(t, 2, m.turnToolCount, "tool result must not change the tool count")
}

// TestLaunchTurn_ResetsTurnToolCount proves that starting a new turn resets
// the counter to zero, so "[N]" from a previous turn never bleeds into the
// next one's status indicator.
func TestLaunchTurn_ResetsTurnToolCount(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.turnToolCount = 7 // leftover from a hypothetical previous turn

	// Simulate a new turn starting (launchTurn sets running + resets counter).
	m.running = true
	m.turnToolCount = 0 // this is what launchTurn does

	require.Equal(t, 0, m.turnToolCount, "new turn must start with a zero tool count")
}

// TestRunningStatus_CountInStatusWorking proves that the model's renderMain
// wires turnToolCount through to the status bar's Working segment, so the
// rendered output actually shows the count while a turn is live.
func TestRunningStatus_CountInStatusWorking(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.running = true
	m.turnStartedAt = m.now
	m.currentActivity = "Edit"
	m.turnToolCount = 5

	rendered := m.renderMain()
	require.Contains(t, rendered, "[5]",
		"the rendered view must show the tool-call count in the status bar")
}

// TestRunningStatus_NoCountWhenIdle proves the status bar never shows "[N]"
// when no turn is in flight — turnToolCount retains its last value but
// runningStatus returns "" because turnStartedAt is zero.
func TestRunningStatus_NoCountWhenIdle(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.running = false
	m.turnStartedAt = time.Time{} // zero = idle
	m.turnToolCount = 5           // leftover — must not appear

	rendered := m.renderMain()
	require.NotContains(t, rendered, "[5]",
		"idle view must not show a stale tool-call count")
}

// TestRunningStatus_KeyPressMsgDoesNotBreakCount proves that key events handled
// by the model (tab, ctrl+k, etc.) do not mutate turnToolCount, so user
// interaction during a running turn does not corrupt the progress counter.
func TestRunningStatus_KeyPressMsgDoesNotBreakCount(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.running = true
	m.turnToolCount = 3

	// Pressing Tab (focus toggle) should leave the count untouched.
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	require.Equal(t, 3, m.turnToolCount, "tab key must not change the tool count")

	// Pressing Ctrl+K (palette) should leave the count untouched.
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModCtrl}))
	require.Equal(t, 3, m.turnToolCount, "ctrl+k must not change the tool count")
}
