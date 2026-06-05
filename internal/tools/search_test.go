package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestGrepCaseInsensitiveForcesInsensitive checks that case_insensitive:true
// overrides smart-case so a mixed-case pattern still matches differing case.
func TestGrepCaseInsensitiveForcesInsensitive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("var url = httpClient\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})

	// Without the flag, the mixed-case pattern "HTTP" is exact and finds nothing.
	exact, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"HTTP"}`))
	require.NoError(t, err)
	require.False(t, exact.IsError)
	require.Equal(t, "No matches found.", exact.Content, "mixed-case pattern is exact by default")

	// With case_insensitive:true the same pattern matches "http".
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"HTTP","case_insensitive":true}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "app.go", "case_insensitive must override smart-case")
}

// TestGrepCaseInsensitiveMultiline checks that case_insensitive:true also forces
// insensitivity on the multiline path.
func TestGrepCaseInsensitiveMultiline(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("alpha\nBETA\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	// Mixed-case "Alpha.*beta" spanning a newline only matches case-insensitively.
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"Alpha.*beta","multiline":true,"case_insensitive":true}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "doc.txt", "case_insensitive must apply on the multiline path")
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

// TestGrepContextLinesSymmetric verifies that context=N returns N lines before
// and after each match, with match lines formatted as "path:line:text" and
// context lines as "path-line-text" (rg --no-heading compatible).
func TestGrepContextLinesSymmetric(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"line one",
		"line two",
		"MATCH HERE",
		"line four",
		"line five",
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "MATCH HERE",
		"context": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// Context line before match (line 2)
	require.Contains(t, result.Content, "file.txt-2-line two", "expected context line before match")
	// The match line itself (line 3)
	require.Contains(t, result.Content, "file.txt:3:MATCH HERE", "expected match line")
	// Context line after match (line 4)
	require.Contains(t, result.Content, "file.txt-4-line four", "expected context line after match")

	// Lines outside the context window must not appear.
	require.NotContains(t, result.Content, "line one", "line 1 is outside context window")
	require.NotContains(t, result.Content, "line five", "line 5 is outside context window")
}

// TestGrepContextLinesBeforeAfter verifies that separate before/after fields
// work independently and asymmetrically.
func TestGrepContextLinesBeforeAfter(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"alpha",
		"beta",
		"FIND",
		"gamma",
		"delta",
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "words.txt"), []byte(content), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "FIND",
		"before":  2,
		"after":   0,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// Two lines before the match.
	require.Contains(t, result.Content, "words.txt-1-alpha")
	require.Contains(t, result.Content, "words.txt-2-beta")
	// The match itself.
	require.Contains(t, result.Content, "words.txt:3:FIND")
	// Nothing after the match.
	require.NotContains(t, result.Content, "gamma")
	require.NotContains(t, result.Content, "delta")
}

// TestGrepContextOverridesBeforeAfter checks that context=N takes precedence
// over before/after when all three are set.
func TestGrepContextOverridesBeforeAfter(t *testing.T) {
	dir := t.TempDir()
	content := "a\nb\nTARGET\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	// context=1 overrides before=0, after=0 — should still show 1 line each side.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "TARGET",
		"context": 1,
		"before":  0,
		"after":   0,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "f.txt-2-b", "context=1 should show line before")
	require.Contains(t, result.Content, "f.txt:3:TARGET")
	require.Contains(t, result.Content, "f.txt-4-d", "context=1 should show line after")
}

// TestGrepContextSeparatorBetweenGroups checks that non-adjacent context windows
// are separated by "--" just like rg output.
func TestGrepContextSeparatorBetweenGroups(t *testing.T) {
	dir := t.TempDir()
	// Two matches far enough apart that their context windows don't touch.
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		if i == 2 || i == 9 {
			sb.WriteString("MATCH\n")
		} else {
			sb.WriteString(fmt.Sprintf("line%d\n", i))
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "g.txt"), []byte(sb.String()), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "MATCH",
		"context": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Both matches present.
	require.Contains(t, result.Content, "g.txt:2:MATCH")
	require.Contains(t, result.Content, "g.txt:9:MATCH")
	// Group separator between them.
	require.Contains(t, result.Content, "\n--\n", "non-adjacent groups must be separated by --")
}

// TestGrepContextNoContextNoChange verifies that when context/before/after are
// all zero the output is identical to the baseline (no separator lines injected).
func TestGrepContextNoContextNoChange(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.go"), []byte("package main\nfunc Foo() {}\n"), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "func Foo",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "plain.go:2:func Foo() {}", result.Content)
	require.NotContains(t, result.Content, "--")
}

// TestGrepContextMergesOverlappingWindows verifies that overlapping context
// windows from adjacent matches are merged into a single group (no spurious
// separator inserted).
func TestGrepContextMergesOverlappingWindows(t *testing.T) {
	dir := t.TempDir()
	// Matches on lines 2 and 4 with context=2 — windows overlap.
	content := "a\nMATCH\nc\nMATCH\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "overlap.txt"), []byte(content), 0o644))

	forceFallback(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"pattern": "MATCH",
		"context": 2,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Should be one merged group — no separator.
	require.NotContains(t, result.Content, "\n--\n", "overlapping windows must merge into one group")
	require.Contains(t, result.Content, "overlap.txt:2:MATCH")
	require.Contains(t, result.Content, "overlap.txt:4:MATCH")
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

func TestGlobHonorsGitignore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "dep"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "dep", "vendored.js"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("y\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.js"), []byte("z\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\nbuild.js\n"), 0o644))

	tool := newGlobTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"**/*.js"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// Only the non-ignored file survives; the ignored directory and the
	// ignored file are both pruned.
	require.Equal(t, "src/app.js", result.Content)
	require.NotContains(t, result.Content, "node_modules")
	require.NotContains(t, result.Content, "build.js")
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

func TestDiagnosticsRendersCodeAndSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.rs")
	require.NoError(t, os.WriteFile(path, []byte("fn main() {}\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{{
			Path:     path,
			Range:    lsp.Range{Start: lsp.Position{Line: 0, Character: 3}},
			Severity: lsp.Error,
			Message:  "cannot find value x",
			Source:   "rustc",
			Code:     "E0425",
		}}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.rs"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "main.rs:1:4: error: cannot find value x [E0425] (rustc)")
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
