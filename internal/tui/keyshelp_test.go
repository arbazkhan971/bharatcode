package tui

import (
	"fmt"
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

// TestKeybindingHelp_DocumentsSubmitKey proves the overlay documents Enter as the
// prompt-send key. handleKey maps "enter" to submitInput, and the overlay's
// contract is to mirror the bindings handleKey services; without an Enter row a
// reader scanning /keys would find every editing and navigation key but no way to
// learn how the prompt is sent.
func TestKeybindingHelp_DocumentsSubmitKey(t *testing.T) {
	var send *keyBinding
	for _, g := range keybindingGroups {
		for i := range g.bindings {
			if g.bindings[i].key == "Enter" {
				send = &g.bindings[i]
			}
		}
	}
	require.NotNil(t, send, "the overlay must document the Enter key that sends the prompt")
	require.Contains(t, strings.ToLower(send.desc), "send", "Enter's description should say it sends the prompt")

	// The rendered body carries the row too, so a user reading the overlay sees it.
	require.Contains(t, keybindingHelpBody(), "Enter")

	// A user hunting for how to submit can filter to it the way they would any
	// other binding.
	require.Contains(t, keybindingHelpBodyFiltered("send"), "Enter")
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

// TestKeybindingHelp_FilterAllTermsMustMatch proves a multi-word filter is split
// into terms that must all match a binding, so a query naming a shortcut from two
// angles ("tab switch") finds it even though no single run of its text contains
// both words — and a binding missing any one term is dropped.
func TestKeybindingHelp_FilterAllTermsMustMatch(t *testing.T) {
	groups := filterKeybindingGroups("tab switch")

	require.NotEmpty(t, groups, "bindings covering both terms should survive a two-term filter")

	// Only bindings where both terms land somewhere (title, key, or description)
	// are kept. The Ctrl+←/→ tab-switcher's description carries both "tab" and
	// "switch", so it survives even though no single word does.
	var got []keyBinding
	for _, g := range groups {
		got = append(got, g.bindings...)
	}
	var foundSwitcher bool
	for _, b := range got {
		require.Contains(t, strings.ToLower(b.key+" "+b.desc), "tab", "every survivor matches 'tab'")
		require.Contains(t, strings.ToLower(b.key+" "+b.desc), "switch", "every survivor matches 'switch'")
		if strings.Contains(b.desc, "switch to the previous/next tab") {
			foundSwitcher = true
		}
	}
	require.True(t, foundSwitcher, "the Ctrl+←/→ tab switcher should survive")

	// Rows matching only one term — "new tab" and "close tab" never mention
	// "switch" — are dropped.
	body := keybindingHelpBodyFiltered("tab switch")
	require.NotContains(t, body, "new tab")
	require.NotContains(t, body, "close tab")
}

// TestKeybindingHelp_FilterTermOrderIndependent proves the term split is
// order-independent: the same binding surfaces however its words are arranged.
// The overlay's count header echoes the query verbatim (so it differs by
// arrangement), so the order-independence is asserted on the filtering itself.
func TestKeybindingHelp_FilterTermOrderIndependent(t *testing.T) {
	require.Equal(t, filterKeybindingGroups("tab switch"), filterKeybindingGroups("switch tab"))
}

// TestKeybindingHelp_FilterCollapsesInnerWhitespace proves extra spaces between
// terms are ignored, so a stray double space does not change the matched rows.
func TestKeybindingHelp_FilterCollapsesInnerWhitespace(t *testing.T) {
	require.Equal(t, filterKeybindingGroups("tab switch"), filterKeybindingGroups("tab   switch"))
}

// TestKeybindingHelp_FilterCaseInsensitive proves the filter folds case, so a
// shouted filter still matches a lower-case description.
func TestKeybindingHelp_FilterCaseInsensitive(t *testing.T) {
	require.Equal(t, filterKeybindingGroups("scroll"), filterKeybindingGroups("SCROLL"))
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

// TestKeybindingHelp_FilterShowsMatchCount proves a successful filter leads with
// an "M of N shortcuts match …" count header, reporting how many bindings
// survived against the full keymap so a narrowing reads as a measured search
// result rather than a silent list.
func TestKeybindingHelp_FilterShowsMatchCount(t *testing.T) {
	body := keybindingHelpBodyFiltered("scroll")

	total := countBindings(keybindingGroups)
	matched := countBindings(filterKeybindingGroups("scroll"))
	require.Equal(t, 2, matched, "the two scroll bindings should match")

	header := body[:strings.Index(body, "\n")]
	require.Equal(t,
		fmt.Sprintf("%d of %d shortcuts match %q", matched, total, "scroll"),
		header,
		"the count header should lead the filtered overlay")

	// The count sits on its own line above a blank separator so it reads as a
	// header rather than a binding row, and the bindings still follow.
	require.True(t, strings.HasPrefix(body, header+"\n\n"), "a blank line should separate the count from the bindings")
	require.Contains(t, body, "scroll the chat one line at a time")
}

// TestKeybindingHelp_FilterCountIsSingularForOneMatch proves the count noun
// agrees in number, so a filter that keeps a single binding reads "1 … shortcut
// match" rather than the plural.
func TestKeybindingHelp_FilterCountIsSingularForOneMatch(t *testing.T) {
	// "model picker" lands on exactly the Ctrl+P binding.
	groups := filterKeybindingGroups("model picker")
	require.Equal(t, 1, countBindings(groups), "only the model-picker binding should match")

	body := keybindingHelpBodyFiltered("model picker")
	require.Contains(t, body, "shortcut match", "a single match uses the singular noun")
	require.NotContains(t, body, "shortcuts match", "a single match must not use the plural noun")
}

// TestKeybindingHelp_NoCountHeaderWhenUnfiltered proves the count header is a
// filtered-overlay affordance only: a bare "/keys" renders the full keymap with
// no count line prepended.
func TestKeybindingHelp_NoCountHeaderWhenUnfiltered(t *testing.T) {
	require.NotContains(t, keybindingHelpBody(), "shortcuts match")
	require.NotContains(t, keybindingHelpBodyFiltered("   "), "shortcuts match")
}
