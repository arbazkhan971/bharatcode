package tui

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// typeString feeds each rune of s to the model as a text key press, exercising
// the same default key path the real terminal uses.
func typeString(t *testing.T, m *model, s string) {
	t.Helper()
	for _, r := range s {
		_, _ = m.Update(keyText(string(r)))
	}
}

func keyUp() tea.KeyPressMsg   { return keySpecial("up", tea.KeyUp) }
func keyDown() tea.KeyPressMsg { return keySpecial("down", tea.KeyDown) }
func keyTab() tea.KeyPressMsg  { return keySpecial("tab", tea.KeyTab) }

// oneTurnScript returns a provider that completes a single agent turn for each
// submitted prompt, so history tests can submit real prompts and drain the run
// to quiescence without the agent panicking on missing infrastructure.
func oneTurnScript(n int) *scriptedProvider {
	scripts := make([][]llm.Event, 0, n)
	for i := 0; i < n; i++ {
		scripts = append(scripts, []llm.Event{
			llm.DeltaTextEvent{Text: "ok"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		})
	}
	return &scriptedProvider{scripts: scripts}
}

// submitPrompt feeds a plain prompt through the real input+enter path and drains
// the resulting agent turn so the model returns to a non-running state.
func submitPrompt(t *testing.T, h *agentHarness, text string) {
	t.Helper()
	typeString(t, h.model, text)
	_, cmd := h.model.Update(keySpecial("enter", tea.KeyEnter))
	// Use startBatch so the run goroutine's runDoneMsg is routed through
	// h.msgCh instead of being lost on a timed-out execWithTimeout call.
	h.startBatch(t, cmd)
	h.drain(t, func() bool { return !h.model.running })
}

// TestInputHistory_UpDownRecall is the headline history contract: after two
// submitted prompts, Up twice recalls the first, and Down recalls the second.
func TestInputHistory_UpDownRecall(t *testing.T) {
	h := newAgentHarness(t, oneTurnScript(2))
	m := h.model

	submitPrompt(t, h, "first prompt")
	submitPrompt(t, h, "second prompt")
	require.Empty(t, m.input.String(), "buffer must be empty after submitting")

	// Up walks back from newest to oldest.
	_, _ = m.Update(keyUp())
	require.Equal(t, "second prompt", m.input.String(), "first Up recalls the newest entry")
	_, _ = m.Update(keyUp())
	require.Equal(t, "first prompt", m.input.String(), "second Up recalls the oldest entry")

	// Up at the oldest entry is a no-op.
	_, _ = m.Update(keyUp())
	require.Equal(t, "first prompt", m.input.String(), "Up at the oldest entry must not change the buffer")

	// Down walks forward toward the live buffer.
	_, _ = m.Update(keyDown())
	require.Equal(t, "second prompt", m.input.String(), "Down recalls the newer entry")
	_, _ = m.Update(keyDown())
	require.Empty(t, m.input.String(), "Down past the newest entry restores the empty live buffer")
}

// TestInputHistory_DraftPreservedAcrossRecall asserts that an in-progress,
// unsubmitted buffer is restored when the user walks Up into history and back
// Down to the live line.
func TestInputHistory_DraftPreservedAcrossRecall(t *testing.T) {
	h := newAgentHarness(t, oneTurnScript(1))
	m := h.model

	submitPrompt(t, h, "older")

	typeString(t, m, "draft text")
	require.Equal(t, "draft text", m.input.String())

	_, _ = m.Update(keyUp())
	require.Equal(t, "older", m.input.String(), "Up recalls history over the live draft")

	_, _ = m.Update(keyDown())
	require.Equal(t, "draft text", m.input.String(), "Down restores the preserved live draft")
}

// TestInputHistory_EditResetsRecallCursor asserts that editing the buffer ends
// the recall walk so the next Up starts again from the most recent entry.
func TestInputHistory_EditResetsRecallCursor(t *testing.T) {
	h := newAgentHarness(t, oneTurnScript(2))
	m := h.model

	submitPrompt(t, h, "alpha")
	submitPrompt(t, h, "beta")

	_, _ = m.Update(keyUp())
	_, _ = m.Update(keyUp())
	require.Equal(t, "alpha", m.input.String())

	// Edit the recalled buffer; this must reset the recall cursor.
	typeString(t, m, "X")
	require.Equal(t, "alphaX", m.input.String())

	// The next Up starts from the newest entry again, not from before "alpha".
	_, _ = m.Update(keyUp())
	require.Equal(t, "beta", m.input.String(), "editing must reset recall to the newest entry")
}

// TestInputHistory_RecordsSlashCommands asserts submitted slash commands are
// recalled by Up just like plain prompts. /help does not start an agent run, so
// the lightweight model suffices here.
func TestInputHistory_RecordsSlashCommands(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/help")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Empty(t, m.input.String())

	_, _ = m.Update(keyUp())
	require.Equal(t, "/help", m.input.String(), "submitted slash commands must be recallable")
}

// TestInputHistory_NoHistory_UpIsNoop asserts Up/Down do nothing with no
// history and an empty buffer.
func TestInputHistory_NoHistory_UpIsNoop(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(keyUp())
	require.Empty(t, m.input.String())
	_, _ = m.Update(keyDown())
	require.Empty(t, m.input.String())
}

// TestSlashCompletion_TabCompletesUniquePrefix is the headline completion
// contract: "/se" + Tab completes to "/sessions". Completion never submits, so
// the lightweight model is sufficient.
func TestSlashCompletion_TabCompletesUniquePrefix(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/se")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/sessions", m.input.String(), "Tab completes the only /se* match")
	// Focus must remain on the input; Tab did not toggle to chat.
	require.Equal(t, focusInput, m.focus, "slash completion must not toggle focus")
}

