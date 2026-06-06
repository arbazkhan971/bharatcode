package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKeybindingHelp_Grouped proves the /keys overlay is rendered as titled
// sections — the grouping that lets a reader scan shortcuts by category instead
// of one flat list — and that every group's header and binding survives into the
// body.
func TestKeybindingHelp_Grouped(t *testing.T) {
	body := keybindingHelpBody()

	require.NotEmpty(t, keybindingGroups, "there must be at least one shortcut group")
	for _, g := range keybindingGroups {
		require.NotEmpty(t, g.title, "every group needs a title")
		require.NotEmpty(t, g.bindings, "group %q must list at least one binding", g.title)

		// The title appears on its own line, with the group's bindings indented
		// beneath it so the header reads as a section heading rather than a row.
		// The first group heads the body; the rest follow a blank line.
		headsBody := strings.HasPrefix(body, g.title+"\n")
		midBody := strings.Contains(body, "\n"+g.title+"\n")
		require.True(t, headsBody || midBody, "group title %q must head its own line", g.title)
		for _, b := range g.bindings {
			require.Contains(t, body, b.key, "binding key %q must appear in the overlay", b.key)
			require.Contains(t, body, b.desc, "binding description for %q must appear", b.key)
		}
	}
}

// TestKeybindingHelp_AlignedDescriptions proves every binding's description
// starts at the same column across all groups, so the overlay stays aligned even
// though a multi-byte arrow glyph ("Ctrl+←/→") would misalign a byte-based pad.
func TestKeybindingHelp_AlignedDescriptions(t *testing.T) {
	body := keybindingHelpBody()

	// Build the lookup of every (key, desc) pair so each rendered binding row can
	// be checked for its description offset.
	descs := map[string]string{}
	for _, g := range keybindingGroups {
		for _, b := range g.bindings {
			descs[b.key] = b.desc
		}
	}

	col := -1
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimPrefix(line, "  ")
		if len(trimmed) == len(line) {
			// No two-space indent: this is a group header, not a binding row.
			continue
		}
		for key, desc := range descs {
			if !strings.HasPrefix(trimmed, key) {
				continue
			}
			// The description must follow the padded key; measure its rune offset
			// from the start of the (indented) row content.
			idx := strings.Index(trimmed, desc)
			require.GreaterOrEqual(t, idx, len([]rune(key)), "description for %q must follow its key", key)
			runeCol := len([]rune(trimmed[:idx]))
			if col == -1 {
				col = runeCol
			}
			require.Equal(t, col, runeCol, "binding %q description must align to the shared column", key)
			break
		}
	}
	require.Greater(t, col, 0, "expected to find at least one aligned binding row")
}

// TestKeybindingHelp_FilterNarrowsToMatchingRows proves "/keys <filter>" keeps
// only the bindings whose key or description matches the filter, dropping the
// groups and rows that do not, so a user hunting for one binding sees just the
// relevant rows.
func TestKeybindingHelp_FilterNarrowsToMatchingRows(t *testing.T) {
	body := keybindingHelpBodyFiltered("scroll")

	// The two scroll bindings describe their behaviour as scrolling the chat, so
	// both survive the filter.
	require.Contains(t, body, "scroll the chat one line at a time")
	require.Contains(t, body, "scroll the chat a page at a time")
	// An unrelated binding from another section is filtered out entirely.
	require.NotContains(t, body, "open the model picker")
	require.NotContains(t, body, "new tab")
}

// TestKeybindingHelp_FilterMatchesGroupTitle proves a filter matching a section
// title keeps that whole section, so "/keys tabs" surfaces every tab shortcut
// even though the individual rows need not contain the word.
func TestKeybindingHelp_FilterMatchesGroupTitle(t *testing.T) {
	groups := filterKeybindingGroups("tabs")

	require.Len(t, groups, 1, "only the Tabs section should survive a 'tabs' filter")
	require.Equal(t, "Tabs", groups[0].title)

	// Find the source Tabs group and confirm every one of its bindings is kept,
	// including those whose own text never mentions "tabs".
	var src keyGroup
	for _, g := range keybindingGroups {
		if g.title == "Tabs" {
			src = g
		}
	}
	require.Equal(t, src.bindings, groups[0].bindings, "a title match keeps all of the group's bindings")
}

// TestKeybindingHelp_FilterCaseInsensitive proves the filter folds case, so a
// shouted filter still matches a lower-case description.
func TestKeybindingHelp_FilterCaseInsensitive(t *testing.T) {
	require.Equal(t, keybindingHelpBodyFiltered("scroll"), keybindingHelpBodyFiltered("SCROLL"))
}

// TestKeybindingHelp_EmptyFilterIsFullOverlay proves a blank or whitespace-only
// filter renders the complete overlay, so a bare "/keys" is unchanged.
func TestKeybindingHelp_EmptyFilterIsFullOverlay(t *testing.T) {
	full := keybindingHelpBody()
	require.Equal(t, full, keybindingHelpBodyFiltered(""))
	require.Equal(t, full, keybindingHelpBodyFiltered("   "))
}

// TestKeybindingHelp_FilterNoMatchNote proves a filter that matches nothing
// yields a quiet explanatory note rather than an empty overlay.
func TestKeybindingHelp_FilterNoMatchNote(t *testing.T) {
	body := keybindingHelpBodyFiltered("zzzznope")
	require.Contains(t, body, "No shortcuts match")
	require.Contains(t, body, "zzzznope")
	require.Empty(t, filterKeybindingGroups("zzzznope"))
}

// TestKeybindingHelp_FilteredOverlayStaysAligned proves the shared description
// column still aligns after a filter drops rows, since the key column is sized
// from the surviving bindings rather than the full keymap.
func TestKeybindingHelp_FilteredOverlayStaysAligned(t *testing.T) {
	body := keybindingHelpBodyFiltered("scroll")

	col := -1
	rows := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimPrefix(line, "  ")
		if len(trimmed) == len(line) {
			continue // group header, not a binding row
		}
		idx := strings.Index(trimmed, "scroll the chat")
		require.GreaterOrEqual(t, idx, 0, "every surviving row should be a scroll binding")
		runeCol := len([]rune(trimmed[:idx]))
		if col == -1 {
			col = runeCol
		}
		require.Equal(t, col, runeCol, "filtered rows must align to a shared column")
		rows++
	}
	require.Equal(t, 2, rows, "expected exactly the two scroll bindings")
}

// TestKeybindingHelp_NoTrailingBlankLine guards the overlay against a trailing
// blank line, which a dialog would render as wasted vertical space.
func TestKeybindingHelp_NoTrailingBlankLine(t *testing.T) {
	body := keybindingHelpBody()
	require.NotEmpty(t, body)
	require.False(t, strings.HasSuffix(body, "\n"), "overlay must not end with a blank line")
}
