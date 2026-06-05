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

// TestGrepFixedStringsLiteralMetacharacters asserts the Go fallback treats the
// pattern literally when fixed_strings is set: "arr[i]" matches the bytes
// "arr[i]" and does not behave as a character-class regex (which would match a
// single "a", "r", or "i" via "arr" + "[i]").
func TestGrepFixedStringsLiteralMetacharacters(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("x := arr[i]\ny := arri\nz := r\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"arr[i]","fixed_strings":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	require.Equal(t, []string{"a.go:1:x := arr[i]"}, lines)
}

// TestGrepFixedStringsWithoutFlagIsRegex is the control: the same pattern
// without fixed_strings is a regex, so "arr[i]" is "arr" followed by the class
// [i] and matches "arri" (and would not match the literal "arr[i]").
func TestGrepFixedStringsWithoutFlagIsRegex(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("x := arr[i]\ny := arri\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"arr[i]"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.go:2:y := arri")
	require.NotContains(t, res.Content, "arr[i]")
}

// TestGrepFixedStringsInvalidRegexBecomesValid asserts a pattern that is invalid
// regex (an unbalanced paren) is accepted under fixed_strings and matched as a
// literal, instead of erroring on compilation.
func TestGrepFixedStringsInvalidRegexBecomesValid(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("call fmt.Sprintf(\nnoise\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})

	// As a regex, "fmt.Sprintf(" has an unbalanced group and fails to compile,
	// surfacing as a tool error.
	_, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"fmt.Sprintf("}`))
	require.Error(t, err)

	// As a fixed string it is a plain literal that matches the source line.
	good, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"fmt.Sprintf(","fixed_strings":true}`))
	require.NoError(t, err)
	require.False(t, good.IsError)
	require.Contains(t, good.Content, "a.go:1:call fmt.Sprintf(")
}

// TestGrepFixedStringsComposesWithWord asserts fixed_strings and word combine:
// the escaped literal "a.b" is bounded by word boundaries, so the standalone
// token "a.b" matches but the substring inside "a.bc" does not.
func TestGrepFixedStringsComposesWithWord(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("use a.b here\nuse a.bc here\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"a.b","fixed_strings":true,"word":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.txt:1:use a.b here")
	require.NotContains(t, res.Content, "a.bc")
}

// TestGrepFixedStringsSmartCase asserts smart-case still applies to a literal:
// an all-lowercase fixed string matches case-insensitively.
func TestGrepFixedStringsSmartCase(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"),
		[]byte("A.B value\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"a.b","fixed_strings":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.txt:1:A.B value")
}

// TestGrepFixedStringsMultiline asserts fixed_strings is honoured in multiline
// mode, which compiles through the separate compileMultilineSmartCase path: a
// literal containing "(" matches across the buffer without regex interpretation.
func TestGrepFixedStringsMultiline(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("alpha\nx := f(1)\nbeta\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"f(1)","fixed_strings":true,"multiline":true}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "a.go:2:x := f(1)")
}

// TestGrepFixedStringsParityRgVsFallback asserts the ripgrep path
// (--fixed-strings) and the Go fallback (regexp.QuoteMeta) agree on a literal
// containing regex metacharacters.
func TestGrepFixedStringsParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("x := arr[i]\ny := arri\nz := arr[j]\n"), 0o644))

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"arr[i]","fixed_strings":true}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgRows := matchRows(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goRows := matchRows(run()) // forced Go fallback

	require.Equal(t, goRows, rgRows, "rg and Go fallback must agree under fixed_strings")
	require.Equal(t, []string{"a.go:1:x := arr[i]"}, goRows)
}
