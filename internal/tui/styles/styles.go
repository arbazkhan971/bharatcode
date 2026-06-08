// Package styles centralizes TUI colors and lipgloss styles.
package styles

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss/v2"
)

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
	// DiffAddEmph and DiffRemoveEmph accent the specific runs that changed
	// within a modified line, so a reviewer's eye lands on the edited words
	// rather than the whole reflowed line, matching the intra-line word-diff
	// highlighting of Claude Code and opencode. They build on the add/remove
	// colors with bold + reverse video so the changed run reads as a solid block
	// (and renders as a single styled span, unlike underline which lipgloss emits
	// per rune).
	DiffAddEmph    lipgloss.Style
	DiffRemoveEmph lipgloss.Style
	DiffHunk       lipgloss.Style
	DiffHeader     lipgloss.Style
	// DiffWhitespace flags trailing whitespace an added line introduced at its
	// end — the kind of whitespace error git's "diff --check" reports — by
	// drawing those blank cells as a reverse-video block so they are visible
	// rather than invisible, matching how delta and opencode surface introduced
	// trailing blanks in a reviewed diff.
	DiffWhitespace lipgloss.Style
	// Match emphasizes the runs of a line that matched an active scrollback
	// search, so the reader sees exactly what matched within the centered line —
	// the reverse-video hit highlight an editor draws on a search result. It
	// builds on the accent color with reverse video so the term reads as a solid
	// block.
	Match lipgloss.Style
	// MatchOther emphasizes the term on the other on-screen matches — every hit
	// that is not the current one — so a reader can see all occurrences at once
	// the way an editor draws every match, with only the active one reverse-
	// highlighted. It underlines in the accent color so these read as secondary
	// to the solid reverse-video current match.
	MatchOther lipgloss.Style
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
		Name:           p.name,
		Markdown:       p.markdown,
		Base:           base,
		Muted:          muted,
		Accent:         accent,
		Panel:          panel,
		Warn:           warn,
		Error:          err,
		Success:        success,
		Header:         panel.Bold(true).Padding(0, 1),
		Status:         panel.Padding(0, 1),
		Footer:         muted,
		Modal:          panel.Border(lipgloss.RoundedBorder()).Padding(1, 2),
		DiffAdd:        success,
		DiffRemove:     err,
		DiffAddEmph:    success.Bold(true).Reverse(true),
		DiffRemoveEmph: err.Bold(true).Reverse(true),
		DiffHunk:       accent,
		// File-header lines (---, +++, diff --git, index) are bold-muted so
		// file boundaries stand out from added/removed content in a multi-file
		// diff without competing with the accent-colored hunk markers.
		DiffHeader: muted.Bold(true),
		// Trailing whitespace shows as a reverse-video block in the remove color
		// so an introduced blank run reads as an error, the way git/delta flag it.
		DiffWhitespace: err.Reverse(true),
		Match:          accent.Reverse(true),
		MatchOther:     accent.Underline(true),
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

// ---------------------------------------------------------------------------
// Activity-stream primitives.
//
// The styled values below back the activity-stream transcript and the chat
// chrome (prompt, status bar, bordered input box, modals). They are a restrained
// palette — mostly the terminal's own foreground with a few accents (amber for
// the active model, blue for paths/links, faint red/green reserved for diffs) —
// so the surface reads like a production agent TUI rather than a wall of color.
//
// Their colors resolve once at package load via lightDark, chosen from the
// terminal's detected background so a single set of primitives looks right on
// both light and dark terminals without the caller threading a Theme. The named
// Theme constructors above (Default/Light/HighContrast) remain the switchable,
// persisted palette the rest of the TUI already wires; these primitives layer
// alongside them for the redesigned chat surface.
// ---------------------------------------------------------------------------

// Restrained accent palette. Each entry is a (light, dark) pair fed to
// lightDark; the light value is darker so it reads on a pale terminal and the
// dark value is brighter so it reads on a black one.
const (
	// amber tints the active model badge and "in-progress" accents — the one
	// warm color in the palette.
	amberLight = "#b5740a"
	amberDark  = "#e2b341"

	// blue tints paths and links.
	blueLight = "#0066cc"
	blueDark  = "#6cb6ff"

	// primary is the body foreground; it tracks the terminal's own text color so
	// prose reads as plain text, not a tinted block.
	primaryLight = "#1f2430"
	primaryDark  = "#d7dde8"

	// meta is the muted gray for sub-output, metadata, and the dimmer half of the
	// status bar.
	metaLight = "#5b6172"
	metaDark  = "#8b93a7"

	// faint is the nearly-invisible gray used for horizontal rules and elision
	// hints ("… +N lines") — present but recessive.
	faintLight = "#9aa0ad"
	faintDark  = "#4b5163"

	// diff add/delete are desaturated green/red, used only inside diffs.
	diffAddLight = "#1e8a3c"
	diffAddDark  = "#7bbf86"
	diffDelLight = "#c0392b"
	diffDelDark  = "#d98a8a"
)

// hasDarkBackground is detected once from the terminal so the primitives below
// resolve to a single coherent palette. Detection failures (for example no real
// terminal under test) fall back to dark, the default surface.
var hasDarkBackground = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)

