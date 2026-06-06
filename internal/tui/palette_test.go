package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// keyCtrlK returns the Ctrl+K key press used to open the command palette.
func keyCtrlK() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModCtrl})
}

// TestAllPaletteEntries_ContainsBuiltins asserts that every entry in
// paletteBuiltinOrder appears in allPaletteEntries with the expected
// description from slashCommandDescriptions.
func TestAllPaletteEntries_ContainsBuiltins(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	entries := m.allPaletteEntries()
	byName := make(map[string]paletteEntry, len(entries))
	for _, e := range entries {
		byName[e.name] = e
	}
	for _, name := range paletteBuiltinOrder {
		e, ok := byName[name]
		require.Truef(t, ok, "allPaletteEntries must include built-in command %s", name)
		require.Equal(t, slashCommandDescriptions[name], e.desc,
			"description for %s must match slashCommandDescriptions", name)
	}
}

// TestAllPaletteEntries_CountMatchesBuiltins asserts the number of entries
// returned by allPaletteEntries matches the known built-in count when no
// dynamic commands are registered (the default harness has none).
func TestAllPaletteEntries_CountMatchesBuiltins(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	entries := m.allPaletteEntries()
	// With no dynamic commands, exactly the built-ins are listed.
	require.Equal(t, len(paletteBuiltinOrder), len(entries))
}

// TestVisiblePaletteEntries_EmptyFilter returns all entries unchanged.
func TestVisiblePaletteEntries_EmptyFilter(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = ""
	require.Equal(t, m.allPaletteEntries(), m.visiblePaletteEntries())
}

// TestVisiblePaletteEntries_PrefixFilter asserts commands whose name starts
// with the query rank in the first tier.
func TestVisiblePaletteEntries_PrefixFilter(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = "di" // prefix of "/diff"

	visible := m.visiblePaletteEntries()
	require.NotEmpty(t, visible)
	// The first result must be the prefix match, not a later substring.
	require.True(t, strings.HasPrefix(visible[0].name, "/di"),
		"prefix match must rank first: got %s", visible[0].name)
}

// TestVisiblePaletteEntries_SubstringFilter asserts a filter that matches
// mid-word (not a name prefix) still surfaces the command.
func TestVisiblePaletteEntries_SubstringFilter(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = "lear" // substring of "/clear"

	visible := m.visiblePaletteEntries()
	names := make([]string, len(visible))
	for i, e := range visible {
		names[i] = e.name
	}
	require.Contains(t, names, "/clear")
}

// TestVisiblePaletteEntries_NoMatch asserts an unrecognised filter returns an
// empty list without panicking.
func TestVisiblePaletteEntries_NoMatch(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = "zzzzzzz"

	require.Empty(t, m.visiblePaletteEntries())
}

// TestVisiblePaletteEntries_CaseInsensitive asserts the filter is
// case-insensitive.
func TestVisiblePaletteEntries_CaseInsensitive(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	m.paletteFilter = "HELP"
	upper := m.visiblePaletteEntries()
	m.paletteFilter = "help"
	lower := m.visiblePaletteEntries()
	require.Equal(t, len(upper), len(lower), "case must not change match count")
}

// TestPaletteWindowBounds_SmallList asserts the whole list is shown when it
// fits within the window.
func TestPaletteWindowBounds_SmallList(t *testing.T) {
	t.Parallel()
	start, end := paletteWindowBounds(0, paletteWindow-1)
	require.Equal(t, 0, start)
	require.Equal(t, paletteWindow-1, end)
}

// TestPaletteWindowBounds_LargeList asserts the window scrolls to keep the
// cursor visible.
func TestPaletteWindowBounds_LargeList(t *testing.T) {
	t.Parallel()
	total := paletteWindow * 3
	// Cursor at the very end: window should clamp to the bottom.
	start, end := paletteWindowBounds(total-1, total)
	require.Equal(t, total, end)
	require.Equal(t, total-paletteWindow, start)
	require.Equal(t, paletteWindow, end-start)
}

