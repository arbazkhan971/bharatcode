package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGrepWordFallbackMatchesWholeWordsOnly asserts the Go fallback honours
// word:true by refusing substring hits: "id" matches the bare identifier but
// not "width", "hidden", or "idle".
func TestGrepWordFallbackMatchesWholeWordsOnly(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("id := 1\nwidth := 2\nhidden := 3\nidle := 4\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"id","word":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.go:1:id := 1")
	require.NotContains(t, res.Content, "width")
	require.NotContains(t, res.Content, "hidden")
	require.NotContains(t, res.Content, "idle")
}

// TestGrepWordWithoutFlagMatchesSubstrings is the control: with word unset the
// same pattern still matches the substring occurrences, so the flag — not some
// unrelated behaviour — is what bounds the match.
func TestGrepWordWithoutFlagMatchesSubstrings(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("id := 1\nwidth := 2\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"id"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.go:1:id := 1")
	require.Contains(t, res.Content, "a.go:2:width := 2")
}

// TestGrepWordComposesWithAlternation asserts the boundaries wrap the whole
// alternation, not just its first branch: \b(?:cat|dog)\b must keep "cat" and
// "dog" while rejecting "category" and "dogma".
func TestGrepWordComposesWithAlternation(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("cat\ncategory\ndog\ndogma\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"cat|dog","word":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	require.Equal(t, []string{"a.txt:1:cat", "a.txt:3:dog"}, lines)
}

// TestGrepWordComposesWithCaseInsensitive asserts word and case_insensitive
// combine: a whole-word, case-folded match still respects the boundaries.
func TestGrepWordComposesWithCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("ID here\nVALID token\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"id","word":true,"case_insensitive":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.txt:1:ID here")
	require.NotContains(t, res.Content, "VALID")
}

// TestGrepWordMultiline asserts word:true also bounds matches in multiline
// mode, which compiles through the separate compileMultilineSmartCase path.
func TestGrepWordMultiline(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("id\nrigid\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"id","word":true,"multiline":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.txt:1:id")
	require.NotContains(t, res.Content, "rigid")
}

// TestGrepWordParityRgVsFallback asserts the ripgrep path (--word-regexp) and
// the Go fallback (\b…\b) agree on a mixed file of whole-word and substring
// occurrences.
func TestGrepWordParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("id := 1\nwidth := 2\nhidden := 3\nidle := 4\nid\n"), 0o644))

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"id","word":true}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgRows := matchRows(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goRows := matchRows(run()) // forced Go fallback

	require.Equal(t, goRows, rgRows, "rg and Go fallback must agree under word")
	require.Equal(t, []string{"a.go:1:id := 1", "a.go:5:id"}, goRows)
}
