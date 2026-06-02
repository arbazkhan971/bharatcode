package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestGrepFallbackFindsContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func main","include":"*.go"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "main.go:2:func main")
}

func TestGlobMatchesRecursiveGoFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "x.go"), []byte("package pkg\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o644))

	tool := newGlobTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"**/*.go"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "pkg/x.go", result.Content)
}

func TestLSHonorsGitignore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644))

	tool := newLSTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "app.go")
	require.NotContains(t, result.Content, "node_modules")
}

func TestTodoRoundTripSameBus(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ToolCallPayload]("todo_test", 8)
	defer bus.Close()

	first := newTodoTool(Dependencies{Bus: bus})
	_, err := first.Run(context.Background(), json.RawMessage(`{"action":"add","text":"ship tools"}`))
	require.NoError(t, err)

	second := newTodoTool(Dependencies{Bus: bus})
	result, err := second.Run(context.Background(), json.RawMessage(`{"action":"list"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "ship tools")
}

type fakeDiagnostics struct {
	items []lsp.Diagnostic
}

func (f fakeDiagnostics) Diagnostics(context.Context, string) ([]lsp.Diagnostic, error) {
	return f.items, nil
}

func TestDiagnosticsUsesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{{
			Path: path,
			Range: lsp.Range{Start: lsp.Position{
				Line:      0,
				Character: 7,
			}},
			Severity: lsp.Error,
			Message:  "expected identifier",
			Source:   "fake",
		}}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "main.go:1:8: error: expected identifier")
}

func TestWebFetchStripsScriptsAndKeepsLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><style>.x{}</style><script>alert(1)</script></head><body><h1>Title</h1><a href="https://example.com">Link</a></body></html>`))
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	t.Cleanup(func() { httpClient = oldClient })

	tool := newWebFetchTool(Dependencies{})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"url": server.URL}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "# Title")
	require.Contains(t, result.Content, "Link (https://example.com)")
	require.NotContains(t, result.Content, "alert")
}

func TestWebSearchParsesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<div class="result"><a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com">Example</a><div class="result__snippet">Snippet text</div></div></div>`))
	}))
	defer server.Close()

	oldEndpoint := webSearchEndpoint
	oldClient := webSearchClient
	webSearchEndpoint = server.URL
	webSearchClient = server.Client()
	t.Cleanup(func() {
		webSearchEndpoint = oldEndpoint
		webSearchClient = oldClient
	})

	tool := newWebSearchTool(Dependencies{})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"query":"example"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "Example")
	require.Contains(t, result.Content, "https://example.com")
	require.Contains(t, result.Content, "Snippet text")
}
