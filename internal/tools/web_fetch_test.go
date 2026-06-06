package tools

import (
	"context"
	"encoding/json"
	"net"
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

func TestWebFetchJSONNotMangledByMarkdown(t *testing.T) {
	// A JSON body whose string values contain '<' must be returned verbatim,
	// not run through the HTML-to-markdown tag stripper, which would delete the
	// "<3" and "a < b" fragments.
	const payload = `{"emoji":"<3","note":"a < b && c > d","tag":"<span>"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
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
	if res.Content != payload {
		t.Fatalf("JSON body was altered.\nwant: %s\ngot:  %s", payload, res.Content)
	}
}

func TestWebFetchSourceCodeNotMangled(t *testing.T) {
	// Raw source served as text/plain with angle brackets (generics, channel
	// directions, redirects) must survive untouched.
	const src = "func f[T any](ch chan<- T, xs []T) { if len(xs) < 1 { return } }"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(src))
	}))
	defer srv.Close()

	tool := newWebFetchTool(Dependencies{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Content != src {
		t.Fatalf("source was altered.\nwant: %s\ngot:  %s", src, res.Content)
	}
}

func TestWebFetchUnlabeledHTMLStillRendered(t *testing.T) {
	// When the server omits a Content-Type, a body with real HTML structure is
	// still sniffed and reduced to markdown.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = w.Write([]byte("<html><body><h1>Title</h1><pre>code here</pre></body></html>"))
	}))
	defer srv.Close()

	tool := newWebFetchTool(Dependencies{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Contains(res.Content, "<h1>") || !strings.Contains(res.Content, "code here") {
		t.Fatalf("unlabeled HTML was not rendered to markdown.\ngot:\n%s", res.Content)
	}
}

func TestShouldRenderHTML(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{"explicit html", "text/html; charset=utf-8", "<html><body>x</body></html>", true},
		{"xhtml", "application/xhtml+xml", "<html/>", true},
		{"json with angle bracket", "application/json", `{"x":"a < b"}`, false},
		{"javascript with comparison", "text/javascript", "if (a<b) {}", false},
		{"css", "text/css", "a{}", false},
		{"xml is not html", "application/xml", "<note>hi</note>", false},
		{"plain text sniffs html structure", "text/plain", "<div>real page</div>", true},
		{"plain text without html markers", "text/plain", "a < b and c > d", false},
		{"missing type sniffs html", "", "<!DOCTYPE html><html></html>", true},
		{"missing type non-html", "", `{"k":"<v>"}`, false},
		{"octet-stream sniffs html", "application/octet-stream", "<body><p>p</p></body>", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRenderHTML(tc.contentType, tc.body); got != tc.want {
				t.Fatalf("shouldRenderHTML(%q, %q) = %v, want %v", tc.contentType, tc.body, got, tc.want)
			}
		})
	}
}

func TestIsBlockedFetchIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		// Cloud-metadata endpoint (link-local) — the headline SSRF target.
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		// Private networks.
		{"10.0.0.1", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"fd00::1", true}, // IPv6 unique-local (fc00::/7)
		// Other non-public ranges.
		{"0.0.0.0", true},
		{"::", true},
		{"224.0.0.1", true}, // multicast
		{"fe80::1", true},   // IPv6 link-local
		// Allowed: loopback (localhost dev servers) and public addresses.
		{"127.0.0.1", false},
		{"::1", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("test bug: unparseable IP %q", c.ip)
		}
		if got := isBlockedFetchIP(ip); got != c.blocked {
			t.Errorf("isBlockedFetchIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

func TestWebFetchBlocksMetadataEndpoint(t *testing.T) {
	// The model must not be able to point web_fetch at the cloud-metadata IP.
	// The guard rejects the dial before any TCP connection, so this is fast.
	tool := newWebFetchTool(Dependencies{})
	args, _ := json.Marshal(map[string]string{"url": "http://169.254.169.254/latest/meta-data/"})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result for the metadata endpoint, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "non-public") {
		t.Fatalf("error message should explain the block.\ngot:\n%s", res.Content)
	}
}

func TestWebFetchBlocksRedirectToPrivate(t *testing.T) {
	// A public-looking page that redirects into a blocked range must still be
	// refused: the Control hook runs on every dial, including the redirect's.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/internal", http.StatusFound)
	}))
	defer srv.Close()

	tool := newWebFetchTool(Dependencies{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected redirect into a private range to be refused, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "non-public") {
		t.Fatalf("error message should explain the block.\ngot:\n%s", res.Content)
	}
}
