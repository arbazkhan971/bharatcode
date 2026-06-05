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

type fakeCodeActions struct {
	actions []lsp.CodeAction
	err     error

	lastPath  string
	lastRange lsp.Range
}

func (f *fakeCodeActions) CodeActions(_ context.Context, file string, rng lsp.Range) ([]lsp.CodeAction, error) {
	f.lastPath, f.lastRange = file, rng
	return f.actions, f.err
}

func writeCodeActionsFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	return path
}

func TestCodeActionsConvertsRangeAndFormats(t *testing.T) {
	dir := t.TempDir()
	path := writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{NewText: "x"}}},
		}},
		{Title: "Run go generate", Kind: "source", Command: &lsp.Command{Command: "gopls.generate"}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 10, "column": 3, "end_line": 12, "end_column": 5,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// 1-based input is converted to 0-based for the LSP call.
	require.Equal(t, path, src.lastPath)
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 9, Character: 2},
		End:   lsp.Position{Line: 11, Character: 4},
	}, src.lastRange)

	// Sorted by kind then title; notes describe how each applies.
	require.Equal(t,
		"1. Run go generate [source] (command: gopls.generate)\n"+
			"2. Organize Imports [source.organizeImports] (edit, 1 file)",
		result.Content,
	)
}

func TestCodeActionsDefaultsRangeToCursor(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{{Title: "Quick Fix", Kind: "quickfix"}}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// column defaults to 1, end defaults to start: a zero-width cursor range.
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 4, Character: 0},
		End:   lsp.Position{Line: 4, Character: 0},
	}, src.lastRange)
	require.Equal(t, "1. Quick Fix [quickfix]", result.Content)
}

func TestCodeActionsDeduplicatesEntries(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Remove unused", Kind: "quickfix"},
		{Title: "Remove unused", Kind: "quickfix"}, // dup
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1,
	}))
	require.NoError(t, err)
	require.Equal(t, "1. Remove unused [quickfix]", result.Content)
}

func TestCodeActionsEmptyReportsDirectly(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	tool := &codeActionsTool{source: &fakeCodeActions{}, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No code actions available.", result.Content)
}

func TestCodeActionsValidatesArgs(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	tool := &codeActionsTool{source: &fakeCodeActions{}, workDir: dir}

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{"line": 1}, "requires a path"},
		{"line below one", map[string]any{"path": "main.go", "line": 0}, "1-based line"},
		{"path escape", map[string]any{"path": "../escape.go", "line": 1}, "escapes the workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Run(context.Background(), mustJSON(t, tc.args))
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Contains(t, result.Content, tc.want)
		})
	}
}

func TestCodeActionsUnavailableWithoutSource(t *testing.T) {
	tool := &codeActionsTool{workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 1}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "no LSP manager configured")
}

func TestCodeActionsPropagatesServerError(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{err: errors.New("server down")}
	tool := &codeActionsTool{source: src, workDir: dir}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 2}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "server down")
}

func TestCodeActionsRejectsMalformedJSON(t *testing.T) {
	tool := &codeActionsTool{source: &fakeCodeActions{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid codeactions arguments")
}