// TestCtrlK_OpensPalette asserts that pressing Ctrl+K pushes a "palette"
// dialog to the stack.
func TestCtrlK_OpensPalette(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	require.True(t, m.dialogs.Contains("palette"), "Ctrl+K must open the command palette dialog")
}

// TestCtrlK_PaletteBodyContainsCommands asserts the open palette dialog body
// lists known built-in commands from the first window of entries (cursor=0).
// Commands beyond paletteWindow are folded into a "⋯ N more below" marker and
// are not checked here; TestPaletteBody_* covers the full entry set.
func TestCtrlK_PaletteBodyContainsCommands(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	body := plainText(m.dialogs.Render(200))
	// /help is always the first entry and /diff is within the first paletteWindow
	// rows, so both are visible without scrolling.
	require.Contains(t, body, "/help")
	require.Contains(t, body, "/diff")
}

// TestPaletteKey_TypeToFilter asserts that pressing printable keys while the
// palette is open narrows the visible list via m.paletteFilter.
func TestPaletteKey_TypeToFilter(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	require.True(t, m.dialogs.Contains("palette"))

	// Type "di" to narrow to diff-related commands.
	_, _ = m.Update(keyText("d"))
	_, _ = m.Update(keyText("i"))
	require.Equal(t, "di", m.paletteFilter)
	require.True(t, m.dialogs.Contains("palette"), "palette must stay open while typing filter")
}

// TestPaletteKey_Backspace removes the last filter character without closing
// the palette.
func TestPaletteKey_Backspace(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	_, _ = m.Update(keyText("d"))
	_, _ = m.Update(keyText("i"))
	require.Equal(t, "di", m.paletteFilter)

	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	require.Equal(t, "d", m.paletteFilter, "backspace must remove the last filter rune")
	require.True(t, m.dialogs.Contains("palette"))
}

// TestPaletteKey_BackspaceOnEmptyFilter does not crash when the filter is
// already empty.
func TestPaletteKey_BackspaceOnEmptyFilter(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	require.Equal(t, "", m.paletteFilter)
	require.NotPanics(t, func() {
		_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	})
	require.True(t, m.dialogs.Contains("palette"), "palette must remain open on backspace with empty filter")
}

// TestPaletteKey_UpDownMoveCursor asserts the cursor moves within the visible
// list and is clamped at the bounds.
func TestPaletteKey_UpDownMoveCursor(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	require.Equal(t, 0, m.paletteCursor, "cursor starts at 0")

	// Down moves cursor forward.
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 1, m.paletteCursor)

	// Up moves cursor back.
	_, _ = m.Update(keySpecial("up", tea.KeyUp))
	require.Equal(t, 0, m.paletteCursor)

	// Up at 0 clamps (no wrap).
	_, _ = m.Update(keySpecial("up", tea.KeyUp))
	require.Equal(t, 0, m.paletteCursor, "cursor must clamp at 0, not wrap")
}

// TestPaletteKey_EnterExecutesCommand asserts that pressing Enter on a palette
// entry closes the dialog and executes the selected slash command — verified by
// checking the dialog stack is cleared.
func TestPaletteKey_EnterExecutesCommand(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	require.True(t, m.dialogs.Contains("palette"))

	// Select the first entry (cursor 0) — any built-in command is fine; /help
	// is always first and opens another dialog rather than running the agent.
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.False(t, m.dialogs.Contains("palette"), "Enter must pop the palette dialog")
}

// TestPaletteKey_EnterOnNoMatchIsNoop asserts that pressing Enter with a
// filter that matches nothing keeps the palette open without panicking.
func TestPaletteKey_EnterOnNoMatchIsNoop(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	m.paletteFilter = "zzzzzzz" // matches nothing
	m.refreshCommandPalette()

	require.NotPanics(t, func() {
		_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	})
	require.True(t, m.dialogs.Contains("palette"), "Enter on empty match list must not close the palette")
}

// TestPaletteKey_FilterResetsCursor asserts that typing to filter resets the
// cursor to 0 so the user always starts from the best match.
func TestPaletteKey_FilterResetsCursor(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	_, _ = m.Update(keyCtrlK())
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 2, m.paletteCursor)

	// Typing a filter character resets the cursor.
	_, _ = m.Update(keyText("h"))
	require.Equal(t, 0, m.paletteCursor, "typing a filter must reset the cursor to 0")
}

