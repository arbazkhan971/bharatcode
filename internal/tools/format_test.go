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

type fakeFormat struct {
	edits      []lsp.TextEdit
	rangeEdits []lsp.TextEdit
	err        error

	lastPath  string
	lastRange lsp.Range
}

func (f *fakeFormat) Format(_ context.Context, path string) ([]lsp.TextEdit, error) {
	f.lastPath = path
	return f.edits, f.err
}

func (f *fakeFormat) FormatRange(_ context.Context, path string, rng lsp.Range) ([]lsp.TextEdit, error) {
	f.lastPath = path
	f.lastRange = rng
	return f.rangeEdits, f.err
}

func writeFormatFile(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestFormatAppliesEditsAndWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "package main\nfunc  f(){}\n")
	src := &fakeFormat{edits: []lsp.TextEdit{
		// Replace the whole second line with gofmt-style spacing.
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 1, Character: 0},
				End:   lsp.Position{Line: 2, Character: 0},
			},
			NewText: "func f() {}\n",
		},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, path, src.lastPath)
	require.Contains(t, result.Content, "formatted main.go")
	require.Contains(t, result.Content, "1 edit")

	// The result surfaces a unified diff of the reformatting, both inline in the
	// content and as structured metadata, mirroring the edit tool.
	require.Contains(t, result.Content, "func  f(){}")
	require.Contains(t, result.Content, "func f() {}")
	diff, ok := result.Metadata["diff"].(string)
	require.True(t, ok, "expected a diff in metadata")
	require.Contains(t, diff, "+func f() {}")
	require.Contains(t, diff, "-func  f(){}")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\nfunc f() {}\n", string(got))
}

func TestFormatSurfacesPostWriteDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "package main\nfunc  f(){}\n")
	src := &fakeFormat{edits: []lsp.TextEdit{
		{
			Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 2, Character: 0}},
			NewText: "func f() {}\n",
		},
	}}
	tool := &formatTool{
		source: src,
		deps:   Dependencies{WorkDir: dir},
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(path, 0, 0, lsp.Error, "undefined: bar"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The reformatted file is re-checked and its errors surfaced, both inline and
	// in metadata, matching the edit/rename/codeactions tools.
	require.Contains(t, result.Content, "Diagnostics after editing")
	require.Contains(t, result.Content, "undefined: bar")
	require.Contains(t, result.Metadata["diagnostics"], "undefined: bar")
}

func TestFormatPreviewSkipsDiagnosticsRecheck(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "package main\nfunc  f(){}\n")
	src := &fakeFormat{edits: []lsp.TextEdit{
		{
			Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 2, Character: 0}},
			NewText: "func f() {}\n",
		},
	}}
	diagnoser := &fakeDiagnoser{diags: []lsp.Diagnostic{
		diag(path, 0, 0, lsp.Error, "undefined: bar"),
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}, diag: diagnoser}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "preview": true}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// A preview writes nothing, so the diagnoser must not be consulted and no
	// diagnostics are appended.
	require.Empty(t, diagnoser.changed)
	require.NotContains(t, result.Content, "Diagnostics after editing")
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestFormatOmitsDiagnosticsWhenClean(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "package main\nfunc  f(){}\n")
	src := &fakeFormat{edits: []lsp.TextEdit{
		{
			Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 2, Character: 0}},
			NewText: "func f() {}\n",
		},
	}}
	// Only hint-level diagnostics: nothing actionable should be appended.
	tool := &formatTool{
		source: src,
		deps:   Dependencies{WorkDir: dir},
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(path, 0, 0, lsp.Hint, "consider simplifying"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotContains(t, result.Content, "Diagnostics after editing")
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestFormatPreviewShowsDiffWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	original := "package main\nfunc  f(){}\n"
	path := writeFormatFile(t, dir, original)
	src := &fakeFormat{edits: []lsp.TextEdit{
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 1, Character: 0},
				End:   lsp.Position{Line: 2, Character: 0},
			},
			NewText: "func f() {}\n",
		},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "preview": true}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The preview announces itself, says nothing was written, and still surfaces
	// the diff both inline and in metadata.
	require.Contains(t, result.Content, "preview")
	require.Contains(t, result.Content, "nothing written")
	require.Contains(t, result.Content, "+func f() {}")
	require.Contains(t, result.Content, "-func  f(){}")
	require.Equal(t, true, result.Metadata["preview"])
	diff, ok := result.Metadata["diff"].(string)
	require.True(t, ok, "expected a diff in metadata")
	require.Contains(t, diff, "+func f() {}")

	// The file on disk is untouched.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, string(got))
}

