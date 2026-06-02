package session

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// roleLabel maps a message role to a human-readable transcript label.
func roleLabel(r message.Role) string {
	switch r {
	case message.RoleUser:
		return "User"
	case message.RoleAssistant:
		return "Assistant"
	case message.RoleSystem:
		return "System"
	case message.RoleTool:
		return "Tool"
	default:
		return string(r)
	}
}

// htmlBlock is one rendered content block for the HTML template.
// Kind selects the rendering shape; Text/Code carry the payload.
// All fields are emitted via {{.}} interpolation, so html/template
// escapes them automatically.
type htmlBlock struct {
	Kind string // "text", "thinking", "tool_use", "tool_result", "image", "unknown".
	Text string // Prose payload (text/thinking blocks).
	Code string // Preformatted payload (tool/image/unknown blocks).
	Lang string // Optional descriptor shown above a code block.
	Err  bool   // True for tool_result blocks that reported an error.
}

// htmlTurn is one message rendered for the HTML template.
type htmlTurn struct {
	Role   string
	When   string
	Blocks []htmlBlock
}

// htmlDoc is the top-level data passed to the HTML template.
type htmlDoc struct {
	Title string
	Model string
	Agent string
	Turns []htmlTurn
}

// transcriptHTMLTemplate renders a full transcript. Every dynamic value is
// interpolated as template data ({{.}}), so html/template HTML-escapes all
// user, model, and tool content; no field is marked template.HTML.
var transcriptHTMLTemplate = template.Must(template.New("transcript").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 50rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; }
.turn { margin-bottom: 1.5rem; border-left: 3px solid #ccc; padding-left: 1rem; }
.role { font-weight: bold; }
.when { color: #888; font-size: 0.85em; margin-left: 0.5rem; }
pre { background: #f4f4f4; padding: 0.75rem; overflow-x: auto; border-radius: 4px; }
.lang { color: #666; font-size: 0.8em; }
.error pre { background: #fde8e8; }
.thinking { color: #666; font-style: italic; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<p class="meta">Model: {{.Model}} &middot; Agent: {{.Agent}}</p>
{{range .Turns}}
<div class="turn">
<div><span class="role">{{.Role}}</span><span class="when">{{.When}}</span></div>
{{range .Blocks}}
{{if eq .Kind "text"}}<p>{{.Text}}</p>
{{else if eq .Kind "thinking"}}<p class="thinking">{{.Text}}</p>
{{else if eq .Kind "tool_result"}}<div class="{{if .Err}}error{{end}}"><span class="lang">{{.Lang}}</span><pre>{{.Code}}</pre></div>
{{else}}<div><span class="lang">{{.Lang}}</span><pre>{{.Code}}</pre></div>
{{end}}
{{end}}
</div>
{{end}}
</body>
</html>
`))

// ExportHTML renders the session and its messages as a standalone, readable
// HTML transcript. User, assistant, and tool turns are labelled; code and
// tool I/O are preserved in <pre> blocks. All message content is HTML-escaped
// via html/template, so embedded markup such as "<script>" cannot execute.
func ExportHTML(sess *Session, messages []message.Message) (string, error) {
	if sess == nil {
		return "", fmt.Errorf("exporting HTML: session is nil")
	}

	doc := htmlDoc{
		Title: sess.Title,
		Model: sess.Model,
		Agent: sess.Agent,
		Turns: make([]htmlTurn, 0, len(messages)),
	}

	for _, msg := range messages {
		turn := htmlTurn{
			Role:   roleLabel(msg.Role),
			When:   msg.CreatedAt.UTC().Format("2006-01-02 15:04:05 MST"),
			Blocks: make([]htmlBlock, 0, len(msg.Content)),
		}
		for _, block := range msg.Content {
			turn.Blocks = append(turn.Blocks, htmlBlockFor(block))
		}
		doc.Turns = append(doc.Turns, turn)
	}

	var buf strings.Builder
	if err := transcriptHTMLTemplate.Execute(&buf, doc); err != nil {
		return "", fmt.Errorf("rendering HTML transcript: %w", err)
	}
	return buf.String(), nil
}

// htmlBlockFor converts a content block into its template representation.
func htmlBlockFor(block message.ContentBlock) htmlBlock {
	switch b := block.(type) {
	case message.TextBlock:
		return htmlBlock{Kind: "text", Text: b.Text}
	case message.ThinkingBlock:
		return htmlBlock{Kind: "thinking", Text: b.Text}
	case message.ToolUseBlock:
		code := b.Name
		if len(b.Input) > 0 {
			code = b.Name + "(" + string(b.Input) + ")"
		}
		return htmlBlock{Kind: "tool_use", Lang: "tool call: " + b.Name, Code: code}
	case message.ToolResultBlock:
		lang := "tool result"
		if b.IsError {
			lang = "tool error"
		}
		return htmlBlock{Kind: "tool_result", Lang: lang, Code: b.Content, Err: b.IsError}
	case message.ImageBlock:
		return htmlBlock{Kind: "image", Lang: "image", Code: fmt.Sprintf("[image: %s, %d bytes]", b.MimeType, len(b.Data))}
	default:
		return htmlBlock{Kind: "unknown", Lang: "unknown block", Code: fmt.Sprintf("[unsupported block: %s]", block.Type())}
	}
}

// ExportMarkdown renders the session and its messages as a readable Markdown
// transcript. Each turn is a "## Role" heading; tool calls, tool results, and
// image placeholders are emitted as fenced code blocks so code is preserved.
// The output is Markdown (not HTML) and is intentionally not HTML-escaped.
func ExportMarkdown(sess *Session, messages []message.Message) (string, error) {
	if sess == nil {
		return "", fmt.Errorf("exporting Markdown: session is nil")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", sess.Title)
	fmt.Fprintf(&b, "- Model: %s\n- Agent: %s\n\n", sess.Model, sess.Agent)

	for _, msg := range messages {
		when := msg.CreatedAt.UTC().Format("2006-01-02 15:04:05 MST")
		fmt.Fprintf(&b, "## %s\n\n*%s*\n\n", roleLabel(msg.Role), when)
		for _, block := range msg.Content {
			writeMarkdownBlock(&b, block)
		}
	}

	return b.String(), nil
}

// writeMarkdownBlock appends one content block to the Markdown builder.
func writeMarkdownBlock(b *strings.Builder, block message.ContentBlock) {
	switch blk := block.(type) {
	case message.TextBlock:
		fmt.Fprintf(b, "%s\n\n", blk.Text)
	case message.ThinkingBlock:
		fmt.Fprintf(b, "> %s\n\n", strings.ReplaceAll(blk.Text, "\n", "\n> "))
	case message.ToolUseBlock:
		fmt.Fprintf(b, "**Tool call: %s**\n\n```json\n%s\n```\n\n", blk.Name, fencedSafe(string(blk.Input)))
	case message.ToolResultBlock:
		label := "Tool result"
		if blk.IsError {
			label = "Tool error"
		}
		fmt.Fprintf(b, "**%s**\n\n```\n%s\n```\n\n", label, fencedSafe(blk.Content))
	case message.ImageBlock:
		fmt.Fprintf(b, "_[image: %s, %d bytes]_\n\n", blk.MimeType, len(blk.Data))
	default:
		fmt.Fprintf(b, "_[unsupported block: %s]_\n\n", block.Type())
	}
}

// fencedSafe defuses any closing code fence in payload that would otherwise
// terminate the surrounding fenced block prematurely.
func fencedSafe(s string) string {
	return strings.ReplaceAll(s, "```", "ʼʼʼ")
}
