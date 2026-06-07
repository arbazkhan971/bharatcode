package tui

import (
	"strings"
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
// recalled by Up just like plain prompts. /clear is used here because it does
// not push a dialog (which would intercept the Up key before history recall).
func TestInputHistory_RecordsSlashCommands(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/clear")
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Empty(t, m.input.String())

	_, _ = m.Update(keyUp())
	require.Equal(t, "/clear", m.input.String(), "submitted slash commands must be recallable")
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

// TestMatchSlash_PrefixWins asserts that when a command begins with the typed
// prefix the fuzzy fallback never fires: the result is exactly the prefix
// matches in canonical order, so existing prefix completion is unchanged.
func TestMatchSlash_PrefixWins(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"/sessions", "/status", "/save", "/search"}, matchSlash(slashCommands, "/s"))
	require.Equal(t, []string{"/help"}, matchSlash(slashCommands, "/help"),
		"a fully typed command still returns itself, not a fuzzy expansion")
}

// TestMatchSlash_FuzzyFallback asserts that when no command begins with the
// prefix, a case-insensitive subsequence match on the command name still finds
// it — so a mistyped or mid-word query like "/port" reaches "/export".
func TestMatchSlash_FuzzyFallback(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"/export"}, matchSlash(slashCommands, "/port"),
		"a subsequence of the name resolves when no prefix matches")
	require.Equal(t, []string{"/compact"}, matchSlash(slashCommands, "/PACT"),
		"the fuzzy fallback is case-insensitive")
	require.Empty(t, matchSlash(slashCommands, "/zzz"),
		"a token that is not even a subsequence still matches nothing")
}

// TestMatchSlash_FuzzyRanksByRelevance asserts the fuzzy fallback orders its
// matches by relevance rather than canonical order: a command that contains the
// token as a contiguous substring sorts ahead of ones that only match it as a
// scattered subsequence, and within the subsequence band the tighter match span
// wins. For "/et", "/budget" contains "et" outright, so it leads the subsequence
// matches; within that band "/agent" (e..t span 3) precedes "/exit" (4)
// precedes "/revert" (5) precedes "/export" (6).
func TestMatchSlash_FuzzyRanksByRelevance(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"/budget", "/agent", "/exit", "/revert", "/export"}, matchSlash(slashCommands, "/et"),
		"substring match leads, then subsequence matches by tightest span")
}

// TestMatchSlash_FuzzyRankedTabCompletesBest asserts the relevance ranking flows
// through Tab completion, so the first Tab lands on the best fuzzy match.
func TestMatchSlash_FuzzyRankedTabCompletesBest(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/et")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/budget", m.input.String(),
		"Tab completes the highest-ranked fuzzy match first")
}

// TestMatchSlash_FuzzyCompletesViaTab asserts the fuzzy fallback flows through
// the end-to-end Tab completion path, not just the matcher in isolation.
func TestMatchSlash_FuzzyCompletesViaTab(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "/port")
	_, _ = m.Update(keyTab())
	require.Equal(t, "/export", m.input.String(),
		"Tab completes a fuzzy match when no command shares the prefix")
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

// TestSuggestSlash_OffersClosestCommand asserts a mistyped command name is
// pointed at its nearest built-in command within the edit-distance threshold,
// covering the common typo shapes (transposition, dropped/doubled letter, wrong
// key), so the unknown-command dialog can show a "did you mean" hint.
func TestSuggestSlash_OffersClosestCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"exprot": "/export", // transposed letters
		"statu":  "/status", // dropped trailing letter
		"helpp":  "/help",   // doubled letter
		"modle":  "/model",  // transposed letters
		"clera":  "/clear",  // transposed letters
		"Quit":   "/quit",   // case-insensitive
	}
	for input, want := range cases {
		require.Equal(t, want, suggestSlash(slashCommands, input),
			"%q should be corrected to %q", input, want)
	}
}

// TestSuggestSlash_NoSuggestionForDistantOrEmpty asserts a name that is too far
// from every command — or too short to be a typo of one — yields no suggestion,
// so the dialog does not "correct" a genuinely novel command to an unrelated one.
func TestSuggestSlash_NoSuggestionForDistantOrEmpty(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "deploy", "xyzzy", "a", "go"} {
		require.Empty(t, suggestSlash(slashCommands, input),
			"%q should not be corrected to any built-in command", input)
	}
}

