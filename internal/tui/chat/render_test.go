package chat

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// TestMarkdownRendererProducesStyledOutput asserts the renderer actually
// transforms markdown (emits ANSI escapes and drops the literal '#' heading
// marker), rather than returning the input unchanged.
func TestMarkdownRendererProducesStyledOutput(t *testing.T) {
	r := newMarkdownRenderer("dark")
	out, ok := r.Render("# Title\n\nsome **bold** text\n", 80)
	require.True(t, ok, "renderer should succeed")
	require.Contains(t, out, "\x1b[", "rendered markdown must contain ANSI styling")
	require.Contains(t, out, "Title")
	require.NotContains(t, out, "# Title", "heading marker should be rendered away")
}

// TestListRendersAssistantMarkdownOnFinish asserts a finished assistant message
// is rendered as styled markdown while a streaming one stays plain, and that
// enabling markdown changes the output versus the plain renderer.
func TestListRendersAssistantMarkdownOnFinish(t *testing.T) {
	body := "## Heading\n\n```go\nfmt.Println(\"hi\")\n```\n"

	plain := New()
	plain.Append(message.Message{ID: "a1", Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: body}}})
	plainOut := plain.Render(80)

	rich := New()
	rich.EnableMarkdown("dark")
	rich.Append(message.Message{ID: "a1", Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: body}}})
	richOut := rich.Render(80)

	require.NotEqual(t, plainOut, richOut, "markdown rendering must change the output")
	require.Contains(t, richOut, "\x1b[", "rich output must carry ANSI styling")
	// The fenced code block's ``` markers should be rendered away, not literal.
	require.NotContains(t, richOut, "```", "code fences should be rendered, not literal")
}

// TestStreamingStaysPlainUntilFinish asserts that while a message is streaming
// it is NOT markdown-rendered (so partial markdown does not flicker), and only
// becomes styled once FinishStream is called.
func TestStreamingStaysPlainUntilFinish(t *testing.T) {
	l := New()
	l.EnableMarkdown("dark")
	l.Stream("s1", "# Partial heading still streaming")
	streaming := l.Render(80)
	require.Contains(t, streaming, "▌", "streaming cursor should be present")
	require.Contains(t, streaming, "# Partial", "streaming text stays plain (heading marker intact)")

	l.FinishStream("s1")
	finished := l.Render(80)
	require.NotContains(t, finished, "▌", "cursor gone after finish")
	require.Contains(t, finished, "\x1b[", "finished assistant message is markdown-styled")
}
