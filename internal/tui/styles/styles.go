// Package styles centralizes TUI colors and lipgloss styles.
package styles

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// Brand palette.
//
// BharatCode's identity is the Indian tricolor: saffron is the primary accent,
// green the secondary, woven into a warm dark surface (the terminal is dark, so
// the brand's paper background is dropped in favor of a warm near-black that
// complements saffron). The named-theme constants below build the switchable
// Default/Light/HighContrast palettes; the lightDark-resolved primitives further
// down build the bordered-panel chrome for the redesigned chat surface. Both
// draw from the same brand hues so the wordmark, panels, and rendered markdown
// read as one identity.
// ---------------------------------------------------------------------------

// Dark palette (the default) — warm tricolor on a warm near-black ground.
// Saffron is softened slightly for sustained on-screen comfort but stays
// clearly saffron; green is brightened for legibility on dark.
const (
	colorBase    = "#e8e2d6" // warm off-white body text
	colorMuted   = "#8c857a" // muted warm grey for meta/secondary
	colorAccent  = "#f0a85a" // saffron, softened for dark
	colorPanel   = "#1e1b17" // raised warm panel surface
	colorWarn    = "#f0a85a" // saffron doubles as the warn/in-progress hue
	colorError   = "#e06c5e" // warm red
	colorSuccess = "#4fb050" // brightened tricolor green
)

// Light palette (a high-readability theme for light terminals). The base
// foreground is dark so text reads against a light terminal background, and the
// panel inverts to a pale warm fill with dark text. Accents deepen to the
// brand's print hues (true saffron #FF9933 and green #138808) so they read on
// paper.
const (
	lightBase    = "#2a2620"
	lightMuted   = "#6b6357"
	lightAccent  = "#cc6a14" // deepened saffron for light backgrounds
	lightPanel   = "#f3ede1" // warm paper panel
	lightWarn    = "#cc6a14"
	lightError   = "#c0392b"
	lightSuccess = "#138808" // brand green at full print saturation
)

// High-contrast palette (maximally legible: pure white text on black panels
// with saturated brand accents). Useful for low-vision use or high-glare
// displays. The accents stay on-brand (vivid saffron and green) so the identity
// survives even at maximum contrast.
const (
	hcBase    = "#ffffff"
	hcMuted   = "#c8c2b6"
	hcAccent  = "#ffb04d" // vivid saffron
	hcPanel   = "#000000"
	hcWarn    = "#ffb04d"
	hcError   = "#ff5555"
	hcSuccess = "#5bd45b" // vivid green
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
	// highlighting of an activity-stream review surface. They build on the
	// add/remove colors with bold + reverse video so the changed run reads as a
	// solid block (and renders as a single styled span, unlike underline which
	// lipgloss emits per rune).
	DiffAddEmph    lipgloss.Style
	DiffRemoveEmph lipgloss.Style
	DiffHunk       lipgloss.Style
	DiffHeader     lipgloss.Style
	// DiffWhitespace flags trailing whitespace an added line introduced at its
	// end — the kind of whitespace error git's "diff --check" reports — by
	// drawing those blank cells as a reverse-video block so they are visible
	// rather than invisible.
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
		Name:     p.name,
		Markdown: p.markdown,
		Base:     base,
		Muted:    muted,
		Accent:   accent,
		Panel:    panel,
		Warn:     warn,
		Error:    err,
		Success:  success,
		Header:   panel.Bold(true).Padding(0, 1),
		// The status bar leads with the saffron accent so the active model badge
		// is the brand's primary color, set on the raised panel surface.
		Status:         panel.Padding(0, 1),
		Footer:         muted,
		Modal:          panel.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.accent)).Padding(1, 2),
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

// Default returns BharatCode's default dark terminal theme: warm tricolor on a
// warm near-black ground.
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
// text and a pale warm panel fill, with the brand's deepened print accents. Its
// markdown renderer follows with glamour's light style.
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
// panels with saturated brand accents. It pairs with glamour's dark markdown
// style.
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
// Activity-stream primitives — premium dark tricolor chrome.
//
// The styled values below back the activity-stream transcript and the chat
// chrome: the bordered input panel, role-differentiated turn blocks, the
// segmented status bar with its saffron model badge, refined separators, and the
// single tasteful tricolor brand moment. They are the BharatCode identity made
// concrete — warm grounds, saffron primary, green secondary — composed into
// framed, raised-reading panels rather than flat text.
//
// Their colors resolve once at package load via lightDark, chosen from the
// terminal's detected background so a single set of primitives looks right on
// both light and dark terminals without the caller threading a Theme; they are
// tuned for dark, the default coding surface. The named Theme constructors above
// (Default/Light/HighContrast) remain the switchable, persisted palette the rest
// of the TUI already wires; these primitives layer alongside them for the
// redesigned chat surface.
// ---------------------------------------------------------------------------

