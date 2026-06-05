package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeTypeFixture lays down a small mixed-language tree used by the type-filter
// tests and returns its root.
func writeTypeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"main.go":      "package main\n// needle here\n",
		"util_test.go": "package main\n// needle in test\n",
		"app.py":       "# needle in python\n",
		"app.js":       "// needle in js\n",
		"styles.css":   "/* needle in css */\n",
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

// TestGrepTypeFilterFallback asserts the Go fallback honours `type` by selecting
// only files whose extension belongs to the requested language.
func TestGrepTypeFilterFallback(t *testing.T) {
	dir := writeTypeFixture(t)
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"needle","type":"go"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "main.go")
	require.Contains(t, res.Content, "util_test.go")
	require.NotContains(t, res.Content, "app.py")
	require.NotContains(t, res.Content, "app.js")
	require.NotContains(t, res.Content, "styles.css")
}

// TestGrepTypeAliasFallback asserts language aliases (python→py) resolve.
func TestGrepTypeAliasFallback(t *testing.T) {
	dir := writeTypeFixture(t)
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"needle","type":"python"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "app.py")
	require.NotContains(t, res.Content, "main.go")
}

// TestGrepTypeAndIncludeAreANDed asserts that type and include both constrain
// the result (a file must satisfy both filters).
func TestGrepTypeAndIncludeAreANDed(t *testing.T) {
	dir := writeTypeFixture(t)
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"needle","type":"go","include":"*_test.go"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Only the .go file that also matches the *_test.go glob survives.
	require.Contains(t, res.Content, "util_test.go")
	require.NotContains(t, res.Content, "main.go")
	require.NotContains(t, res.Content, "app.py")
}

// TestGrepUnknownTypeErrors asserts an unrecognised type name is rejected with a
// helpful message listing supported names.
func TestGrepUnknownTypeErrors(t *testing.T) {
	dir := writeTypeFixture(t)
	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"needle","type":"cobol"}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown type")
	require.Contains(t, res.Content, "go") // supported-types list mentions a real type
}

// TestGrepTypeFilterMultiline asserts the type filter applies on the multiline
// fallback path too.
func TestGrepTypeFilterMultiline(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc a() {}\nfunc b() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"),
		[]byte("def a():\n    pass\n"), 0o644))
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	// A dotall pattern that spans two lines; only the .go file should match.
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"func a.*func b","multiline":true,"type":"go"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "main.go")
	require.NotContains(t, res.Content, "app.py")
}

// TestGrepTypeParityRgVsFallback asserts that, when ripgrep is installed, the rg
// path and the Go fallback select the same set of files for a `type` query — the
// synthetic --type-add must mirror the shared extension table exactly.
func TestGrepTypeParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := writeTypeFixture(t)

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"needle","type":"go","output_mode":"files_with_matches"}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgFiles := filesFromOutput(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goFiles := filesFromOutput(run()) // forced Go fallback

	require.Equal(t, goFiles, rgFiles, "rg and Go fallback must select identical files for type=go")
	require.Equal(t, []string{"main.go", "util_test.go"}, goFiles)
}

// TestGrepCaseInsensitiveParityRgVsFallback asserts the rg path and the Go
// fallback agree when case_insensitive forces a mixed-case pattern to match
// differing case: rg's --ignore-case must override its --smart-case the same
// way the fallback's compileSmartCase override does.
func TestGrepCaseInsensitiveParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("var httpClient int\n"), 0o644))

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"HTTP","case_insensitive":true,"output_mode":"files_with_matches"}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgFiles := filesFromOutput(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goFiles := filesFromOutput(run()) // forced Go fallback

	require.Equal(t, goFiles, rgFiles, "rg and Go fallback must agree under case_insensitive")
	require.Equal(t, []string{"a.go"}, goFiles)
}

// filesFromOutput parses files_with_matches output into a sorted base-name list.
func filesFromOutput(out string) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		names = append(names, filepath.Base(line))
	}
	sort.Strings(names)
	return names
}