// TestSlashCompletion_TabCyclesMultipleMatches asserts Tab cycles through every
// match for an ambiguous prefix in canonical order and wraps around.
func TestSlashCompletion_TabCyclesMultipleMatches(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	// "/s" matches /sessions, /status, /save, /search in slashCommands order.
	typeString(t, m, "/s")

	_, _ = m.Update(keyTab())
	require.Equal(t, "/sessions", m.input.String())
	_, _ = m.Update(keyTab())
	require.Equal(t, "/status", m.input.String())
	_, _ = m.Update(keyTab())
	require.Equal(t, "/save", m.input.String())
	_, _ = m.Update(keyTab())
	require.Equal(t, "/search", m.input.String())
	// Cycle wraps back to the first match.
	_, _ = m.Update(keyTab())
	require.Equal(t, "/sessions", m.input.String(), "the cycle must wrap to the first match")
}

// TestSlashCompletion_OffersAllHandledCommands guards against the completion
// list drifting out of sync with the commands handleSlash actually accepts:
// these were handled and listed in /help but were not Tab-completable, so the
// user could not discover or complete them at the prompt.
func TestSlashCompletion_OffersAllHandledCommands(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"/search", "/tab", "/tabs", "/theme"} {
		require.Contains(t, slashCommands, cmd,
			"%s is handled by handleSlash and must be Tab-completable", cmd)
	}
}

// TestSlashCommandsAllHaveDescriptions asserts every completable command carries
// an inline gloss, so the slash-hint menu can always explain the command the
// user has settled on instead of showing a bare name.
func TestSlashCommandsAllHaveDescriptions(t *testing.T) {
	t.Parallel()

	for _, cmd := range slashCommands {
		require.NotEmptyf(t, slashCommandDescriptions[cmd],
			"completable command %s must have a slashCommandDescriptions entry", cmd)
	}
}

// TestSlashCompletion_EditMidCycleReseeds asserts that editing the buffer after
// a completion ends the cycle and the next Tab completes the new prefix.
func TestSlashCompletion_EditMidCycleReseeds(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/se")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/sessions", m.input.String())

	// Clear and type a different prefix; the next Tab must complete it.
	for range "/sessions" {
		_, _ = m.Update(keySpecial("backspace", tea.KeyBackspace))
	}
	require.Empty(t, m.input.String())
	typeString(t, m, "/he")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/help", m.input.String(), "Tab must complete the freshly typed prefix")
}

// TestTab_NonSlashTogglesFocus asserts the original focus-toggle behavior of
// Tab is preserved when the buffer is not a slash prefix.
func TestTab_NonSlashTogglesFocus(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, focusInput, m.focus)
	_, _ = m.Update(keyTab())
	require.Equal(t, focusChat, m.focus, "Tab with a non-slash buffer toggles focus to chat")
	_, _ = m.Update(keyTab())
	require.Equal(t, focusInput, m.focus, "Tab toggles focus back to input")
}

// TestSlashCompletion_NoMatchLeavesBuffer asserts a slash prefix with no match
// is left unchanged and does not toggle focus.
func TestSlashCompletion_NoMatchLeavesBuffer(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/zzz")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/zzz", m.input.String(), "an unmatched slash prefix is left unchanged")
	require.Equal(t, focusInput, m.focus, "an unmatched slash prefix must not toggle focus")
}