// lightDark picks the light or dark variant of a color pair for the detected
// background. Build colors through it rather than hardcoding a single hex so the
// primitives read on both light and dark terminals.
var lightDark = lipgloss.LightDark(hasDarkBackground)

// Primitive colors, resolved once for the detected background.
var (
	colorPrimary = lightDark(lipgloss.Color(primaryLight), lipgloss.Color(primaryDark))
	colorMetaC   = lightDark(lipgloss.Color(metaLight), lipgloss.Color(metaDark))
	colorFaint   = lightDark(lipgloss.Color(faintLight), lipgloss.Color(faintDark))
	colorAmber   = lightDark(lipgloss.Color(amberLight), lipgloss.Color(amberDark))
	colorBlue    = lightDark(lipgloss.Color(blueLight), lipgloss.Color(blueDark))
	colorAddC    = lightDark(lipgloss.Color(diffAddLight), lipgloss.Color(diffAddDark))
	colorDelC    = lightDark(lipgloss.Color(diffDelLight), lipgloss.Color(diffDelDark))
)

// Restrained styled primitives. These are immutable lipgloss styles; copy and
// extend them (.Bold(true), .Width(n), ...) at the call site as needed.
var (
	// Primary renders body text in the terminal's foreground.
	Primary = lipgloss.NewStyle().Foreground(colorPrimary)
	// Muted renders sub-output, metadata, and secondary text.
	Muted = lipgloss.NewStyle().Foreground(colorMetaC)
	// Faint renders the most recessive elements (rules, elision hints).
	Faint = lipgloss.NewStyle().Foreground(colorFaint)
	// Accent (amber) marks the active model and in-progress activity — the one
	// warm accent.
	Accent = lipgloss.NewStyle().Foreground(colorAmber)
	// Link (blue) marks paths and links.
	Link = lipgloss.NewStyle().Foreground(colorBlue)
	// DiffAdd / DiffDel color added / removed diff lines. Reserved for diffs so
	// green/red carry meaning wherever they appear.
	DiffAdd = lipgloss.NewStyle().Foreground(colorAddC)
	DiffDel = lipgloss.NewStyle().Foreground(colorDelC)

	// Verb renders the bold action verb that leads a transcript turn
	// ("Editing", "Running", ...).
	Verb = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	// Meta renders the muted trailing metadata on a turn line.
	Meta = Muted
	// Placeholder renders the dim prompt placeholder text.
	Placeholder = lipgloss.NewStyle().Foreground(colorFaint)

	// InputBox frames the prompt textarea: a rounded border in the muted color
	// with single-column horizontal padding so the cursor never sits on the
	// frame.
	InputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMetaC).
			Padding(0, 1)

	// InputBoxActive is InputBox with the accent border, used while the prompt is
	// focused so the active field reads at a glance.
	InputBoxActive = InputBox.BorderForeground(colorAmber)

	// ModalBox frames dialogs (model picker, sessions, onboarding, command
	// palette): a rounded border in the accent color with interior padding so the
	// modal floats above the transcript.
	ModalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAmber).
			Padding(1, 2)
)

// Glyph strings used by the activity-stream layout. They are plain runes so a
// caller can measure or pad around them; wrap in a style (e.g. Faint.Render) to
// color them.
const (
	// BulletGlyph leads each transcript turn.
	BulletGlyph = "•"
	// ConnectorGlyph leads a turn's muted sub-output line.
	ConnectorGlyph = "└"
	// PromptGlyph is the input prompt marker ("› ").
	PromptGlyph = "› "
)

// Bullet returns the turn bullet in the accent color, the marker that leads each
// activity-stream turn.
func Bullet() string {
	return Accent.Render(BulletGlyph)
}

// Connector returns the muted "└" that leads a turn's sub-output line, so nested
// command output reads as subordinate to the turn that produced it.
func Connector() string {
	return Muted.Render(ConnectorGlyph)
}

// Prompt returns the styled input prompt glyph ("› ") in the accent color.
func Prompt() string {
	return Accent.Render(PromptGlyph)
}

// Rule returns a faint full-width horizontal rule of the given width, the thin
// divider drawn between transcript turns. A width below 1 yields the empty
// string so a rule never renders wider than its pane; callers pass the measured
// content width.
func Rule(width int) string {
	if width < 1 {
		return ""
	}
	return Faint.Render(strings.Repeat("─", width))
}

// ModelBadge renders the minimal status-bar model descriptor: the model name in
// the warm amber accent followed by a muted effort qualifier ("kimi-k2 high").
// An empty effort renders just the model; an empty model renders the empty
// string so the badge collapses cleanly when nothing is active.
func ModelBadge(model, effort string) string {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	if model == "" {
		return ""
	}
	badge := Accent.Render(model)
	if effort != "" {
		badge += " " + Muted.Render(effort)
	}
	return badge
}

// IsDarkBackground reports whether the primitives resolved against a dark
// terminal background. Components that need to make their own light/dark choice
// (for example building a matching glamour renderer) can branch on it so they
// stay consistent with the rest of the palette.
func IsDarkBackground() bool {
	return hasDarkBackground
}