// Brand palette as (light, dark) pairs fed to lightDark; the light value is
// deeper so it reads on a pale terminal and the dark value is brighter/softer so
// it reads on a warm near-black one.
const (
	// saffron is the PRIMARY brand accent: the model badge, the › prompt, the
	// active panel border, the assistant accent bar, the wordmark's saffron.
	// Softened on dark for sustained comfort while staying clearly saffron;
	// deepened on light so it reads on paper.
	saffronLight = "#cc6a14"
	saffronDark  = "#f0a85a"

	// green is the SECONDARY tricolor accent: success, the green half of the
	// tricolor motif, diff-add. Full print saturation on light, brightened on
	// dark for legibility.
	greenLight = "#138808"
	greenDark  = "#4fb050"

	// brandWhite is the bright center of the tricolor wordmark/rule — the white
	// stripe of the flag. On dark it is the warm off-white body color; on light
	// it darkens so the stripe stays visible against paper.
	brandWhiteLight = "#2a2620"
	brandWhiteDark  = "#f4efe6"

	// blue tints paths and links — the brand's soft tech-blue (#58A6FF on dark,
	// the same hue as the website's link accent), recessive and never loud. The
	// light variant deepens so it still reads on paper.
	blueLight = "#2f6fb0"
	blueDark  = "#58a6ff"

	// primary is the body foreground: a warm off-white on dark, near-black warm
	// grey on light, so prose reads as plain text rather than a tinted block.
	primaryLight = "#2a2620"
	primaryDark  = "#e8e2d6"

	// meta is the muted warm grey for sub-output, metadata, role labels, and the
	// dimmer half of the status bar.
	metaLight = "#6b6357"
	metaDark  = "#8c857a"

	// faint is the nearly-invisible warm grey used for refined rules and elision
	// hints ("… +N lines") — present but recessive.
	faintLight = "#a39a8c"
	faintDark  = "#5c564c"

	// panelSurface is the raised panel fill — a touch lighter than the ground so
	// a bordered region reads as raised. Used as the badge/pill background tint.
	panelSurfaceLight = "#ece5d7"
	panelSurfaceDark  = "#1e1b17"

	// onAccent is the text color drawn on top of the saffron badge fill: a dark
	// warm near-black so the saffron pill reads as a solid, legible chip on both
	// backgrounds.
	onAccentLight = "#fff6ea"
	onAccentDark  = "#15130f"

	// diff add/delete are desaturated green/red, used only inside diffs. Add
	// leans on the brand green so additions feel on-brand.
	diffAddLight = "#138808"
	diffAddDark  = "#6fbf73"
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
	colorSaffron = lightDark(lipgloss.Color(saffronLight), lipgloss.Color(saffronDark))
	colorGreen   = lightDark(lipgloss.Color(greenLight), lipgloss.Color(greenDark))
	colorWhite   = lightDark(lipgloss.Color(brandWhiteLight), lipgloss.Color(brandWhiteDark))
	colorBlue    = lightDark(lipgloss.Color(blueLight), lipgloss.Color(blueDark))
	colorSurface = lightDark(lipgloss.Color(panelSurfaceLight), lipgloss.Color(panelSurfaceDark))
	colorOnAcc   = lightDark(lipgloss.Color(onAccentLight), lipgloss.Color(onAccentDark))
	colorAddC    = lightDark(lipgloss.Color(diffAddLight), lipgloss.Color(diffAddDark))
	colorDelC    = lightDark(lipgloss.Color(diffDelLight), lipgloss.Color(diffDelDark))
)

