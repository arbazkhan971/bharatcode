package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/stretchr/testify/require"
)

type fakeSymbols struct {
	workspace []lsp.Symbol
	document  []lsp.Symbol
	lastQuery string
	lastPath  string
}

func (f *fakeSymbols) WorkspaceSymbols(_ context.Context, query string) ([]lsp.Symbol, error) {
	f.lastQuery = query
	return f.workspace, nil
}

func (f *fakeSymbols) DocumentSymbols(_ context.Context, path string) ([]lsp.Symbol, error) {
	f.lastPath = path
	return f.document, nil
}

func TestSymbolsWorkspaceSearchFormatsAndSorts(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSymbols{workspace: []lsp.Symbol{
		{
			Name:  "Run",
			Kind:  lsp.Method,
			Path:  filepath.Join(dir, "b.go"),
			Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 5}},
		},
		{
			Name:          "Run",
			Kind:          lsp.Function,
			Path:          filepath.Join(dir, "a.go"),
			Range:         lsp.Range{Start: lsp.Position{Line: 9, Character: 0}},
			ContainerName: "pkg",
		},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Run"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "Run", src.lastQuery)
	// a.go sorts before b.go; columns/lines are 1-based; container appended.
	require.Equal(t, "a.go:10:1: function Run (in pkg)\nb.go:5:6: method Run", result.Content)
}

func TestSymbolsDocumentOutlineFiltersByQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	src := &fakeSymbols{document: []lsp.Symbol{
		{Name: "Server", Kind: lsp.Struct, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}},
		{Name: "handler", Kind: lsp.Function, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8}}},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go", "query": "serv"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, path, src.lastPath)
	require.Equal(t, "main.go:3:1: struct Server", result.Content)
}

func TestSymbolsDocumentOutlineRendersDetail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	src := &fakeSymbols{document: []lsp.Symbol{
		{Name: "Add", Kind: lsp.Function, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}, Detail: "func(a int, b int) int"},
		// A symbol without a detail keeps the bare "kind name" form.
		{Name: "Server", Kind: lsp.Struct, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8}}},
		// Detail and container both present: detail precedes the "(in ...)" suffix.
		{Name: "Run", Kind: lsp.Method, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 12}}, Detail: "func() error", ContainerName: "Server"},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"main.go:3:1: function Add func(a int, b int) int\n"+
			"main.go:9:1: struct Server\n"+
			"main.go:13:1: method Run func() error (in Server)",
		result.Content)
}

func TestSymbolsWorkspaceRequiresQuery(t *testing.T) {
	tool := &symbolsTool{source: &fakeSymbols{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "non-empty query")
}

func TestSymbolsRejectsPathOutsideWorkspace(t *testing.T) {
	tool := &symbolsTool{source: &fakeSymbols{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "../escape.go"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestSymbolsUnavailableWithoutSource(t *testing.T) {
	tool := &symbolsTool{workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "X"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "no LSP manager configured")
}

func TestSymbolsNoMatches(t *testing.T) {
	tool := &symbolsTool{source: &fakeSymbols{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Nope"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No symbols found.", result.Content)
}
