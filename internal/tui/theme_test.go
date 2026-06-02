package tui

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// submitLine drives a typed line through the real key path: it writes the text
// into the input buffer and presses enter, exercising handleSlash exactly as a
// user would. It mirrors the input+enter pattern used by the other slash tests.
func submitLine(t *testing.T, m *model, line string) {
	t.Helper()
	m.input.WriteString(line)
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
}

// TestSlashTheme_SwitchesActiveThemeAndBack is the /theme contract test. It
// asserts that /theme light switches the active theme to a genuinely different
// palette (a known style/color changes) and that /theme dark switches back. The
// discriminator is the Base foreground color, which differs between the dark and
// light themes; comparing against the styles constructors keeps the test free of
// hardcoded hex (so TestStyles_NoHardcodedHex stays green).
func TestSlashTheme_SwitchesActiveThemeAndBack(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// Default is the dark theme.
	require.Equal(t, styles.NameDark, m.themeName)
	require.Equal(t, styles.Default().Base.GetForeground(), m.theme.Base.GetForeground())
	// The dark and light palettes must actually differ, or the test below proves
	// nothing.
	require.NotEqual(t, styles.Default().Base.GetForeground(), styles.Light().Base.GetForeground(),
		"the dark and light themes must use different foreground colors")

	// /theme light switches the active theme.
	submitLine(t, m, "/theme light")
	require.Equal(t, styles.NameLight, m.themeName, "/theme light must persist the choice on the model")
	require.Equal(t, styles.Light().Base.GetForeground(), m.theme.Base.GetForeground(),
		"/theme light must change the active Base foreground color")
	// The propagated component copies follow the new theme.
	require.Equal(t, styles.Light().Base.GetForeground(), m.footer.Theme.Base.GetForeground(),
		"the footer must re-theme on /theme light")
	require.Equal(t, styles.Light().Status.GetForeground(), m.status.Theme.Status.GetForeground(),
		"the status bar must re-theme on /theme light")
	// The confirmation dialog is pushed after the switch, so it adopts the new
	// theme: dialogs pushed after /theme re-render in the new palette.
	require.True(t, m.dialogs.Contains("theme"), "/theme light must surface a confirmation dialog")
	require.Equal(t, styles.Light().Modal.GetForeground(),
		m.dialogs.Top().(*dialog.Text).Theme.Modal.GetForeground(),
		"a dialog pushed after the switch must carry the new theme")
	m.dialogs.Pop()

	// /theme dark switches back.
	submitLine(t, m, "/theme dark")
	require.Equal(t, styles.NameDark, m.themeName, "/theme dark must restore the dark theme")
	require.Equal(t, styles.Default().Base.GetForeground(), m.theme.Base.GetForeground(),
		"/theme dark must restore the dark Base foreground color")
	require.Equal(t, styles.Default().Base.GetForeground(), m.footer.Theme.Base.GetForeground(),
		"the footer must re-theme back to dark")
}

// TestSlashTheme_Unknown_LeavesThemeUnchanged asserts an unknown theme name
// surfaces an error dialog and does not mutate the active theme.
func TestSlashTheme_Unknown_LeavesThemeUnchanged(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	before := m.theme.Base.GetForeground()

	submitLine(t, m, "/theme solarized")
	require.True(t, m.dialogs.Contains("error"), "an unknown theme must raise the error dialog")
	require.Equal(t, styles.NameDark, m.themeName, "an unknown theme must not change the active theme")
	require.Equal(t, before, m.theme.Base.GetForeground(), "an unknown theme must leave the palette untouched")
}

// TestSlashTheme_MarkdownRendererFollows asserts the glamour markdown style
// follows the active theme: a finished assistant markdown message renders
// differently under the light theme than under the dark theme, and the theme's
// paired Markdown style is what drives it. This proves the renderer was actually
// re-pointed at the new style (EnableMarkdown resets the render cache), not just
// that a name flipped.
func TestSlashTheme_MarkdownRendererFollows(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// A finished assistant markdown message exercises glamour styling.
	m.chat.Append(message.Message{
		ID:      "a1",
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: "# Heading\n\nsome **bold** body text\n"}},
	})

	width := 80

	// Render under the dark theme (the default).
	require.Equal(t, "dark", m.theme.Markdown, "the dark theme must pair with glamour's dark style")
	darkOut := m.chat.Render(width)
	require.Contains(t, darkOut, "\x1b[", "dark markdown must carry ANSI styling")

	// Switch to light and re-render.
	submitLine(t, m, "/theme light")
	require.Equal(t, "light", m.theme.Markdown, "the light theme must pair with glamour's light style")
	lightOut := m.chat.Render(width)
	require.Contains(t, lightOut, "\x1b[", "light markdown must carry ANSI styling")

	// The glamour style genuinely follows the theme: dark and light palettes emit
	// different ANSI for the same markdown.
	require.NotEqual(t, darkOut, lightOut,
		"the markdown renderer style must follow the theme (dark vs light output must differ)")
	m.dialogs.Pop()

	// Switching back to dark restores the original rendering.
	submitLine(t, m, "/theme dark")
	require.Equal(t, "dark", m.theme.Markdown)
	require.Equal(t, darkOut, m.chat.Render(width),
		"switching back to dark must restore the dark markdown rendering")
}
