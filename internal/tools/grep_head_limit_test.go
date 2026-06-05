package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeNumberedLines writes a file whose lines are "match 1".."match N", each
// matching the pattern `match`, so the grep window can be reasoned about by
// entry count.
func writeMatchFile(t *testing.T, dir, name string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "match %d\n", i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644))
}

// TestGrepHeadLimitCapsContentLines asserts head_limit keeps only the first N
// content rows (Go fallback) and advertises the window range.
func TestGrepHeadLimitCapsContentLines(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 10)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","head_limit":3}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"a.txt:1:match 1",
		"a.txt:2:match 2",
		"a.txt:3:match 3",
		"[showing entries 1-3 of 10]",
	}, lines)
}

// TestGrepOffsetSkipsEntries asserts offset drops the leading entries before the
// window is rendered, paging deeper into the result list.
func TestGrepOffsetSkipsEntries(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 10)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","offset":7}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"a.txt:8:match 8",
		"a.txt:9:match 9",
		"a.txt:10:match 10",
		"[showing entries 8-10 of 10]",
	}, lines)
}

// TestGrepOffsetAndHeadLimitPaging asserts offset and head_limit compose as
// `tail -n +offset | head -N`, selecting an interior page.
func TestGrepOffsetAndHeadLimitPaging(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 10)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","offset":3,"head_limit":2}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"a.txt:4:match 4",
		"a.txt:5:match 5",
		"[showing entries 4-5 of 10]",
	}, lines)
}

// TestGrepHeadLimitFilesMode asserts the window counts one entry per file in
// files_with_matches mode.
func TestGrepHeadLimitFilesMode(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		writeMatchFile(t, dir, name, 1)
	}

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","output_mode":"files_with_matches","head_limit":2}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"a.txt",
		"b.txt",
		"[showing entries 1-2 of 4]",
	}, lines)
}

// TestGrepHeadLimitCountMode asserts the window counts one entry per file in
// count mode too.
func TestGrepHeadLimitCountMode(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 2)
	writeMatchFile(t, dir, "b.txt", 3)
	writeMatchFile(t, dir, "c.txt", 4)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","output_mode":"count","offset":1}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"b.txt:3",
		"c.txt:4",
		"[showing entries 2-3 of 3]",
	}, lines)
}

// TestGrepHeadLimitLargerThanResultsUntouched asserts a head_limit at or above
// the result count leaves the output unchanged (no window notice).
func TestGrepHeadLimitLargerThanResultsUntouched(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 3)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","head_limit":50}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.NotContains(t, res.Content, "showing entries")
	lines := strings.Split(res.Content, "\n")
	require.Equal(t, []string{
		"a.txt:1:match 1",
		"a.txt:2:match 2",
		"a.txt:3:match 3",
	}, lines)
}

// TestGrepOffsetPastEnd asserts an offset beyond the last entry yields a clear
// message instead of empty output.
func TestGrepOffsetPastEnd(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 3)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"match","offset":9}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "No results in window: offset 9 skips all 3 entries.", res.Content)
}

// TestGrepHeadLimitNoMatchesUntouched asserts the no-match sentinel is returned
// verbatim even when a window is requested.
func TestGrepHeadLimitNoMatchesUntouched(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 3)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"absent","head_limit":2}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "No matches found.", res.Content)
}

// TestGrepNegativeWindowRejected asserts negative offset/head_limit are errors.
func TestGrepNegativeWindowRejected(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeMatchFile(t, dir, "a.txt", 1)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	for _, raw := range []string{
		`{"pattern":"match","head_limit":-1}`,
		`{"pattern":"match","offset":-1}`,
	} {
		res, err := tool.Run(context.Background(), json.RawMessage(raw))
		require.NoError(t, err)
		require.True(t, res.IsError, "expected error for %s", raw)
	}
}

// TestGrepWindowParityRgVsFallback asserts the rg path and the Go fallback page
// a single file identically under offset+head_limit. It is skipped when rg is
// absent so CI without ripgrep stays green.
func TestGrepWindowParityRgVsFallback(t *testing.T) {
	if _, err := lookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}
	dir := t.TempDir()
	writeMatchFile(t, dir, "a.txt", 10)

	raw := json.RawMessage(`{"pattern":"match","offset":2,"head_limit":3}`)

	// rg path (default lookPath).
	rgRes, err := newGrepTool(Dependencies{WorkDir: dir}).Run(context.Background(), raw)
	require.NoError(t, err)

	// Go fallback path.
	forceFallback(t)
	goRes, err := newGrepTool(Dependencies{WorkDir: dir}).Run(context.Background(), raw)
	require.NoError(t, err)

	require.Equal(t, goRes.Content, rgRes.Content)
	require.Contains(t, rgRes.Content, "[showing entries 3-5 of 10]")
}

// TestApplyHeadWindowSupersedesCapNotice unit-tests the cap-notice handling that
// is impractical to trigger end-to-end (grepMatchCap is 1000): when the window
// trims, the cap notice is replaced; when it covers everything, it survives.
func TestApplyHeadWindowSupersedesCapNotice(t *testing.T) {
	capped := "a.txt:1:x\na.txt:2:x\na.txt:3:x\n[results capped: showing first 1000 matches]"

	trimmed := applyHeadWindow(capped, 0, 2)
	require.Equal(t, "a.txt:1:x\na.txt:2:x\n[showing entries 1-2 of 3]", trimmed)

	// A window that covers all entries keeps the original cap notice intact.
	kept := applyHeadWindow(capped, 0, 3)
	require.Equal(t, capped, kept)
}
