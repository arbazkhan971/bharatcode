package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// multilineSource is a function whose signature spans several lines so that a
// cross-line pattern only matches in multiline mode.
const multilineSource = "package main\n\nfunc foo(\n\ta int,\n\tb int,\n) int {\n\treturn a + b\n}\n"

func writeGrepFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// TestGrepMultilineFallbackSpansLines asserts the Go fallback matches a pattern
// that crosses newlines and prints every touched line as path:line:content.
func TestGrepMultilineFallbackSpansLines(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "main.go", multilineSource)
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func foo\\(.*?\\) int","multiline":true}`))
	require.NoError(t, err)
	require.False(t, result.IsError)

	for _, want := range []string{
		"main.go:3:func foo(",
		"main.go:4:\ta int,",
		"main.go:5:\tb int,",
		"main.go:6:) int {",
	} {
		require.Contains(t, result.Content, want)
	}
	// The line after the match must not be printed.
	require.NotContains(t, result.Content, "main.go:7:")
}

// TestGrepMultilineDisabledDoesNotCrossLines asserts that without the flag the
// same cross-line pattern finds nothing (single-line semantics preserved).
func TestGrepMultilineDisabledDoesNotCrossLines(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "main.go", multilineSource)
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func foo\\(.*?\\) int"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "No matches found.")
}

// TestGrepMultilineFallbackCountMode reports one count per match span.
func TestGrepMultilineFallbackCountMode(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "spans.txt", "a START x\ny END b\nmid\nc START z\nw END d\n")
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"START.*?END","multiline":true,"output_mode":"count"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "spans.txt:2", strings.TrimSpace(result.Content))
}

// TestGrepMultilineFallbackTwoMatches prints all lines from both spans in order
// with no separator (mirroring rg -U content output).
func TestGrepMultilineFallbackTwoMatches(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "spans.txt", "a START x\ny END b\nmid\nc START z\nw END d\n")
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"START.*?END","multiline":true}`))
	require.NoError(t, err)
	require.False(t, result.IsError)

	want := "spans.txt:1:a START x\nspans.txt:2:y END b\nspans.txt:4:c START z\nspans.txt:5:w END d"
	require.Equal(t, want, strings.TrimSpace(result.Content))
	require.NotContains(t, result.Content, "--")
	require.NotContains(t, result.Content, "spans.txt:3:")
}

// TestGrepMultilineFallbackFilesMode lists files with a multiline match once.
func TestGrepMultilineFallbackFilesMode(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "main.go", multilineSource)
	writeGrepFile(t, dir, "other.go", "package other\n")
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func foo\\(.*?\\) int","multiline":true,"output_mode":"files_with_matches"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "main.go", strings.TrimSpace(result.Content))
}

// TestGrepMultilineFallbackSkipsBinary keeps the binary guard in the multiline
// path: a NUL-containing file is never returned.
func TestGrepMultilineFallbackSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "text.go", multilineSource)
	binary := append([]byte("func foo(\n) int \x00"), make([]byte, 50)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), binary, 0o644))
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func foo\\(.*?\\) int","multiline":true,"output_mode":"files_with_matches"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "text.go")
	require.NotContains(t, result.Content, "bin.dat")
}

// TestGrepMultilineSmartCase keeps smart-case behaviour: an all-lowercase
// multiline pattern matches case-insensitively.
func TestGrepMultilineSmartCase(t *testing.T) {
	dir := t.TempDir()
	writeGrepFile(t, dir, "main.go", "Func Foo(\n) Int\n")
	forceFallback(t)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	result, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"func foo\\(.*?\\) int","multiline":true}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "main.go:1:Func Foo(")
}

// TestGrepMultilineRipgrepParity asserts the rg path and the Go fallback agree
// byte-for-byte on multiline content output. Skipped when rg is unavailable.
func TestGrepMultilineRipgrepParity(t *testing.T) {
	if _, err := lookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}
	dir := t.TempDir()
	writeGrepFile(t, dir, "spans.txt", "a START x\ny END b\nmid\nc START z\nw END d\n")
	args := json.RawMessage(`{"pattern":"START.*?END","multiline":true}`)

	// rg path (default lookPath).
	rgTool := newGrepTool(Dependencies{WorkDir: dir})
	rgResult, err := rgTool.Run(context.Background(), args)
	require.NoError(t, err)
	require.False(t, rgResult.IsError)

	// Go fallback.
	forceFallback(t)
	goTool := newGrepTool(Dependencies{WorkDir: dir})
	goResult, err := goTool.Run(context.Background(), args)
	require.NoError(t, err)
	require.False(t, goResult.IsError)

	require.Equal(t, strings.TrimSpace(rgResult.Content), strings.TrimSpace(goResult.Content))
}
