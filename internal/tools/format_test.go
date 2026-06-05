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
	edits []lsp.TextEdit
	err   error

	lastPath string
}

func (f *fakeFormat) Format(_ context.Context, path string) ([]lsp.TextEdit, error) {
	f.lastPath = path
	return f.edits, f.err
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

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\nfunc f() {}\n", string(got))
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
