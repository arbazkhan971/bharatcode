package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		// A nested method (depth 1) is indented beneath its container rather than
		// carrying a redundant "(in Server)" suffix; its detail still renders.
		{Name: "Run", Kind: lsp.Method, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 12}}, Detail: "func() error", ContainerName: "Server", Depth: 1},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"main.go:3:1: function Add func(a int, b int) int\n"+
			"main.go:9:1: struct Server\n"+
			"  main.go:13:1: method Run func() error",
		result.Content)
}

func TestSymbolsDocumentOutlineRendersNestedTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	// A struct with two fields and a method that nests a closure: depth drives the
	// indentation so the outline reads as the file's structure.
	src := &fakeSymbols{document: []lsp.Symbol{
		{Name: "Server", Kind: lsp.Struct, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}, Depth: 0},
		{Name: "Addr", Kind: lsp.Field, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 3}}, ContainerName: "Server", Depth: 1},
		{Name: "Port", Kind: lsp.Field, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 4}}, ContainerName: "Server", Depth: 1},
		{Name: "Run", Kind: lsp.Method, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8}}, ContainerName: "Server", Depth: 1},
		{Name: "handle", Kind: lsp.Function, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 9}}, ContainerName: "Run", Depth: 2},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"main.go:3:1: struct Server\n"+
			"  main.go:4:1: field Addr\n"+
			"  main.go:5:1: field Port\n"+
			"  main.go:9:1: method Run\n"+
			"    main.go:10:1: function handle",
		result.Content)
}

func TestSymbolsFilteredOutlineStaysFlat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	// A filtered outline (path + query) is not a contiguous hierarchy, so the
	// matched symbol keeps the flat "(in container)" form instead of an indent
	// that would dangle without its parent.
	src := &fakeSymbols{document: []lsp.Symbol{
		{Name: "Server", Kind: lsp.Struct, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}, Depth: 0},
		{Name: "Run", Kind: lsp.Method, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8}}, ContainerName: "Server", Depth: 1},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go", "query": "run"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "main.go:9:1: method Run (in Server)", result.Content)
}

func TestSymbolsWorkspaceFiltersByKind(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSymbols{workspace: []lsp.Symbol{
		{Name: "Run", Kind: lsp.Method, Path: filepath.Join(dir, "a.go"), Range: lsp.Range{Start: lsp.Position{Line: 0}}},
		{Name: "Run", Kind: lsp.Function, Path: filepath.Join(dir, "a.go"), Range: lsp.Range{Start: lsp.Position{Line: 4}}},
		{Name: "RunMode", Kind: lsp.Constant, Path: filepath.Join(dir, "a.go"), Range: lsp.Range{Start: lsp.Position{Line: 8}}},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	// Only the function survives the kind filter; the method and constant are dropped.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Run", "kind": "function"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "a.go:5:1: function Run", result.Content)
}

func TestSymbolsKindFilterAcceptsMultipleLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	// A comma-separated kind list keeps both functions and methods but drops the
	// struct; the kind filter also flattens the outline (no tree indentation).
	src := &fakeSymbols{document: []lsp.Symbol{
		{Name: "Server", Kind: lsp.Struct, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}, Depth: 0},
		{Name: "Run", Kind: lsp.Method, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8}}, ContainerName: "Server", Depth: 1},
		{Name: "handle", Kind: lsp.Function, Path: path, Range: lsp.Range{Start: lsp.Position{Line: 12}}},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go", "kind": "function, method"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"main.go:9:1: method Run (in Server)\n"+
			"main.go:13:1: function handle",
		result.Content)
}

func TestSymbolsKindFilterRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSymbols{workspace: []lsp.Symbol{
		{Name: "Run", Kind: lsp.Function, Path: filepath.Join(dir, "a.go")},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Run", "kind": "func"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "unknown symbol kind(s): func")
}

func TestSymbolsKindFilterNoMatches(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSymbols{workspace: []lsp.Symbol{
		{Name: "Run", Kind: lsp.Function, Path: filepath.Join(dir, "a.go")},
	}}
	tool := &symbolsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Run", "kind": "struct"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No symbols found.", result.Content)
}

func TestSymbolsWorkspaceCapsLargeResultSet(t *testing.T) {
	dir := t.TempDir()
	// A broad query can resolve to far more symbols than fit usefully in context.
	// Build more than the cap, each on its own line, with zero-padded names so the
	// deterministic name-tiebreak sort keeps them in a predictable order.
	const n = symbolMatchCap + 25
	syms := make([]lsp.Symbol, 0, n)
	for i := 0; i < n; i++ {
		syms = append(syms, lsp.Symbol{
			Name:  fmt.Sprintf("Run%04d", i),
			Kind:  lsp.Function,
			Path:  filepath.Join(dir, "a.go"),
			Range: lsp.Range{Start: lsp.Position{Line: i}},
		})
	}
	tool := &symbolsTool{source: &fakeSymbols{workspace: syms}, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"query": "Run"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	// symbolMatchCap rendered entries plus the trailing truncation notice.
	require.Len(t, lines, symbolMatchCap+1)
	// The list is sorted by line, so the first entry is the lowest-numbered symbol
	// and the last rendered entry is the one at the cap boundary.
	require.Equal(t, "a.go:1:1: function Run0000", lines[0])
	require.Equal(t, fmt.Sprintf("a.go:%d:1: function Run%04d", symbolMatchCap, symbolMatchCap-1), lines[symbolMatchCap-1])
	require.Equal(t, fmt.Sprintf("... and %d more (%d total) not shown", n-symbolMatchCap, n), lines[symbolMatchCap])
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