// TestSuggestSlash_ResultIsKnownCommand guards the invariant that any suggestion
// is itself a completable built-in command, so the hint never points at a name
// the user cannot actually run.
func TestSuggestSlash_ResultIsKnownCommand(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"exprot", "statu", "sessons", "compatc"} {
		if s := suggestSlash(slashCommands, input); s != "" {
			require.Contains(t, slashCommands, s,
				"suggestion %q for %q must be a real built-in command", s, input)
		}
	}
}

// TestLevenshtein_KnownDistances pins the edit-distance metric the suggestion
// ranking depends on, so a refactor of the inner loop cannot silently change
// which command a typo resolves to.
func TestLevenshtein_KnownDistances(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"export", "exprot", 2},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
	}
	for _, c := range cases {
		require.Equal(t, c.want, levenshtein(c.a, c.b), "levenshtein(%q,%q)", c.a, c.b)
		require.Equal(t, c.want, levenshtein(c.b, c.a), "levenshtein must be symmetric")
	}
}

// TestHandleSlash_UnknownCommandSuggestsClosest asserts the unknown-command
// dialog echoes the typed command and points a likely typo at its nearest
// built-in via a "Did you mean" hint, so the user can recover without /help.
func TestHandleSlash_UnknownCommandSuggestsClosest(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.handleSlash("/exprot")

	require.True(t, m.dialogs.Contains("error"), "an unknown command must surface the error dialog")
	body := plainText(m.dialogs.Render(200))
	require.Contains(t, body, "/exprot", "the dialog must echo the typed command")
	require.Contains(t, body, "Did you mean /export?", "a close typo must suggest the nearest command")
}

// TestHandleSlash_UnknownCommandNoSuggestionWhenDistant asserts a genuinely
// novel command name surfaces the error dialog without a misleading suggestion.
func TestHandleSlash_UnknownCommandNoSuggestionWhenDistant(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.handleSlash("/deploy")

	require.True(t, m.dialogs.Contains("error"))
	require.NotContains(t, plainText(m.dialogs.Render(200)), "Did you mean",
		"a distant command name must not be corrected to an unrelated one")
}

// TestSetDynamicCommands_FiltersBuiltinsAndDuplicates asserts the dynamic
// command list drops blanks, names without a leading slash, names that collide
// with a built-in (the built-in handler wins at runtime), and duplicates, while
// preserving the surviving order so the caller controls how recipes and prompts
// sort after the built-ins.
func TestSetDynamicCommands_FiltersBuiltinsAndDuplicates(t *testing.T) {
	t.Parallel()

	var st inputState
	st.setDynamicCommands([]string{
		"/triage", "  ", "noslash", "/help", "/triage", "  /review  ", "/deploy",
	})
	require.Equal(t, []string{"/triage", "/review", "/deploy"}, st.dynamicCommands,
		"blanks, bare names, built-in collisions, and duplicates are dropped; order kept")
}

// TestCandidates_AppendsDynamicAfterBuiltins asserts candidates returns the
// built-ins unchanged when no dynamic commands are set, and otherwise the
// built-ins followed by the dynamic commands.
func TestCandidates_AppendsDynamicAfterBuiltins(t *testing.T) {
	t.Parallel()

	var st inputState
	require.Equal(t, slashCommands, st.candidates(),
		"with no dynamic commands the shared built-in slice is returned unchanged")

	st.setDynamicCommands([]string{"/triage"})
	got := st.candidates()
	require.Equal(t, len(slashCommands)+1, len(got))
	require.Equal(t, "/triage", got[len(got)-1], "dynamic commands sort after built-ins")
	require.Equal(t, slashCommands[0], got[0], "built-ins keep their leading position")
}

// TestMatchSlash_CompletesDynamicCommand asserts a dynamic recipe/prompt name
// completes like a built-in: a prefix that only a dynamic command shares resolves
// to it, both as an exact prefix and via the fuzzy subsequence fallback.
func TestMatchSlash_CompletesDynamicCommand(t *testing.T) {
	t.Parallel()

	var st inputState
	st.setDynamicCommands([]string{"/triage", "/deploy"})
	cmds := st.candidates()

	require.Equal(t, []string{"/triage"}, matchSlash(cmds, "/tri"),
		"a prefix unique to a dynamic command completes to it")
	require.Equal(t, []string{"/deploy"}, matchSlash(cmds, "/dpl"),
		"the fuzzy subsequence fallback reaches a dynamic command too")
}

