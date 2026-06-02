package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type webFetchTool struct {
	client *http.Client
}

type webFetchArgs struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt,omitempty"`
}

var (
	httpClient     = &http.Client{Timeout: 15 * time.Second}
	schemaWebFetch = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["url"],
  "properties": {
    "url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
    "prompt": {"type": "string", "description": "Optional note describing what information is needed from the page."}
  }
}`)
)

//go:embed web_fetch.md
var webFetchDescription string

func newWebFetchTool(Dependencies) Tool {
	return &webFetchTool{client: httpClient}
}

func (t *webFetchTool) Name() string {
	return "web_fetch"
}

func (t *webFetchTool) Description() string {
	return webFetchDescription
}

func (t *webFetchTool) Schema() json.RawMessage {
	return schemaWebFetch
}

func (t *webFetchTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args webFetchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid web_fetch arguments: " + err.Error()), nil
	}
	args.URL = strings.TrimSpace(args.URL)
	if args.URL == "" {
		return errorResult("url is required"), nil
	}
	parsed, err := url.Parse(args.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errorResult("url must be an absolute HTTP or HTTPS URL"), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errorResult("url must use http or https"), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("creating web request: %w", err)
	}
	req.Header.Set("User-Agent", "BharatCode/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetching url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorResult(fmt.Sprintf("request failed with status %s", resp.Status)), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return Result{}, fmt.Errorf("reading response body: %w", err)
	}
	truncated := len(body) > 2*1024*1024
	if truncated {
		body = body[:2*1024*1024]
	}

	contentType := resp.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(text, "<") {
		text = htmlToMarkdown(text)
	}
	if args.Prompt != "" {
		text = "Fetch note: " + args.Prompt + "\n\n" + text
	}
	if truncated {
		text += "\n\n[truncated response body]"
	}
	return Result{Content: strings.TrimSpace(text)}, nil
}

func htmlToMarkdown(input string) string {
	s := regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`).ReplaceAllString(input, " ")
	s = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?is)<!--.*?-->`).ReplaceAllString(s, " ")
	for i := 6; i >= 1; i-- {
		re := regexp.MustCompile(fmt.Sprintf(`(?is)<h%d\b[^>]*>(.*?)</h%d>`, i, i))
		prefix := strings.Repeat("#", i)
		s = re.ReplaceAllString(s, "\n\n"+prefix+" $1\n\n")
	}
	s = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`).ReplaceAllString(s, "$2 ($1)")
	s = regexp.MustCompile(`(?is)<li\b[^>]*>`).ReplaceAllString(s, "\n- ")
	s = regexp.MustCompile(`(?is)</p>|<br\s*/?>|</div>|</section>|</article>|</tr>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
