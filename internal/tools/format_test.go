package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
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
