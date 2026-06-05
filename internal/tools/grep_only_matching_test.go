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

// TestGrepOnlyMatchingFallbackEmitsSubstrings asserts the Go fallback prints
// only the matched substring (rg -o), not the whole line, in content mode.
func TestGrepOnlyMatchingFallbackEmitsSubstrings(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("func Alpha() {}\nfunc Beta() {}\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"func \\w+","only_matching":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Each row carries path:line:<just the match>, not the surrounding " {}".
	require.Contains(t, res.Content, "a.go:1:func Alpha")
	require.Contains(t, res.Content, "a.go:2:func Beta")
	require.NotContains(t, res.Content, "{}")
}

// TestGrepOnlyMatchingMultiplePerLine asserts every match on a single line is
// emitted on its own row, all sharing that line's number.
func TestGrepOnlyMatchingMultiplePerLine(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("cat dog cat\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"cat","only_matching":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	require.Equal(t, []string{"a.txt:1:cat", "a.txt:1:cat"}, lines)
}

// TestGrepOnlyMatchingSkipsEmptyMatches asserts zero-width matches (possible
// with patterns like "x*") are dropped, mirroring rg -o.
func TestGrepOnlyMatchingSkipsEmptyMatches(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("axxb\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"x*","only_matching":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	// "x*" can match the empty string at several positions; only the real "xx"
	// run is reported, and never an empty trailing field.
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	require.Equal(t, []string{"a.txt:1:xx"}, lines)
}

// TestGrepOnlyMatchingSupersedesContext asserts context options are ignored
// when only_matching is set, so no "path-line-text" context rows appear.
func TestGrepOnlyMatchingSupersedesContext(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("before\nhit needle hit\nafter\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"needle","only_matching":true,"context":1}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Equal(t, "a.txt:2:needle", strings.TrimSpace(res.Content))
	require.NotContains(t, res.Content, "before")
	require.NotContains(t, res.Content, "after")
}

// TestGrepOnlyMatchingIgnoredInMultiline asserts only_matching has no effect in
// multiline mode (the whole touched line is still printed), keeping the rg and
// Go paths consistent.
func TestGrepOnlyMatchingIgnoredInMultiline(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("alpha beta\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"alpha","only_matching":true,"multiline":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Whole line, not just the matched "alpha".
	require.Equal(t, "a.txt:1:alpha beta", strings.TrimSpace(res.Content))
}

// TestGrepOnlyMatchingIgnoredInCountMode asserts only_matching does not change
// count mode, which still reports matching-line counts as path:count.
func TestGrepOnlyMatchingIgnoredInCountMode(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("cat cat\ncat\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"cat","only_matching":true,"output_mode":"count"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Two matching lines, not three matches.
	require.Equal(t, "a.txt:2", strings.TrimSpace(res.Content))
}

// TestGrepOnlyMatchingParityRgVsFallback asserts the rg path and the Go fallback
// produce identical only_matching output for the same input.
func TestGrepOnlyMatchingParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("func Alpha() {}\nfunc Beta() {}\nvar x int\n"), 0o644))

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"func \\w+","only_matching":true}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgRows := matchRows(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goRows := matchRows(run()) // forced Go fallback

	require.Equal(t, goRows, rgRows, "rg and Go fallback must agree under only_matching")
	require.Equal(t, []string{"a.go:1:func Alpha", "a.go:2:func Beta"}, goRows)
}

// matchRows splits grep content output into trimmed, sorted rows, dropping the
// cap-notice line if present.
func matchRows(out string) []string {
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		rows = append(rows, line)
	}
	sort.Strings(rows)
	return rows
}
