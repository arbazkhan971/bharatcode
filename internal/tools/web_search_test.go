package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilterSearchResultsAllowedRestrictsToHost(t *testing.T) {
	results := []searchResult{
		{Title: "a", URL: "https://pkg.go.dev/fmt"},
		{Title: "b", URL: "https://stackoverflow.com/q/1"},
		{Title: "c", URL: "https://go.dev/blog"},
	}
	got := filterSearchResults(results, []string{"go.dev"}, nil)
	// pkg.go.dev is a subdomain of go.dev, go.dev matches exactly,
	// stackoverflow.com is excluded.
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if strings.Contains(r.URL, "stackoverflow") {
			t.Fatalf("blocked host leaked through allow list: %s", r.URL)
		}
	}
}

func TestFilterSearchResultsBlockedWinsOverAllowed(t *testing.T) {
	results := []searchResult{
		{Title: "good", URL: "https://docs.go.dev/x"},
		{Title: "bad", URL: "https://blog.go.dev/spam"},
	}
	got := filterSearchResults(results, []string{"go.dev"}, []string{"blog.go.dev"})
	if len(got) != 1 || got[0].Title != "good" {
		t.Fatalf("blocked subdomain should be dropped even when allowed: %+v", got)
	}
}

func TestFilterSearchResultsNormalizesDomainInput(t *testing.T) {
	results := []searchResult{{Title: "a", URL: "https://www.example.com/page"}}
	// Scheme, leading www., and trailing path on the filter entry must all be
	// tolerated and still match the www.example.com host.
	got := filterSearchResults(results, []string{"https://www.example.com/"}, nil)
	if len(got) != 1 {
		t.Fatalf("normalized domain should match: %+v", got)
	}
}

func TestFilterSearchResultsEmptyListsReturnInput(t *testing.T) {
	results := []searchResult{{Title: "a", URL: "https://example.com"}}
	got := filterSearchResults(results, nil, nil)
	if len(got) != 1 {
		t.Fatalf("empty allow/block lists must pass everything through: %+v", got)
	}
}

func TestFilterSearchResultsAllowDropsUnparseableHost(t *testing.T) {
	results := []searchResult{
		{Title: "ok", URL: "https://go.dev/x"},
		{Title: "bare", URL: "not a url"},
	}
	got := filterSearchResults(results, []string{"go.dev"}, nil)
	// A result with no parseable host cannot be verified against an allow list,
	// so it is excluded.
	if len(got) != 1 || got[0].Title != "ok" {
		t.Fatalf("unverifiable host should be excluded under an allow list: %+v", got)
	}
}

func TestHostInDomainsRejectsSuffixSpoof(t *testing.T) {
	// "notgo.dev" must not match the domain "go.dev" — only exact host or a
	// dot-bounded subdomain qualifies.
	if hostInDomains("notgo.dev", []string{"go.dev"}) {
		t.Fatal("suffix spoof matched the domain")
	}
	if !hostInDomains("go.dev", []string{"go.dev"}) {
		t.Fatal("exact host should match")
	}
	if !hostInDomains("api.go.dev", []string{"go.dev"}) {
		t.Fatal("subdomain should match")
	}
}

func TestWebSearchRunAppliesDomainFilter(t *testing.T) {
	page := `
<div class="result results_links">
  <a class="result__a" href="https://pkg.go.dev/fmt">fmt package</a>
  <div class="result__snippet">format strings</div>
</div>
<div class="result results_links">
  <a class="result__a" href="https://example.com/other">unrelated</a>
  <div class="result__snippet">noise</div>
</div>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	prevEndpoint := webSearchEndpoint
	webSearchEndpoint = srv.URL
	defer func() { webSearchEndpoint = prevEndpoint }()

	tool := &webSearchTool{client: srv.Client()}
	raw, _ := json.Marshal(webSearchArgs{Query: "fmt", AllowedDomains: []string{"go.dev"}})
	res, err := tool.Run(context.Background(), raw)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(res.Content, "pkg.go.dev/fmt") {
		t.Fatalf("allowed result missing from output:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "example.com/other") {
		t.Fatalf("filtered result leaked into output:\n%s", res.Content)
	}
}