// Restrained styled primitives. These are immutable lipgloss styles; copy and
// extend them (.Bold(true), .Width(n), ...) at the call site as needed.
var (
	// Primary renders body text in the warm off-white foreground.
	Primary = lipgloss.NewStyle().Foreground(colorPrimary)
	// Muted renders sub-output, metadata, and secondary text.
	Muted = lipgloss.NewStyle().Foreground(colorMetaC)
	// Faint renders the most recessive elements (rules, elision hints).
	Faint = lipgloss.NewStyle().Foreground(colorFaint)
	// Accent (saffron) is the PRIMARY brand accent: the active model, the prompt,
	// in-progress activity, the assistant bar.
	Accent = lipgloss.NewStyle().Foreground(colorSaffron)
	// Success (green) is the SECONDARY tricolor accent: completion and the green
	// half of any tricolor motif.
	Success = lipgloss.NewStyle().Foreground(colorGreen)
	// Link (soft blue) marks paths and links.
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

	// InputPanel frames the prompt textarea while it is focused: a rounded
	// border in the saffron brand accent with single-column horizontal padding so
	// the cursor never sits on the frame and the active field reads as a raised,
	// framed region at a glance.
	InputPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSaffron).
			Padding(0, 1)

	// InputPanelBlurred is InputPanel with a muted border, used while the prompt
	// is not focused so the frame recedes without losing its shape.
	InputPanelBlurred = InputPanel.BorderForeground(colorMetaC)

	// ModalPanel frames dialogs (model picker, sessions, onboarding, command
	// palette): a rounded border in the saffron accent with interior padding so
	// the modal floats above the transcript as a distinct raised panel.
	ModalPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSaffron).
			Padding(1, 2)

	// AssistantAccent renders the saffron "▍" left bar that leads an assistant
	// turn block, the vertical brand accent that marks who is speaking and gives
	// the assistant block a framed left edge.
	AssistantAccent = lipgloss.NewStyle().Foreground(colorSaffron)

	// AssistantBlock draws the saffron left accent bar down the whole height of a
	// multi-line assistant turn: a left-only rounded border in the brand saffron
	// plus a one-column left pad, so every wrapped line of the block shares one
	// continuous brand edge and a slight indent (more robust than a single leading
	// glyph, which only marks the first line). Set .Width(n) at the call site to
	// the measured content width so the bar clips with the pane. Pairs with
	// UserBlock, whose edge is recessive so the two turns read as distinct.
	AssistantBlock = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(colorSaffron).
			PaddingLeft(1)

	// UserBlock draws the recessive muted left edge down a user turn, the muted
	// counterpart to AssistantBlock's saffron bar so a reader instantly tells the
	// two turns apart by the color (and weight) of their shared left edge.
	UserBlock = AssistantBlock.BorderForeground(colorFaint)

	// AssistantLabel renders the assistant role label in the saffron accent,
	// bold, so the reader instantly tells the assistant is speaking.
	AssistantLabel = lipgloss.NewStyle().Foreground(colorSaffron).Bold(true)
	// UserLabel renders the user role label in muted warm grey, recessive against
	// the accented assistant label so the two turns are visually distinct.
	UserLabel = lipgloss.NewStyle().Foreground(colorMetaC).Bold(true)
	// Timestamp renders the "· HH:MM" suffix on a role label in the faintest
	// chrome so the time reads as incidental metadata.
	Timestamp = lipgloss.NewStyle().Foreground(colorFaint)

	// Badge renders the saffron status-bar model pill: dark warm text on a
	// saffron fill with single-column padding, so the active model reads as a
	// solid brand chip rather than loose colored text.
	Badge = lipgloss.NewStyle().
		Foreground(colorOnAcc).
		Background(colorSaffron).
		Padding(0, 1).
		Bold(true)

	// Separator renders the dim "·" segment divider in the status bar, the thin
	// gap between badge, cost, and meta segments.
	Separator = lipgloss.NewStyle().Foreground(colorFaint)
)

// Backwards-compatible aliases. The input box was previously named InputBox /
// InputBoxActive and the modal ModalBox; the redesigned names are InputPanel /
// InputPanelBlurred and ModalPanel. Keep the old names pointing at the new
// styles so existing call sites compile unchanged while the compose phase
// migrates to the panel vocabulary.
var (
	// InputBox is the focused input frame (alias of InputPanel).
	InputBox = InputPanel
	// InputBoxActive is the focused input frame (alias of InputPanel); the active
	// state is the saffron-bordered default.
	InputBoxActive = InputPanel
	// ModalBox frames dialogs (alias of ModalPanel).
	ModalBox = ModalPanel
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
	// AccentBarGlyph is the "▍" left bar that marks an assistant turn block.
	AccentBarGlyph = "▍"
)