// TestPaletteBody_ShowsFilterCount asserts the palette body includes the
// "N of M" tally when a filter is active, matching the session picker display.
func TestPaletteBody_ShowsFilterCount(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = "di"

	body := plainText(m.paletteBody())
	require.Contains(t, body, "Filter: di")
	require.Contains(t, body, "of")
}

// TestPaletteBody_EmptyMatchMessage asserts the palette body reports "no
// commands match" when the filter eliminates every entry.
func TestPaletteBody_EmptyMatchMessage(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	m.paletteFilter = "zzzzzzz"

	body := plainText(m.paletteBody())
	require.Contains(t, body, "no commands match")
}

// TestPaletteBody_ShowsShortcutForCommandWithKey asserts that the palette body
// includes the keyboard shortcut hint for commands that have a slashCommandKeys
// entry, so users learn the faster Ctrl+X path while browsing the palette.
func TestPaletteBody_ShowsShortcutForCommandWithKey(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	// Position the cursor on /model (the first command with a Ctrl+X shortcut).
	// /model is within paletteBuiltinOrder; find its index so the cursor lands on it.
	entries := m.allPaletteEntries()
	modelIdx := -1
	for i, e := range entries {
		if e.name == "/model" {
			modelIdx = i
			break
		}
	}
	require.GreaterOrEqualf(t, modelIdx, 0, "/model must appear in allPaletteEntries")

	m.paletteCursor = modelIdx
	body := plainText(m.paletteBody())
	require.Contains(t, body, "Ctrl+P", "palette body must show Ctrl+P shortcut next to /model (selected row)")
}

// TestPaletteBody_ShowsShortcutOnUnselectedRow asserts that palette rows that
// are NOT selected also display their keyboard shortcut hint.
func TestPaletteBody_ShowsShortcutOnUnselectedRow(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	// Find /agent (Ctrl+A) and ensure it is NOT the selected row, but still
	// within the visible window. Position the cursor one row after /agent.
	entries := m.allPaletteEntries()
	agentIdx := -1
	for i, e := range entries {
		if e.name == "/agent" {
			agentIdx = i
			break
		}
	}
	require.GreaterOrEqualf(t, agentIdx, 0, "/agent must appear in allPaletteEntries")

	// Select the row just after /agent so /agent is unselected but visible.
	nextIdx := agentIdx + 1
	if nextIdx >= len(entries) {
		nextIdx = agentIdx - 1
	}
	m.paletteCursor = nextIdx
	body := plainText(m.paletteBody())
	require.Contains(t, body, "Ctrl+A", "palette body must show Ctrl+A shortcut next to unselected /agent row")
}

// TestPaletteBody_NoSpuriousShortcut asserts that a command without a
// slashCommandKeys entry does NOT gain a shortcut hint in the palette body.
func TestPaletteBody_NoSpuriousShortcut(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)

	// /help has no slashCommandKeys entry and must not show any "(Ctrl+…)" suffix.
	// Filter to just /help so it is the only visible row.
	m.paletteFilter = "help"
	m.paletteCursor = 0
	body := plainText(m.paletteBody())
	require.Contains(t, body, "/help", "/help must appear in the filtered palette")
	// No shortcut should appear for /help.
	for _, key := range slashCommandKeys {
		require.NotContains(t, body, "("+key+")",
			"palette body for /help must not show shortcut (%s) that belongs to another command", key)
	}
}

// TestKeybindingGroups_IncludesPalette asserts the /keys overlay lists the
// Ctrl+K command-palette binding so users can discover it from the help text.
func TestKeybindingGroups_IncludesPalette(t *testing.T) {
	t.Parallel()
	found := false
	for _, g := range keybindingGroups {
		for _, kb := range g.bindings {
			if kb.key == "Ctrl+K" {
				found = true
			}
		}
	}
	require.True(t, found, "keybindingGroups must list Ctrl+K for the command palette")
}
