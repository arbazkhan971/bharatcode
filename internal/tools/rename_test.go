package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/stretchr/testify/require"
)

type fakeRename struct {
	edit lsp.WorkspaceEdit
	err  error

	lastPath    string
	lastLine    int
	lastCol     int
	lastNewName string
}

func (f *fakeRename) Rename(_ context.Context, path string, line, col int, newName string) (lsp.WorkspaceEdit, error) {
	f.lastPath = path
	f.lastLine = line
	f.lastCol = col
	f.lastNewName = newName
	return f.edit, f.err
}

// replaceWord builds a single-line edit replacing characters [start,end) on
// line 0 with newText.
func replaceWord(start, end int, newText string) lsp.TextEdit {
	return lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: start}, End: lsp.Position{Line: 0, Character: end}},
		NewText: newText,
	}
}

func TestRenameAppliesEditsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(a, []byte("foo()\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("foo()\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		a: {replaceWord(0, 3, "bar")},
		b: {replaceWord(0, 3, "bar")},
	}}}
	tool := &renameTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "column": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// 1-based coordinates are translated to the LSP's 0-based positions.
	require.Equal(t, a, src.lastPath)
	require.Equal(t, 0, src.lastLine)
	require.Equal(t, 0, src.lastCol)
	require.Equal(t, "bar", src.lastNewName)

	require.Contains(t, result.Content, `renamed to "bar"`)
	require.Contains(t, result.Content, "2 edit(s) across 2 file(s)")
	require.Contains(t, result.Content, "a.go (1 edit(s))")
	require.Contains(t, result.Content, "b.go (1 edit(s))")

	gotA, err := os.ReadFile(a)
	require.NoError(t, err)
	require.Equal(t, "bar()\n", string(gotA))
	gotB, err := os.ReadFile(b)
	require.NoError(t, err)
	require.Equal(t, "bar()\n", string(gotB))

	// A compact unified diff of each changed file is included so the model sees
	// the touched lines, mirroring the edit/multiedit/write tools.
	require.Contains(t, result.Content, "-foo()")
	require.Contains(t, result.Content, "+bar()")
	diffs, ok := result.Metadata["diffs"].(map[string]string)
	require.True(t, ok)
	require.Contains(t, diffs, "a.go")
	require.Contains(t, diffs, "b.go")
	require.Contains(t, diffs["a.go"], "+bar()")
}

func TestRenamePreviewShowsDiffWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(a, []byte("foo()\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("foo()\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		a: {replaceWord(0, 3, "bar")},
		b: {replaceWord(0, 3, "bar")},
	}}}
	// A diagnoser is wired but must not run for a preview, which writes nothing.
	tool := &renameTool{
		source: src,
		deps:   Dependencies{WorkDir: dir},
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(a, 0, 0, lsp.Error, "undefined: bar"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar", "preview": true,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The result is framed as a preview and still carries the per-file diffs.
	require.Contains(t, result.Content, "preview: renaming to \"bar\"")
	require.Contains(t, result.Content, "2 edit(s) across 2 file(s)")
	require.Contains(t, result.Content, "nothing written")
	require.Contains(t, result.Content, "-foo()")
	require.Contains(t, result.Content, "+bar()")
	require.Equal(t, true, result.Metadata["preview"])
	diffs, ok := result.Metadata["diffs"].(map[string]string)
	require.True(t, ok)
	require.Contains(t, diffs["a.go"], "+bar()")

	// Nothing is written to disk and the diagnostics re-check is skipped.
	gotA, err := os.ReadFile(a)
	require.NoError(t, err)
	require.Equal(t, "foo()\n", string(gotA))
	gotB, err := os.ReadFile(b)
	require.NoError(t, err)
	require.Equal(t, "foo()\n", string(gotB))
	require.NotContains(t, result.Content, "undefined: bar")
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestRenamePreviewSkipsPermissionCheck(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(a, []byte("foo()\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		a: {replaceWord(0, 3, "bar")},
	}}}
	// A permission policy that denies the rename tool outright: an applying rename
	// would be blocked, but a preview writes nothing and so never consults it.
	cfg := &config.Config{}
	cfg.Permissions.Deny = []string{"rename"}
	tool := &renameTool{source: src, deps: Dependencies{WorkDir: dir, Permission: permission.New(cfg, nil)}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar", "preview": true,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "preview: renaming to \"bar\"")

	// The same rename without preview is denied, confirming the policy is live.
	denied, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Contains(t, denied.Content, "permission denied")
}

func TestRenameSurfacesPostWriteDiagnostics(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(a, []byte("foo()\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		a: {replaceWord(0, 3, "bar")},
	}}}
	tool := &renameTool{
		source: src,
		deps:   Dependencies{WorkDir: dir},
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(a, 0, 0, lsp.Error, "undefined: bar"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Contains(t, result.Content, "undefined: bar")
	require.Contains(t, result.Content, "please fix")
	require.Contains(t, result.Metadata["diagnostics"], "undefined: bar")
}

func TestRenameOmitsDiagnosticsWhenClean(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(a, []byte("foo()\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		a: {replaceWord(0, 3, "bar")},
	}}}
	// Only hint/info-level diagnostics: nothing actionable should be appended.
	tool := &renameTool{
		source: src,
		deps:   Dependencies{WorkDir: dir},
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(a, 0, 0, lsp.Hint, "consider simplifying"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotContains(t, result.Content, "Diagnostics after editing")
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestRenameNoChangesReportsNothingDone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("foo\n"), 0o644))
	tool := &renameTool{source: &fakeRename{}, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "No rename performed")
}

func TestRenameRejectsEditOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("foo\n"), 0o644))
	outside := filepath.Join(t.TempDir(), "other.go")
	require.NoError(t, os.WriteFile(outside, []byte("foo\n"), 0o644))

	src := &fakeRename{edit: lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		outside: {replaceWord(0, 3, "bar")},
	}}}
	tool := &renameTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "outside the workspace")

	// The out-of-workspace file must be left untouched.
	got, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "foo\n", string(got))
}

func TestRenameRequiresNewName(t *testing.T) {
	dir := t.TempDir()
	tool := &renameTool{source: &fakeRename{}, deps: Dependencies{WorkDir: dir}}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "  ",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "non-empty new_name")
}

func TestRenameUnavailableWithoutSource(t *testing.T) {
	tool := &renameTool{deps: Dependencies{WorkDir: t.TempDir()}}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "no LSP manager configured")
}

func TestRenameValidatesPath(t *testing.T) {
	dir := t.TempDir()
	tool := &renameTool{source: &fakeRename{}, deps: Dependencies{WorkDir: dir}}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "../escape.go", "line": 1, "new_name": "bar",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "outside the workspace")
}

func TestRenamePropagatesServerError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("foo\n"), 0o644))
	src := &fakeRename{err: errors.New("server down")}
	tool := &renameTool{source: src, deps: Dependencies{WorkDir: dir}}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go", "line": 1, "new_name": "bar",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "server down")
}

func TestRenameRejectsMalformedJSON(t *testing.T) {
	tool := &renameTool{source: &fakeRename{}, deps: Dependencies{WorkDir: t.TempDir()}}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid rename arguments")
}
