// Package logo renders the BharatCode wordmark: "Bharat" in saffron and "Code"
// in green, framed by a saffron│white│green tricolour rule and a diagonal field.
package logo

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/arbazkhan971/bharatcode/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

const diag = `╱`

// brandWhite is the white stripe of the tricolour, used for the rule and the
// " Pro" suffix.
var brandWhite = lipgloss.Color("#f4efe6")

// Opts are the options for rendering the BharatCode wordmark.
type Opts struct {
	FieldColor   color.Color // diagonal texture
	TitleColorA  color.Color // "Bharat" — saffron
	TitleColorB  color.Color // "Code" — green
	CharmColor   color.Color // " Pro" suffix / accent (white stripe)
	VersionColor color.Color // version text color
	Width        int         // width of the rendered logo, used for truncation
	Hyper        bool        // whether it is BharatCode or BharatCode Pro

	// Unstable is retained for API compatibility; the wordmark now renders
	// deterministically, so it has no effect.
	Unstable bool
}

func fg(c color.Color, bold bool, s string) string {
	st := lipgloss.NewStyle().Foreground(c)
	if bold {
		st = st.Bold(true)
	}
	return st.Render(s)
}

// tricolorRule renders a saffron│white│green horizontal rule of width w.
func tricolorRule(w int, saffron, green color.Color) string {
	if w < 3 {
		w = 3
	}
	seg := w / 3
	return fg(saffron, false, strings.Repeat("─", seg)) +
		fg(brandWhite, false, strings.Repeat("─", seg)) +
		fg(green, false, strings.Repeat("─", w-2*seg))
}

// wordmark builds the two-tone "BharatCode" (+ " Pro" when Hyper) wordmark.
func wordmark(o Opts) string {
	word := fg(o.TitleColorA, true, "Bharat") + fg(o.TitleColorB, true, "Code")
	if o.Hyper {
		word += fg(o.CharmColor, true, " Pro")
	}
	return word
}

// diagField renders height rows of width diagonal `╱` characters.
func diagField(c color.Color, width, height int) string {
	row := fg(c, false, strings.Repeat(diag, max(1, width)))
	b := new(strings.Builder)
	for i := range height {
		b.WriteString(row)
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Render renders the BharatCode wordmark. Set compact to true for the narrow
// sidebar version; false renders the wide main-pane version framed by diagonal
// fields. The signature is unchanged from the original logo so callers in
// internal/ui/model do not need to change.
func Render(base lipgloss.Style, version string, compact bool, o Opts) string {
	word := wordmark(o)
	wordWidth := lipgloss.Width(word)

	// Version, truncated to the wordmark width.
	ver := ""
	if version != "" {
		ver = fg(o.VersionColor, false, ansi.Truncate(version, wordWidth, "…"))
	}

	// Stacked block: rule / wordmark / rule / version.
	lines := []string{
		tricolorRule(wordWidth, o.TitleColorA, o.TitleColorB),
		word,
		tricolorRule(wordWidth, o.TitleColorA, o.TitleColorB),
	}
	if ver != "" {
		lines = append(lines, ver)
	}
	block := strings.Join(lines, "\n")

	if compact {
		// Sidebar form: diagonal texture, wordmark block, diagonal texture.
		field := fg(o.FieldColor, false, strings.Repeat(diag, wordWidth))
		return strings.Join([]string{field, block, field, ""}, "\n")
	}

	// Wide form: diagonal fields to the left and right of the wordmark block.
	blockHeight := lipgloss.Height(block)
	const leftWidth = 6
	leftField := diagField(o.FieldColor, leftWidth, blockHeight)
	rightWidth := max(15, o.Width-wordWidth-leftWidth-2) // 2 for the gaps.
	rightField := diagField(o.FieldColor, rightWidth, blockHeight)

	const hGap = " "
	out := lipgloss.JoinHorizontal(lipgloss.Top, leftField, hGap, block, hGap, rightField)
	if o.Width > 0 {
		ls := strings.Split(out, "\n")
		for i, line := range ls {
			ls[i] = ansi.Truncate(line, o.Width, "")
		}
		out = strings.Join(ls, "\n")
	}
	return out
}

// SmallRender renders a compact single-line wordmark for sidebars and small
// windows. The signature is unchanged from the original logo.
func SmallRender(t *styles.Styles, width int, o Opts) string {
	saffron := t.Logo.TitleColorA
	green := t.Logo.TitleColorB

	title := fg(saffron, true, "Bharat") + fg(green, true, "Code")
	if o.Hyper {
		title += fg(brandWhite, true, " Pro")
	}

	remainingWidth := width - lipgloss.Width(title) - 1 // 1 for the trailing space.
	if remainingWidth > 0 {
		lines := strings.Repeat(diag, remainingWidth)
		title = fmt.Sprintf("%s %s", title, t.Logo.SmallDiagonals.Render(lines))
	}
	return title
}