// TestCompleteSlash_CyclesDynamicCommand asserts Tab completion on an inputState
// carrying dynamic commands lands on the dynamic match, proving the wiring from
// completeSlash through candidates reaches recipes and custom prompts.
func TestCompleteSlash_CyclesDynamicCommand(t *testing.T) {
	t.Parallel()

	var st inputState
	st.setDynamicCommands([]string{"/triage"})
	got, ok := st.completeSlash("/tri")
	require.True(t, ok)
	require.Equal(t, "/triage", got)
}

// TestCompleteSlashPrev_SeedsOnLastMatch asserts the first Shift+Tab on a
// slash prefix lands on the final candidate rather than the first, so a user can
// reach the end of the menu in one step the way a backward cycle should.
func TestCompleteSlashPrev_SeedsOnLastMatch(t *testing.T) {
	t.Parallel()

	var st inputState
	matches := matchSlash(st.candidates(), "/s")
	require.Greater(t, len(matches), 1, "the test needs an ambiguous prefix")

	got, ok := st.completeSlashPrev("/s")
	require.True(t, ok, "a backward step on a matching prefix seeds the cycle")
	require.Equal(t, matches[len(matches)-1], got,
		"the first Shift+Tab lands on the last match")
}

// TestCompleteSlashPrev_StepsBackwardAndWraps asserts Shift+Tab reverses an
// active Tab cycle and that stepping back past the first match wraps to the last,
// the mirror image of the forward cycle.
func TestCompleteSlashPrev_StepsBackwardAndWraps(t *testing.T) {
	t.Parallel()

	var st inputState
	matches := matchSlash(st.candidates(), "/s")
	require.Greater(t, len(matches), 1, "the test needs an ambiguous prefix")

	// Two forward Tabs settle on the second match.
	c1, ok := st.completeSlash("/s")
	require.True(t, ok)
	c2, ok := st.completeSlash(c1)
	require.True(t, ok)
	require.Equal(t, matches[1], c2)

	// One backward step returns to the first match...
	back, ok := st.completeSlashPrev(c2)
	require.True(t, ok)
	require.Equal(t, matches[0], back)

	// ...and a further backward step wraps to the last.
	wrapped, ok := st.completeSlashPrev(back)
	require.True(t, ok)
	require.Equal(t, matches[len(matches)-1], wrapped)
}

// TestSlashHintCommands_SurfacesDynamicCommand asserts the hint dropdown lists a
// dynamic command for an ambiguous prefix it shares with a built-in, so recipes
// and custom prompts are as visible while typing as the built-ins.
func TestSlashHintCommands_SurfacesDynamicCommand(t *testing.T) {
	t.Parallel()

	var st inputState
	st.setDynamicCommands([]string{"/help-me-debug"})
	cmds, active := slashHintCommands("/help", &st)
	require.Contains(t, cmds, "/help-me-debug",
		"a dynamic command sharing the prefix shows in the hint menu")
	require.Equal(t, -1, active)
}

// TestSuggestSlash_CorrectsToDynamicCommand asserts the did-you-mean suggester
// points a likely typo at a dynamic command, not only a built-in.
func TestSuggestSlash_CorrectsToDynamicCommand(t *testing.T) {
	t.Parallel()

	var st inputState
	st.setDynamicCommands([]string{"/triage"})
	require.Equal(t, "/triage", suggestSlash(st.candidates(), "trige"),
		"a one-edit typo of a recipe name is corrected to the recipe")
}

// --- Undo/redo (Ctrl+Z / Ctrl+Y) tests ---

// keyCtrlZ and keyCtrlY produce the key-press messages for the undo/redo bindings.
func keyCtrlZ() tea.KeyPressMsg { return keyCtrl('z') }
func keyCtrlY() tea.KeyPressMsg { return keyCtrl('y') }

