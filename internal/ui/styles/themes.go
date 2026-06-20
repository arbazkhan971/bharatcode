package styles

import (
	"charm.land/lipgloss/v2"
)

// Tiranga (तिरंगा) palette — the Indian tricolour, tuned for a warm-dark
// terminal. Saffron and green are the brand spine; the white stripe shows up
// as the off-white text and the wordmark divider.
var (
	// Brand spine.
	cSaffron      = lipgloss.Color("#f0a85a") // primary
	cSaffronVivid = lipgloss.Color("#ffb04d")
	cSaffronDeep  = lipgloss.Color("#cc6a14")
	cGreen        = lipgloss.Color("#4fb050") // secondary
	cGreenDeep    = lipgloss.Color("#3c8c40")
	cGreenDeeper  = lipgloss.Color("#2e6e34")
	cWhite        = lipgloss.Color("#f4efe6") // brand white stripe

	// Foregrounds (warm, brightest -> dimmest).
	cFg     = lipgloss.Color("#e8e2d6")
	cFgSub  = lipgloss.Color("#c9c1b3")
	cFgMore = lipgloss.Color("#8c857a")
	cFgMost = lipgloss.Color("#6b655c")

	// Backgrounds (warm near-black -> visible warm panels).
	cBg        = lipgloss.Color("#15120e")
	cBgLeast   = lipgloss.Color("#1e1b17")
	cBgLess    = lipgloss.Color("#2a2620")
	cBgMost    = lipgloss.Color("#3a342b")
	cSeparator = lipgloss.Color("#4a4339")

	cOnSaffron = lipgloss.Color("#1a1206")

	// Statuses.
	cError = lipgloss.Color("#e06c5e")
	cBlue  = lipgloss.Color("#58a6ff")
)

// ThemeForProvider returns the Styles associated with the given provider ID.
// Unknown or empty provider IDs yield the default Tiranga dark theme.
func ThemeForProvider(providerID string) Styles {
	switch providerID {
	case "hyper":
		return BharatCodeProObsidiana()
	default:
		return CharmtonePantera()
	}
}

// CharmtonePantera is the default theme. It is kept as a thin shim over
// [TirangaDark] so the many callers/tests that reference it by name keep
// working while the real palette lives in TirangaDark.
func CharmtonePantera() Styles {
	return TirangaDark()
}

// TirangaDark is the default BharatCode theme: the Indian tricolour on a warm
// near-black background. Saffron is primary, green is secondary, and the white
// stripe shows through as the off-white foreground.
func TirangaDark() Styles {
	s := quickStyle(quickStyleOpts{
		// Brand.
		primary:   cSaffron,
		secondary: cGreen,
		accent:    cSaffronVivid,
		keyword:   cGreenDeep,

		fgBase:       cFg,
		fgSubtle:     cFgSub,
		fgMoreSubtle: cFgMore,
		fgMostSubtle: cFgMost,

		onPrimary: cOnSaffron,

		bgBase:         cBg,
		bgLeastVisible: cBgLeast,
		bgLessVisible:  cBgLess,
		bgMostVisible:  cBgMost,

		separator: cSeparator,

		destructive:       cError,
		error:             cError,
		warningSubtle:     lipgloss.Color("#ffc97a"),
		warning:           cSaffronVivid,
		denied:            cSaffronDeep,
		busy:              lipgloss.Color("#d98c3a"),
		info:              cBlue,
		infoMoreSubtle:    lipgloss.Color("#4a7fc0"),
		infoMostSubtle:    lipgloss.Color("#6b89a8"),
		success:           lipgloss.Color("#5cc35d"),
		successMoreSubtle: cGreen,
		successMostSubtle: cGreenDeep,

		// ANSI 16-color palette for remapping raw terminal output onto
		// legible, on-brand tricolour colours.
		ansiBlack:   lipgloss.Color("#100d0a"),
		ansiRed:     cError,
		ansiGreen:   cGreen,
		ansiYellow:  cSaffronVivid,
		ansiBlue:    cBlue,
		ansiMagenta: lipgloss.Color("#b87fd0"),
		ansiCyan:    lipgloss.Color("#5fb3b3"),
		ansiWhite:   cFgSub,

		ansiBrightBlack:   cFgMost,
		ansiBrightRed:     lipgloss.Color("#ef8478"),
		ansiBrightGreen:   lipgloss.Color("#5cc35d"),
		ansiBrightYellow:  lipgloss.Color("#ffc97a"),
		ansiBrightBlue:    lipgloss.Color("#79b8ff"),
		ansiBrightMagenta: lipgloss.Color("#d6a0e6"),
		ansiBrightCyan:    lipgloss.Color("#7fcaca"),
		ansiBrightWhite:   cWhite,
	})

	// Tricolour wordmark: "Bharat" in saffron, "Code" in green, framed by the
	// saffron|white|green rule. logo.Render reads TitleColorA for "Bharat" and
	// TitleColorB for "Code".
	s.Logo.FieldColor = cSaffron
	s.Logo.TitleColorA = cSaffron
	s.Logo.TitleColorB = cGreen
	s.Logo.CharmColor = cWhite
	s.Logo.VersionColor = cFgMore
	s.Logo.SmallCharm = lipgloss.NewStyle().Foreground(cWhite)
	s.Logo.SmallDiagonals = lipgloss.NewStyle().Foreground(cSaffron)
	s.Logo.SmallGradFromColor = cSaffron
	s.Logo.SmallGradToColor = cGreen

	// Header compact wordmark gradient: saffron -> green.
	s.Header.LogoGradFromColor = cSaffron
	s.Header.LogoGradToColor = cGreen

	return s
}

// BharatCodeProObsidiana is the BharatCode Pro theme: a more vivid saffron over
// a deeper obsidian base. It derives from [TirangaDark] so every field stays
// populated.
func BharatCodeProObsidiana() Styles {
	s := TirangaDark()

	// Vivid Pro accents.
	s.Logo.FieldColor = cSaffronVivid
	s.Logo.TitleColorA = cSaffronVivid
	s.Logo.TitleColorB = lipgloss.Color("#5cc35d")
	s.Logo.SmallGradFromColor = cSaffronVivid
	s.Logo.SmallGradToColor = lipgloss.Color("#5cc35d")
	s.Logo.SmallDiagonals = lipgloss.NewStyle().Foreground(cSaffronVivid)
	s.Header.Diagonals = s.Header.Diagonals.Foreground(cSaffronVivid)
	s.Header.LogoGradFromColor = cSaffronVivid
	s.Header.LogoGradToColor = cGreenDeeper

	return s
}