func TestFormatPreviewSkipsPermissionCheck(t *testing.T) {
	dir := t.TempDir()
	writeFormatFile(t, dir, "package main\nfunc  f(){}\n")
	src := &fakeFormat{edits: []lsp.TextEdit{
		{
			Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 2, Character: 0}},
			NewText: "func f() {}\n",
		},
	}}
	// A permission policy that denies the format tool outright: an applying format
	// would be blocked, but a preview writes nothing and so never consults it.
	cfg := &config.Config{}
	cfg.Permissions.Deny = []string{"format"}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir, Permission: permission.New(cfg, nil)}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "preview": true}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "preview")

	// The same format without preview is denied, confirming the policy is live.
	denied, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Contains(t, denied.Content, "permission denied")
}

func TestFormatRangeFormatsOnlyTheSpan(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "package main\nfunc  f(){}\nfunc  g(){}\n")
	src := &fakeFormat{rangeEdits: []lsp.TextEdit{
		// Reformat just the second line, leaving g untouched.
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 1, Character: 0},
				End:   lsp.Position{Line: 2, Character: 0},
			},
			NewText: "func f() {}\n",
		},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 2}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, path, src.lastPath)

	// The whole-file formatter must not have been consulted; the request carried
	// the single-line span (0-based, end at the start of the following line).
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 1, Character: 0},
		End:   lsp.Position{Line: 2, Character: 0},
	}, src.lastRange)
	require.Contains(t, result.Content, "formatted main.go:2")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\nfunc f() {}\nfunc  g(){}\n", string(got))
}

func TestFormatRangeSpansMultipleLines(t *testing.T) {
	dir := t.TempDir()
	writeFormatFile(t, dir, "package main\nfunc  f(){}\nfunc  g(){}\n")
	src := &fakeFormat{rangeEdits: []lsp.TextEdit{
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 0}}, NewText: ""},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 2, "end_line": 3}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The span covers lines 2..3 (0-based 1..2); the end addresses the start of
	// the line after the last selected one.
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 1, Character: 0},
		End:   lsp.Position{Line: 3, Character: 0},
	}, src.lastRange)
	require.Contains(t, result.Content, "main.go:2-3")
}

func TestFormatRangeAlreadyFormattedReportsSpan(t *testing.T) {
	dir := t.TempDir()
	writeFormatFile(t, dir, "package main\n")
	tool := &formatTool{source: &fakeFormat{}, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 1}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "main.go:1 is already formatted.", result.Content)
}

func TestFormatAppliesMultipleEditsInOrder(t *testing.T) {
	dir := t.TempDir()
	path := writeFormatFile(t, dir, "aXbYc")
	// Two non-overlapping single-character replacements on the same line; the
	// tool must apply them back-to-front so offsets stay valid.
	src := &fakeFormat{edits: []lsp.TextEdit{
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 1}, End: lsp.Position{Line: 0, Character: 2}}, NewText: "-"},
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 3}, End: lsp.Position{Line: 0, Character: 4}}, NewText: "_"},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "a-b_c", string(got))
}

func TestFormatHandlesMultibyteColumns(t *testing.T) {
	dir := t.TempDir()
	// "héllo" — é is one UTF-16 unit but two bytes, so a column-2 edit must land
	// after it by byte offset, not character index.
	path := writeFormatFile(t, dir, "héllo")
	src := &fakeFormat{edits: []lsp.TextEdit{
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 2}, End: lsp.Position{Line: 0, Character: 5}}, NewText: "y"},
	}}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "héy", string(got))
}

func TestFormatNoEditsReportsAlreadyFormatted(t *testing.T) {
	dir := t.TempDir()
	writeFormatFile(t, dir, "package main\n")
	tool := &formatTool{source: &fakeFormat{}, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "main.go is already formatted.", result.Content)
}

func TestFormatUnavailableWithoutSource(t *testing.T) {
	tool := &formatTool{deps: Dependencies{WorkDir: t.TempDir()}}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "no LSP manager configured")
}

func TestFormatValidatesPath(t *testing.T) {
	dir := t.TempDir()
	tool := &formatTool{source: &fakeFormat{}, deps: Dependencies{WorkDir: dir}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "../escape.go"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "outside the workspace")
}

func TestFormatPropagatesServerError(t *testing.T) {
	dir := t.TempDir()
	writeFormatFile(t, dir, "package main\n")
	src := &fakeFormat{err: errors.New("server down")}
	tool := &formatTool{source: src, deps: Dependencies{WorkDir: dir}}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "server down")
}

func TestFormatRejectsMalformedJSON(t *testing.T) {
	tool := &formatTool{source: &fakeFormat{}, deps: Dependencies{WorkDir: t.TempDir()}}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid format arguments")
}

func TestApplyTextEditsRejectsOutOfRangeLine(t *testing.T) {
	_, err := applyTextEdits("one line\n", []lsp.TextEdit{
		{Range: lsp.Range{Start: lsp.Position{Line: 9, Character: 0}, End: lsp.Position{Line: 9, Character: 0}}, NewText: "x"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}