// TestInputUndo_UndoesTypingCharByChar verifies that each Ctrl+Z walks the input
// buffer back by one character, undoing the most recent keystroke first.
func TestInputUndo_UndoesTypingCharByChar(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "abc")
	require.Equal(t, "abc", m.input.String())

	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "ab", m.input.String(), "first Ctrl+Z undoes the last character")
	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "a", m.input.String(), "second Ctrl+Z undoes one more character")
	_, _ = m.Update(keyCtrlZ())
	require.Empty(t, m.input.String(), "third Ctrl+Z undoes back to empty")
}

// TestInputUndo_UndoesCtrlU verifies that a Ctrl+Z after Ctrl+U reinstates the
// full prompt that was cleared, so an accidental wipe is recoverable.
func TestInputUndo_UndoesCtrlU(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "do not lose this")
	_, _ = m.Update(keyCtrl('u'))
	require.Empty(t, m.input.String(), "Ctrl+U must clear the buffer")

	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "do not lose this", m.input.String(), "Ctrl+Z must reinstate the cleared text")
}

// TestInputUndo_UndoesBackspace verifies that Ctrl+Z after a Backspace restores
// the character that was deleted.
func TestInputUndo_UndoesBackspace(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "hello")
	_, _ = m.Update(keySpecial("backspace", tea.KeyBackspace))
	require.Equal(t, "hell", m.input.String())

	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "hello", m.input.String(), "Ctrl+Z must restore the backspaced character")
}

// TestInputUndo_UndoesWordDelete verifies that Ctrl+Z after Alt+Backspace
// restores the word that was deleted.
func TestInputUndo_UndoesWordDelete(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "fix the bug")
	_, _ = m.Update(keyAltBackspace())
	require.Equal(t, "fix the ", m.input.String())

	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "fix the bug", m.input.String(), "Ctrl+Z must restore the word-deleted text")
}

// TestInputUndo_NoopOnEmpty verifies Ctrl+Z on an empty buffer with no history
// is inert and does not panic.
func TestInputUndo_NoopOnEmpty(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(keyCtrlZ())
	require.Empty(t, m.input.String(), "Ctrl+Z on an empty buffer must be a no-op")
}

// TestInputRedo_RedoesAfterUndo verifies that Ctrl+Y after a Ctrl+Z reinstates
// the edit that was undone, walking the redo stack forward.
func TestInputRedo_RedoesAfterUndo(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "abc")
	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "ab", m.input.String())

	_, _ = m.Update(keyCtrlY())
	require.Equal(t, "abc", m.input.String(), "Ctrl+Y must redo the undone character")
}

// TestInputRedo_MultiStep verifies multiple undo/redo steps interleave correctly:
// undo then redo at each character boundary round-trips cleanly.
func TestInputRedo_MultiStep(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "xy")

	_, _ = m.Update(keyCtrlZ())
	_, _ = m.Update(keyCtrlZ())
	require.Empty(t, m.input.String(), "two undos should reach empty")

	_, _ = m.Update(keyCtrlY())
	require.Equal(t, "x", m.input.String(), "one redo reinstates first character")
	_, _ = m.Update(keyCtrlY())
	require.Equal(t, "xy", m.input.String(), "second redo reinstates second character")
}

// TestInputRedo_NoopOnEmpty verifies Ctrl+Y with no prior undo is inert.
func TestInputRedo_NoopOnEmpty(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "hello")
	_, _ = m.Update(keyCtrlY())
	require.Equal(t, "hello", m.input.String(), "Ctrl+Y with no undo history must be a no-op")
}

// TestInputRedo_ClearedByNewEdit verifies that typing after an undo discards the
// redo stack, so Ctrl+Y cannot reinstate an edit that was superseded by new input.
func TestInputRedo_ClearedByNewEdit(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	typeString(t, m, "abc")
	_, _ = m.Update(keyCtrlZ())
	require.Equal(t, "ab", m.input.String())

	// New edit after undo: this must clear the redo stack.
	typeString(t, m, "x")
	require.Equal(t, "abx", m.input.String())

	// Ctrl+Y must be a no-op because the redo stack was cleared.
	_, _ = m.Update(keyCtrlY())
	require.Equal(t, "abx", m.input.String(), "Ctrl+Y must be a no-op after a new edit clears the redo stack")
}

