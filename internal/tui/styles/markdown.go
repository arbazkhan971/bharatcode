package styles

import (
	_ "embed"
	"fmt"

	"github.com/charmbracelet/glamour"
)

// markdownDarkJSON is a restrained dark glamour theme derived from glamour's
// stock dark style and calmed for the activity-stream transcript: the h1
// highlight background is flattened, every heading is unified to one muted amber
// accent, and the rule/link/inline-code colors are dimmed so prose reads as
// mostly monochrome with only a few accents. The chat component pairs it with
// the viewport width via NewMarkdownRenderer.
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
