package styles

import (
	_ "embed"
	"fmt"

	"github.com/charmbracelet/glamour"
)

// markdownDarkJSON is the premium dark glamour theme for the activity-stream
// transcript, tuned to the BharatCode brand: headings lead in saffron and step
// down to a muted warm grey, inline code and fenced blocks sit on a subtly
// tinted warm-dark surface with a desaturated warm chroma palette, links are a
// soft recessive blue, and the rule is faint — so prose reads as warm and
// branded with only a few accents rather than a wall of color. The chat
// component pairs it with the viewport width via NewMarkdownRenderer.
//
//go:embed markdown-dark.json
var markdownDarkJSON []byte

// MarkdownDarkJSON returns the embedded restrained dark glamour theme bytes so
// callers that build their own renderer (or want to derive a variant) can reuse
// the same palette the chat transcript renders with.
func MarkdownDarkJSON() []byte {
	out := make([]byte, len(markdownDarkJSON))
	copy(out, markdownDarkJSON)
	return out
}

// NewMarkdownRenderer builds a glamour TermRenderer that renders assistant
// markdown to ANSI for the transcript, using the embedded restrained dark theme
// wrapped at width. Preserved newlines keep the model's intentional line breaks
// (for example inside fenced blocks) instead of reflowing them away. The chat
// component calls this with the viewport width and rebuilds it on resize.
//
// A width below 1 is clamped to 1 so glamour never receives a non-positive wrap
// that would panic; callers that have no width yet should avoid rendering until
// a WindowSizeMsg arrives.
func NewMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	if width < 1 {
		width = 1
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes(markdownDarkJSON),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil, fmt.Errorf("building markdown renderer: %w", err)
	}
	return r, nil
}
