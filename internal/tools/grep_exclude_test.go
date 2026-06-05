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

// writeExcludeFixture lays down a small tree with a source file and a matching
// test file so exclude can be exercised against base-name globs.
func writeExcludeFixture(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget.go"),
		[]byte("package p\nfunc Build() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget_test.go"),
		[]byte("package p\nfunc TestBuild() {}\n"), 0o644))
}

// TestGrepExcludeSkipsMatchingFiles asserts the Go fallback drops files whose
// base name matches the exclude glob: "*_test.go" removes widget_test.go while
// the non-test file still matches.
func TestGrepExcludeSkipsMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeExcludeFixture(t, dir)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"Build","exclude":"*_test.go"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "widget.go:")
	require.NotContains(t, res.Content, "widget_test.go")
}

// TestGrepExcludeWithoutFlagSearchesEverything is the control: without exclude,
// both files match, proving the fixture would otherwise include the test file.
func TestGrepExcludeWithoutFlagSearchesEverything(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeExcludeFixture(t, dir)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"Build"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "widget.go:")
	require.Contains(t, res.Content, "widget_test.go:")
}

// TestGrepIncludeAndExcludeCombine asserts a file must pass include AND not
// match exclude. Here include keeps only *.go and exclude drops *_test.go, so
// only widget.go survives even though a .md file is also present.
func TestGrepIncludeAndExcludeCombine(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeExcludeFixture(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"),
		[]byte("Build instructions\n"), 0o644))

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"Build","include":"*.go","exclude":"*_test.go"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Contains(t, res.Content, "widget.go:")
	require.NotContains(t, res.Content, "widget_test.go")
	require.NotContains(t, res.Content, "notes.md")
}

// TestGrepExcludeFilesMode asserts exclude also applies in files_with_matches
// mode, not just content mode (the two fallback walk loops are separate).
func TestGrepExcludeFilesMode(t *testing.T) {
	dir := t.TempDir()
	forceFallback(t)
	writeExcludeFixture(t, dir)

	tool := newGrepTool(Dependencies{WorkDir: dir})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"pattern":"Build","exclude":"*_test.go","output_mode":"files_with_matches"}`))
	require.NoError(t, err)
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	require.Equal(t, []string{"widget.go"}, lines)
}

// TestGrepExcludeParityRgVsFallback asserts the ripgrep path (negated --glob)
// and the Go fallback (filepath.Match on the base name) agree on which files an
// exclude glob removes.
func TestGrepExcludeParityRgVsFallback(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping parity check")
	}
	dir := t.TempDir()
	writeExcludeFixture(t, dir)

	run := func() string {
		tool := newGrepTool(Dependencies{WorkDir: dir})
		res, err := tool.Run(context.Background(),
			json.RawMessage(`{"pattern":"Build","exclude":"*_test.go","output_mode":"files_with_matches"}`))
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res.Content
	}

	rgRows := matchRows(run()) // rg path (rg on PATH)

	oldLookPath := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = oldLookPath })
	goRows := matchRows(run()) // forced Go fallback

	require.Equal(t, goRows, rgRows, "rg and Go fallback must agree under exclude")
	require.Equal(t, []string{"widget.go"}, goRows)
}
