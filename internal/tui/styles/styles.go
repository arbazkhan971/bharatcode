// Package styles centralizes TUI colors and lipgloss styles.
package styles

import "github.com/charmbracelet/lipgloss/v2"

// Dark palette (the default).
const (
	colorBase    = "#d7dde8"
	colorMuted   = "#8b93a7"
	colorAccent  = "#4fb3ff"
	colorPanel   = "#202434"
	colorWarn    = "#e5a50a"
	colorError   = "#ff6b6b"
	colorSuccess = "#51cf66"
)

// Light palette (a high-readability theme for light terminals). The base
// foreground is dark so text reads against a light terminal background, and the
// panel inverts to a pale fill with dark text.
const (
	lightBase    = "#1f2430"
	lightMuted   = "#5b6172"
	lightAccent  = "#0066cc"
	lightPanel   = "#e6e9f0"
	lightWarn    = "#b5740a"
	lightError   = "#c0392b"
	lightSuccess = "#1e8a3c"
)

// High-contrast palette (maximally legible: pure white text on black panels
// with saturated accents). Useful for low-vision use or high-glare displays.
const (
	hcBase    = "#ffffff"
	hcMuted   = "#c0c0c0"
	hcAccent  = "#00ffff"
	hcPanel   = "#000000"
	hcWarn    = "#ffff00"
	hcError   = "#ff5555"
	hcSuccess = "#55ff55"
)

// Theme names. These are the values accepted by /theme and stored on the model
// to persist the active choice.
const (
	NameDark         = "dark"
	NameLight        = "light"
	NameHighContrast = "high-contrast"
)

// Theme contains named styles shared by TUI subcomponents.
type Theme struct {
	// Name is the theme's identifier (for example "dark" or "light"). It is the
	// value /theme switches between and the model persists.
	Name string
	// Markdown is the glamour standard style this theme pairs with ("dark" or
	// "light"), so the markdown renderer follows the active theme.
	Markdown string

	Base       lipgloss.Style
	Muted      lipgloss.Style
	Accent     lipgloss.Style
	Panel      lipgloss.Style
	Warn       lipgloss.Style
	Error      lipgloss.Style
	Success    lipgloss.Style
	Header     lipgloss.Style
	Status     lipgloss.Style
	Footer     lipgloss.Style
	Modal      lipgloss.Style
	DiffAdd    lipgloss.Style
	DiffRemove lipgloss.Style
	DiffHunk   lipgloss.Style
}

// palette is the set of raw colors a theme is built from.
type palette struct {
	name     string
	markdown string
	base     string
	muted    string
	accent   string
	panel    string
	warn     string
	err      string
	success  string
}

// build assembles a Theme from a palette. All themes share the same style
// composition; only the colors and the paired glamour markdown style differ.
func build(p palette) Theme {
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(p.base))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(p.muted))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(p.accent))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color(p.warn))
	err := lipgloss.NewStyle().Foreground(lipgloss.Color(p.err))
	success := lipgloss.NewStyle().Foreground(lipgloss.Color(p.success))
	panel := lipgloss.NewStyle().Foreground(lipgloss.Color(p.base)).Background(lipgloss.Color(p.panel))

	return Theme{
		Name:       p.name,
		Markdown:   p.markdown,
		Base:       base,
		Muted:      muted,
		Accent:     accent,
		Panel:      panel,
		Warn:       warn,
		Error:      err,
		Success:    success,
		Header:     panel.Bold(true).Padding(0, 1),
		Status:     panel.Padding(0, 1),
		Footer:     muted,
		Modal:      panel.Border(lipgloss.RoundedBorder()).Padding(1, 2),
		DiffAdd:    success,
		DiffRemove: err,
		DiffHunk:   accent,
	}
}

// Default returns BharatCode's default dark terminal theme.
func Default() Theme {
	return build(palette{
		name:     NameDark,
		markdown: "dark",
		base:     colorBase,
		muted:    colorMuted,
		accent:   colorAccent,
		panel:    colorPanel,
		warn:     colorWarn,
		err:      colorError,
		success:  colorSuccess,
	})
}

// Light returns a theme tuned for light terminal backgrounds: dark foreground
// text and a pale panel fill. Its markdown renderer follows with glamour's
// light style.
func Light() Theme {
	return build(palette{
		name:     NameLight,
		markdown: "light",
		base:     lightBase,
		muted:    lightMuted,
		accent:   lightAccent,
		panel:    lightPanel,
		warn:     lightWarn,
		err:      lightError,
		success:  lightSuccess,
	})
}

// HighContrast returns a maximally legible theme: pure white text on black
// panels with saturated accents. It pairs with glamour's dark markdown style.
func HighContrast() Theme {
	return build(palette{
		name:     NameHighContrast,
		markdown: "dark",
		base:     hcBase,
		muted:    hcMuted,
		accent:   hcAccent,
		panel:    hcPanel,
		warn:     hcWarn,
		err:      hcError,
		success:  hcSuccess,
	})
}

// ByName returns the theme registered under name and whether it exists. Lookup
// is exact: callers should normalize case before calling.
func ByName(name string) (Theme, bool) {
	switch name {
	case NameDark:
		return Default(), true
	case NameLight:
		return Light(), true
	case NameHighContrast:
		return HighContrast(), true
	default:
		return Theme{}, false
	}
}

// Names returns the selectable theme names in display order.
func Names() []string {
	return []string{NameDark, NameLight, NameHighContrast}
}