// TestPushUndo_NoDuplicateEntries verifies that pushing the same value twice
// does not store a duplicate undo entry, so a no-op edit (like Backspace on an
// empty buffer) does not produce phantom undo steps.
func TestPushUndo_NoDuplicateEntries(t *testing.T) {
	t.Parallel()

	var st inputState
	st.pushUndo("hello")
	st.pushUndo("hello") // duplicate — must not be stored again
	require.Equal(t, 1, len(st.undoStack), "duplicate pushes must not create extra undo entries")
}

// TestPushUndo_BoundedByMaxUndoHistory verifies the undo stack is capped at
// maxUndoHistory entries, dropping the oldest when the limit is exceeded.
func TestPushUndo_BoundedByMaxUndoHistory(t *testing.T) {
	t.Parallel()

	var st inputState
	for i := range maxUndoHistory + 10 {
		// Each push stores a distinct value so no dedup fires.
		st.pushUndo(strings.Repeat("x", i+1))
	}
	require.Equal(t, maxUndoHistory, len(st.undoStack),
		"undo stack must be capped at maxUndoHistory entries")
}

// --- Reverse history search (Ctrl+R / bck-i-search) tests ---

// keyCtrlR and keyCtrlG produce key-press messages for the history-search bindings.
func keyCtrlR() tea.KeyPressMsg { return keyCtrl('r') }
func keyCtrlG() tea.KeyPressMsg { return keyCtrl('g') }
func keyEsc() tea.KeyPressMsg   { return keySpecial("esc", tea.KeyEsc) }

// TestHistSearch_FindsNewestMatch asserts that typing a query finds the newest
// history entry containing it, placing it in the buffer.
func TestHistSearch_FindsNewestMatch(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(3))
	submitPrompt(t, h, "go test ./...")
	submitPrompt(t, h, "go build ./...")
	submitPrompt(t, h, "grep -r foo .")

	_, _ = h.model.Update(keyCtrlR())
	require.True(t, h.model.inputHistory.histSearchActive, "Ctrl+R must activate bck-i-search mode")

	typeString(t, h.model, "go")
	// "go build ./..." is the newest entry containing "go"
	require.Equal(t, "go build ./...", h.model.input.String(),
		"typing 'go' must recall the newest match")
}

// TestHistSearch_CtrlRStepsOlder asserts that pressing Ctrl+R again while in
// search mode steps to the next older match.
func TestHistSearch_CtrlRStepsOlder(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(3))
	submitPrompt(t, h, "go test ./...")
	submitPrompt(t, h, "go build ./...")
	submitPrompt(t, h, "grep -r foo .")

	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "go") // lands on "go build ./..."

	_, _ = h.model.Update(keyCtrlR()) // step to older
	require.Equal(t, "go test ./...", h.model.input.String(),
		"second Ctrl+R must step to the next older match")
}

// TestHistSearch_EscRestoresBuffer asserts that Esc cancels the search and
// restores the buffer that was in place when Ctrl+R was pressed.
func TestHistSearch_EscRestoresBuffer(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(1))
	submitPrompt(t, h, "go build ./...")

	typeString(t, h.model, "draft")
	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "go") // finds "go build ./..."
	require.Equal(t, "go build ./...", h.model.input.String())

	_, _ = h.model.Update(keyEsc())
	require.False(t, h.model.inputHistory.histSearchActive, "Esc must exit search mode")
	require.Equal(t, "draft", h.model.input.String(),
		"Esc must restore the pre-search buffer")
}

// TestHistSearch_CtrlGRestoresBuffer asserts that Ctrl+G (the classic cancel
// key) behaves identically to Esc, restoring the saved buffer.
func TestHistSearch_CtrlGRestoresBuffer(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(1))
	submitPrompt(t, h, "go build ./...")

	typeString(t, h.model, "saved")
	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "go")

	_, _ = h.model.Update(keyCtrlG())
	require.False(t, h.model.inputHistory.histSearchActive, "Ctrl+G must exit search mode")
	require.Equal(t, "saved", h.model.input.String(),
		"Ctrl+G must restore the pre-search buffer")
}