// Bullet returns the turn bullet in the saffron accent, the marker that leads
// each activity-stream turn.
func Bullet() string {
	return Accent.Render(BulletGlyph)
}

// Connector returns the muted "└" that leads a turn's sub-output line, so nested
// command output reads as subordinate to the turn that produced it.
func Connector() string {
	return Muted.Render(ConnectorGlyph)
}

// Prompt returns the styled input prompt glyph ("› ") in the saffron accent.
func Prompt() string {
	return Accent.Render(PromptGlyph)
}

// AccentBar returns the saffron "▍" left bar that leads an assistant turn block.
func AccentBar() string {
	return AssistantAccent.Render(AccentBarGlyph)
}

// RoleLabel returns a turn's role label styled for who is speaking: the
// assistant label in saffron-bold and the user label in muted-bold, with an
// optional "· HH:MM" timestamp suffix in the faintest chrome. ts is the
// preformatted time string ("" to omit it). A blank role falls back to
// "message" rendered muted.
func RoleLabel(role, ts string) string {
	var labelStyle lipgloss.Style
	switch role {
	case "assistant":
		labelStyle = AssistantLabel
	case "user":
		labelStyle = UserLabel
	default:
		labelStyle = UserLabel
		if role == "" {
			role = "message"
		}
	}
	out := labelStyle.Render(role)
	if ts != "" {
		out += " " + Timestamp.Render("· "+ts)
	}
	return out
}

// SoftRule returns a refined, recessive separator of the given width: a faint
// dotted "·" run rather than a heavy solid line, the subtle divider drawn
// between transcript turns. A width below 1 yields the empty string so a rule
// never renders wider than its pane; callers pass the measured content width.
func SoftRule(width int) string {
	if width < 1 {
		return ""
	}
	return Faint.Render(strings.Repeat("·", width))
}

// Rule is the legacy thin full-width separator (a faint "─" run). SoftRule is
// the refined replacement; Rule is retained so existing call sites compile.
func Rule(width int) string {
	if width < 1 {
		return ""
	}
	return Faint.Render(strings.Repeat("─", width))
}

// TurnGap returns the vertical spacing drawn between transcript turns when a
// rule would read as too heavy — generous breathing room instead of a line.
// It is two blank lines, the gap a premium transcript leaves around each turn.
func TurnGap() string {
	return "\n\n"
}

// TricolorRule returns the single tasteful brand moment: a thin horizontal rule
// split into three saffron / white / green stripes, the Indian tricolor woven
// into one refined accent line. The stripes split the width into near-thirds; a
// width below 3 collapses to a plain saffron run (and below 1 to the empty
// string) so the rule always fits its pane. Use sparingly — one tricolor moment,
// not a recurring motif.
func TricolorRule(width int) string {
	if width < 1 {
		return ""
	}
	if width < 3 {
		return Accent.Render(strings.Repeat("─", width))
	}
	third := width / 3
	// Center stripe absorbs the remainder so the three segments sum to width.
	mid := width - 2*third
	saffron := Accent.Render(strings.Repeat("─", third))
	white := lipgloss.NewStyle().Foreground(colorWhite).Render(strings.Repeat("─", mid))
	green := Success.Render(strings.Repeat("─", third))
	return saffron + white + green
}

// Wordmark returns the "BharatCode" brand wordmark with a tasteful tricolor
// treatment: "Bharat" in saffron and "Code" in green, the two halves of the
// brand carrying the flag's primary colors. Used once in the header so the
// identity is stated without painting the whole surface.
func Wordmark() string {
	return Accent.Bold(true).Render("Bharat") + Success.Bold(true).Render("Code")
}

// ModelBadge renders the status-bar model descriptor as a saffron brand pill:
// the model name in dark warm text on a saffron fill, followed by a muted effort
// qualifier outside the pill ("[ kimi-k2 ] high"). An empty effort renders just
// the pill; an empty model renders the empty string so the badge collapses
// cleanly when nothing is active.
func ModelBadge(model, effort string) string {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	if model == "" {
		return ""
	}
	badge := Badge.Render(model)
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
