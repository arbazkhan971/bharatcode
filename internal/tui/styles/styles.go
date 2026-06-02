// Package styles centralizes TUI colors and lipgloss styles.
package styles

import "github.com/charmbracelet/lipgloss/v2"

const (
	colorBase    = "#d7dde8"
	colorMuted   = "#8b93a7"
	colorAccent  = "#4fb3ff"
	colorPanel   = "#202434"
	colorWarn    = "#e5a50a"
	colorError   = "#ff6b6b"
	colorSuccess = "#51cf66"
)

// Theme contains named styles shared by TUI subcomponents.
type Theme struct {
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

// Default returns BharatCode's default dark terminal theme.
func Default() Theme {
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorBase))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarn))
	err := lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	success := lipgloss.NewStyle().Foreground(lipgloss.Color(colorSuccess))
	panel := lipgloss.NewStyle().Foreground(lipgloss.Color(colorBase)).Background(lipgloss.Color(colorPanel))

	return Theme{
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
