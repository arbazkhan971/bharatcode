package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// forceFallback replaces the lookPath seam so runGoGrep is always used,
// and returns a cleanup function.
func forceFallback(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = old })
}

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

// TestGrepFallbackSkipsBinaryFiles asserts that a file containing NUL bytes is
// never included in Go-fallback results.
func TestGrepFallbackSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	// Write a "text" file that should match.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "source.go"), []byte("package main // target\n"), 0o644))
	// Write a "binary" file that also contains the pattern but has a NUL byte.
	binary := append([]byte("target\x00binary data"), make([]byte, 100)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "binary.bin"), binary, 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"target"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "source.go")
	require.NotContains(t, result.Content, "binary.bin", "binary file must be skipped")
}

// TestGrepFallbackSkipsNodeModulesAndGit asserts that node_modules and .git
// directories are never walked by the Go fallback.
func TestGrepFallbackSkipsNodeModulesAndGit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "some_pkg"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "some_pkg", "index.js"), []byte("findme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "COMMIT_EDITMSG"), []byte("findme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main // no match here\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"findme"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The pattern appears only in ignored directories — expect no matches.
	require.Equal(t, "No matches found.", result.Content)
}

// TestGrepFallbackSkipsVendorDir asserts that vendor/ is also skipped.
func TestGrepFallbackSkipsVendorDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor", "lib", "util.go"), []byte("package lib // vendored\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"vendored"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No matches found.", result.Content, "vendor dir must be skipped")
}

// TestGrepFallbackMatchCapBoundsOutput asserts that results are capped at
// grepMatchCap and that the cap notice is present in the output.
func TestGrepFallbackMatchCapBoundsOutput(t *testing.T) {
	dir := t.TempDir()

	// Write a file with more lines than grepMatchCap.
	var sb strings.Builder
	for i := 0; i < grepMatchCap+50; i++ {
		sb.WriteString("matchline\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"matchline"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "[results capped:", "cap notice must appear")
	// Count the actual match lines returned — must be exactly grepMatchCap.
	lines := strings.Split(strings.TrimSpace(result.Content), "\n")
	matchLines := 0
	for _, l := range lines {
		if strings.Contains(l, "matchline") {
			matchLines++
		}
	}
	require.Equal(t, grepMatchCap, matchLines, "must return exactly grepMatchCap match lines")
}

// TestGrepSmartCaseLowercaseIsInsensitive checks that a fully-lowercase pattern
// matches text that differs in case (smart-case fallback behaviour).
func TestGrepSmartCaseLowercaseIsInsensitive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("func MyFunction() {}\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	// Lowercase pattern "myfunction" must match "MyFunction".
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"myfunction"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "app.go", "lowercase pattern must match mixed-case text")
}

// TestGrepSmartCaseMixedIsExact checks that a mixed-case pattern does NOT
// match text that only differs in case.
func TestGrepSmartCaseMixedIsExact(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("func myfunction() {}\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	// Mixed-case pattern "MyFunction" must NOT match lowercase "myfunction".
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"MyFunction"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No matches found.", result.Content, "mixed-case pattern must be exact")
}

// TestGrepFallbackGitignoreRespected asserts that a directory listed in
// .gitignore at the workspace root is skipped by the Go fallback.
func TestGrepFallbackGitignoreRespected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "output.txt"), []byte("findme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# ignore build artefacts\nbuild/\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"findme"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No matches found.", result.Content, ".gitignore dir must be skipped")
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