// TestHistSearch_EnterAcceptsAndSubmits asserts that Enter while in search
// mode commits the current match (exits search mode) and submits the prompt.
func TestHistSearch_EnterAcceptsAndSubmits(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(2))
	submitPrompt(t, h, "go build ./...")

	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "go") // finds "go build ./..."

	_, cmd := h.model.Update(keySpecial("enter", tea.KeyEnter))
	h.startBatch(t, cmd)
	h.drain(t, func() bool { return !h.model.running })

	require.False(t, h.model.inputHistory.histSearchActive, "Enter must exit search mode")
}

// TestHistSearch_BackspaceShrinsQuery asserts that Backspace in search mode
// removes the last query character and re-runs the wider search.
func TestHistSearch_BackshrinsQuery(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(2))
	submitPrompt(t, h, "go test ./...")
	submitPrompt(t, h, "run the tests")

	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "test") // narrow: finds "run the tests" (newest)
	require.Equal(t, "run the tests", h.model.input.String())

	_, _ = h.model.Update(keySpecial("backspace", tea.KeyBackspace)) // query becomes "tes"
	// "run the tests" still contains "tes" and is still the newest match.
	require.Equal(t, "run the tests", h.model.input.String())
}

// TestHistSearch_HintActiveWhileSearching asserts histSearchHint returns the
// "(bck-i-search):" prefix while search is active and "" otherwise.
func TestHistSearch_HintActiveWhileSearching(t *testing.T) {
	t.Parallel()

	var st inputState
	st.history = []string{"go build ./..."}
	require.Empty(t, st.histSearchHint(), "hint must be empty when search is not active")

	st.startHistSearch("")
	require.Contains(t, st.histSearchHint(), "(bck-i-search):", "hint must contain the prefix when active")

	st.histSearchAcceptChar("g")
	require.Contains(t, st.histSearchHint(), "g_", "hint must include the query and cursor")

	st.cancelHistSearch()
	require.Empty(t, st.histSearchHint(), "hint must be empty after cancel")
}

// TestHistSearch_NoMatchLeavesBufferUnchanged asserts that typing a query with
// no matching history entry does not clear the buffer, matching readline
// behavior where the "(failing bck-i-search):" variant keeps showing the
// prompt that was in the buffer before.
func TestHistSearch_NoMatchLeavesBufferUnchanged(t *testing.T) {
	t.Parallel()

	h := newAgentHarness(t, oneTurnScript(1))
	submitPrompt(t, h, "go build ./...")

	typeString(t, h.model, "hello") // live buffer
	_, _ = h.model.Update(keyCtrlR())
	typeString(t, h.model, "zzz") // no match

	require.True(t, h.model.inputHistory.histSearchActive)
	// Buffer must remain as it was (the last successful match or the saved buffer).
	require.NotEqual(t, "zzz", h.model.input.String(),
		"a non-matching query must not place the query itself in the buffer")
}

// TestHistSearch_EmptyHistoryIsNoop asserts that Ctrl+R with no history
// activates search mode but typing never finds anything, keeping the buffer empty.
func TestHistSearch_EmptyHistoryIsNoop(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(keyCtrlR())
	require.True(t, m.inputHistory.histSearchActive, "Ctrl+R must activate search mode even with no history")

	typeString(t, m, "any")
	require.Empty(t, m.input.String(), "with no history, searching must not place text in the buffer")
}

// TestHistSearch_RenderShowsHint asserts that renderMain includes the
// "(bck-i-search):" hint in the rendered output while search is active.
func TestHistSearch_RenderShowsHint(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	_, _ = m.Update(keyCtrlR())
	out := plainText(m.renderMain())
	require.Contains(t, out, "(bck-i-search):",
		"renderMain must show the bck-i-search hint while search is active")
}

// TestHistSearch_KeybindingDocumented asserts keybindingGroups lists Ctrl+R so
// it is discoverable via /keys, matching the contract that every handled key
// appears in the overlay.
func TestHistSearch_KeybindingDocumented(t *testing.T) {
	t.Parallel()

	found := false
	for _, g := range keybindingGroups {
		for _, b := range g.bindings {
			if b.key == "Ctrl+R" {
				found = true
			}
		}
	}
	require.True(t, found, "keybindingGroups must document Ctrl+R for history search")
}
