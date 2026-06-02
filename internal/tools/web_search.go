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

type webSearchTool struct {
	client *http.Client
}

type webSearchArgs struct {
	Query string `json:"query"`
}

var (
	webSearchEndpoint = "https://html.duckduckgo.com/html/"
	webSearchClient   = &http.Client{Timeout: 15 * time.Second}
	schemaWebSearch   = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["query"],
  "properties": {
    "query": {"type": "string", "description": "Search query to send to the web provider."}
  }
}`)
)

//go:embed web_search.md
var webSearchDescription string

func newWebSearchTool(Dependencies) Tool {
	return &webSearchTool{client: webSearchClient}
}

func (t *webSearchTool) Name() string {
	return "web_search"
}

func (t *webSearchTool) Description() string {
	return webSearchDescription
}

func (t *webSearchTool) Schema() json.RawMessage {
	return schemaWebSearch
}

func (t *webSearchTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args webSearchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid web_search arguments: " + err.Error()), nil
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return errorResult("query is required"), nil
	}

	endpoint, err := url.Parse(webSearchEndpoint)
	if err != nil {
		return Result{}, fmt.Errorf("parsing search endpoint: %w", err)
	}
	q := endpoint.Query()
	q.Set("q", args.Query)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("User-Agent", "BharatCode/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("running web search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorResult(fmt.Sprintf("search failed with status %s", resp.Status)), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return Result{}, fmt.Errorf("reading search response: %w", err)
	}

	results := parseSearchResults(string(body))
	if len(results) == 0 {
		return Result{Content: "No search results found."}, nil
	}
	var b strings.Builder
	for i, result := range results {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, result.Title, result.URL, result.Snippet)
	}
	return Result{Content: strings.TrimSpace(b.String())}, nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func parseSearchResults(body string) []searchResult {
	blockRE := regexp.MustCompile(`(?is)<div[^>]+class=["'][^"']*result[^"']*["'][^>]*>(.*?)</div>\s*</div>`)
	linkRE := regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*result__a[^"']*["'][^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	snippetRE := regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*result__snippet[^"']*["'][^>]*>(.*?)</a>|<div[^>]+class=["'][^"']*result__snippet[^"']*["'][^>]*>(.*?)</div>`)
	blocks := blockRE.FindAllStringSubmatch(body, -1)
	if len(blocks) == 0 {
		blocks = [][]string{{"", body}}
	}

	var results []searchResult
	for _, block := range blocks {
		part := block[1]
		link := linkRE.FindStringSubmatch(part)
		if len(link) < 3 {
			continue
		}
		snippet := ""
		if match := snippetRE.FindStringSubmatch(part); len(match) > 0 {
			for _, group := range match[1:] {
				if group != "" {
					snippet = cleanSearchText(group)
					break
				}
			}
		}
		if snippet == "" {
			if match := snippetRE.FindStringSubmatch(body); len(match) > 0 {
				for _, group := range match[1:] {
					if group != "" {
						snippet = cleanSearchText(group)
						break
					}
				}
			}
		}
		results = append(results, searchResult{
			Title:   cleanSearchText(link[2]),
			URL:     cleanSearchURL(html.UnescapeString(link[1])),
			Snippet: snippet,
		})
	}
	return results
}

func cleanSearchText(value string) string {
	value = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func cleanSearchURL(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Host == "duckduckgo.com" {
		if target := parsed.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return value
}
