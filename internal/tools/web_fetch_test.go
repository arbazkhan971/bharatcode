package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTMLToMarkdownPreservesPreBlock(t *testing.T) {
	// The indentation and line breaks inside <pre> are exactly what a coding
	// agent needs; they must survive the whitespace-collapsing pass.
	in := "<h1>Example</h1>\n<pre>func main() {\n    fmt.Println(\"hi\")\n}</pre>\n<p>done</p>"
	got := htmlToMarkdown(in)

	want := "```\nfunc main() {\n    fmt.Println(\"hi\")\n}\n```"
	if !strings.Contains(got, want) {
		t.Fatalf("pre block not preserved as fenced code.\ngot:\n%s", got)
	}
	if !strings.Contains(got, "# Example") {
		t.Fatalf("heading lost.\ngot:\n%s", got)
	}
	if !strings.Contains(got, "done") {
		t.Fatalf("trailing text lost.\ngot:\n%s", got)
	}
}

func TestHTMLToMarkdownPreCodeAndEntities(t *testing.T) {
	// <pre><code> is the canonical doc markup; entities inside it must decode
	// and the indentation must be preserved verbatim (not field-collapsed).
	in := "<pre><code>if a &lt; b {\n        return &amp;x\n}</code></pre>"
	got := htmlToMarkdown(in)

	want := "```\nif a < b {\n        return &x\n}\n```"
	if !strings.Contains(got, want) {
		t.Fatalf("pre/code with entities not preserved.\ngot:\n%q", got)
	}
}

func TestHTMLToMarkdownMultiplePreBlocks(t *testing.T) {
	in := "<pre>one\n  two</pre><p>middle</p><pre>three\n  four</pre>"
	got := htmlToMarkdown(in)

	if !strings.Contains(got, "```\none\n  two\n```") {
		t.Fatalf("first block lost.\ngot:\n%q", got)
	}
	if !strings.Contains(got, "```\nthree\n  four\n```") {
		t.Fatalf("second block lost.\ngot:\n%q", got)
	}
	if !strings.Contains(got, "middle") {
		t.Fatalf("text between blocks lost.\ngot:\n%q", got)
	}
}

func TestHTMLToMarkdownInlineCode(t *testing.T) {
	got := htmlToMarkdown("<p>Call <code>fmt.Println</code> to print.</p>")
	if !strings.Contains(got, "`fmt.Println`") {
		t.Fatalf("inline code not backtick-wrapped.\ngot:\n%q", got)
	}
}

func TestHTMLToMarkdownStripsScriptAndStyle(t *testing.T) {
	in := "<style>.x{color:red}</style><script>var x=1;</script><p>visible</p>"
	got := htmlToMarkdown(in)
	if strings.Contains(got, "color:red") || strings.Contains(got, "var x") {
		t.Fatalf("script/style content leaked.\ngot:\n%q", got)
	}
	if !strings.Contains(got, "visible") {
		t.Fatalf("visible text lost.\ngot:\n%q", got)
	}
}

func TestWebFetchRunReturnsCodeBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h1>Docs</h1><pre>go build ./...\ngo test ./...</pre>"))
	}))
	defer srv.Close()

	tool := newWebFetchTool(Dependencies{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "```\ngo build ./...\ngo test ./...\n```") {
		t.Fatalf("fetched code block not preserved.\ngot:\n%s", res.Content)
	}
}
