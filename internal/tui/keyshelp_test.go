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

// TestKeybindingHelp_NoTrailingBlankLine guards the overlay against a trailing
// blank line, which a dialog would render as wasted vertical space.
func TestKeybindingHelp_NoTrailingBlankLine(t *testing.T) {
	body := keybindingHelpBody()
	require.NotEmpty(t, body)
	require.False(t, strings.HasSuffix(body, "\n"), "overlay must not end with a blank line")
}
