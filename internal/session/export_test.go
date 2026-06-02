package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// sampleTranscript builds a small session with a user prompt, an assistant
// reply containing a tool call, a tool result, and a final assistant message.
// The user prompt embeds a raw <script> tag to exercise HTML escaping.
func sampleTranscript() (*Session, []message.Message) {
	sess := &Session{
		ID:    "sess-1",
		Title: "Export demo",
		Model: "deepseek-chat",
		Agent: "coder",
	}

	when := time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC)

	messages := []message.Message{
		{
			ID:        "m1",
			SessionID: "sess-1",
			Role:      message.RoleUser,
			CreatedAt: when,
			Content: []message.ContentBlock{
				message.TextBlock{Text: "Please run <script>alert('xss')</script> & list files."},
			},
		},
		{
			ID:        "m2",
			SessionID: "sess-1",
			Role:      message.RoleAssistant,
			CreatedAt: when.Add(time.Second),
			Content: []message.ContentBlock{
				message.TextBlock{Text: "Sure, listing the directory now."},
				message.ToolUseBlock{
					ID:    "call-1",
					Name:  "list_dir",
					Input: json.RawMessage(`{"path":"/tmp"}`),
				},
			},
		},
		{
			ID:        "m3",
			SessionID: "sess-1",
			Role:      message.RoleTool,
			CreatedAt: when.Add(2 * time.Second),
			Content: []message.ContentBlock{
				message.ToolResultBlock{
					ToolUseID: "call-1",
					Content:   "main.go\nREADME.md",
					IsError:   false,
				},
			},
		},
		{
			ID:        "m4",
			SessionID: "sess-1",
			Role:      message.RoleAssistant,
			CreatedAt: when.Add(3 * time.Second),
			Content: []message.ContentBlock{
				message.TextBlock{Text: "Found two files: main.go and README.md."},
			},
		},
	}
	return sess, messages
}

func TestExportHTML_ContainsContentAndLabels(t *testing.T) {
	sess, messages := sampleTranscript()

	out, err := ExportHTML(sess, messages)
	if err != nil {
		t.Fatalf("ExportHTML returned error: %v", err)
	}

	wantSubstrings := []string{
		"Export demo",                      // session title
		"deepseek-chat",                    // model
		"User",                             // role label
		"Assistant",                        // role label
		"Tool",                             // role label
		"Sure, listing the directory now.", // assistant text
		"list_dir",                         // tool name
		"main.go",                          // tool result content
		"Found two files: main.go and README.md.", // final assistant text
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("HTML output missing expected substring %q", want)
		}
	}
}

func TestExportHTML_EscapesContent(t *testing.T) {
	sess, messages := sampleTranscript()

	out, err := ExportHTML(sess, messages)
	if err != nil {
		t.Fatalf("ExportHTML returned error: %v", err)
	}

	// The escaped form must be present.
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("HTML output does not contain escaped <script> tag; got:\n%s", out)
	}

	// And critically, no raw <script> tag may leak. We never emit a literal
	// <script> tag in the template, so any occurrence would be unescaped
	// user content -- a real XSS escape failure.
	if strings.Contains(out, "<script>") {
		t.Errorf("HTML output contains UNESCAPED <script> tag (XSS): escaping failed")
	}

	// The raw ampersand from the user text must also be escaped.
	if strings.Contains(out, "</script>") {
		t.Errorf("HTML output contains UNESCAPED </script> tag (XSS): escaping failed")
	}
}

func TestExportHTML_NilSession(t *testing.T) {
	if _, err := ExportHTML(nil, nil); err == nil {
		t.Fatal("ExportHTML(nil, nil) expected error, got nil")
	}
}

func TestExportMarkdown_ContainsContentAndLabels(t *testing.T) {
	sess, messages := sampleTranscript()

	out, err := ExportMarkdown(sess, messages)
	if err != nil {
		t.Fatalf("ExportMarkdown returned error: %v", err)
	}

	wantSubstrings := []string{
		"# Export demo",                    // title heading
		"deepseek-chat",                    // model
		"## User",                          // role heading
		"## Assistant",                     // role heading
		"## Tool",                          // role heading
		"Sure, listing the directory now.", // assistant text
		"**Tool call: list_dir**",          // tool call label
		"```json",                          // fenced code block for tool input
		`{"path":"/tmp"}`,                  // tool input content
		"**Tool result**",                  // tool result label
		"main.go",                          // tool result content
		"Found two files: main.go and README.md.", // final assistant text
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown output missing expected substring %q", want)
		}
	}
}

func TestExportMarkdown_NotHTMLEscaped(t *testing.T) {
	sess, messages := sampleTranscript()

	out, err := ExportMarkdown(sess, messages)
	if err != nil {
		t.Fatalf("ExportMarkdown returned error: %v", err)
	}

	// Markdown is not HTML, so the literal user text (including the raw
	// <script> tag and ampersand) must be preserved verbatim, NOT escaped.
	if !strings.Contains(out, "<script>alert('xss')</script>") {
		t.Errorf("Markdown output should preserve raw text verbatim, got:\n%s", out)
	}
	if strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("Markdown output should NOT be HTML-escaped, but found escaped entity")
	}
}

func TestExportMarkdown_ToolErrorLabel(t *testing.T) {
	sess := &Session{Title: "Err demo", Model: "kimi-k2", Agent: "coder"}
	messages := []message.Message{
		{
			Role:      message.RoleTool,
			CreatedAt: time.Unix(0, 0).UTC(),
			Content: []message.ContentBlock{
				message.ToolResultBlock{ToolUseID: "c", Content: "boom", IsError: true},
			},
		},
	}

	out, err := ExportMarkdown(sess, messages)
	if err != nil {
		t.Fatalf("ExportMarkdown returned error: %v", err)
	}
	if !strings.Contains(out, "**Tool error**") {
		t.Errorf("expected tool error label in output, got:\n%s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("expected tool error content in output, got:\n%s", out)
	}
}

func TestFencedSafe_DefusesClosingFence(t *testing.T) {
	got := fencedSafe("text ``` more")
	if strings.Contains(got, "```") {
		t.Errorf("fencedSafe left a raw closing fence: %q", got)
	}
}
