package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/stretchr/testify/require"
)

type fakeNavigate struct {
	definition     []lsp.Location
	typeDefinition []lsp.Location
	implementation []lsp.Location
	references     []lsp.Location
	hover          string

	defErr  error
	typeErr error
	implErr error
	refErr  error
	hovErr  error

	lastPath string
	lastLine int
	lastCol  int
}

func (f *fakeNavigate) Definition(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.definition, f.defErr
}

func (f *fakeNavigate) TypeDefinition(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.typeDefinition, f.typeErr
}

func (f *fakeNavigate) Implementation(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.implementation, f.implErr
}

func (f *fakeNavigate) References(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.references, f.refErr
}

func (f *fakeNavigate) Hover(_ context.Context, path string, line, col int) (string, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.hover, f.hovErr
}

func writeNavFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	return path
}

func writeNavFileContent(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestNavigateDefinitionConvertsPositionAndFormats(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 41, Character: 7}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 10, "column": 3, "action": "definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// 1-based input is converted to 0-based for the LSP call.
	require.Equal(t, path, src.lastPath)
	require.Equal(t, 9, src.lastLine)
	require.Equal(t, 2, src.lastCol)
	// Output is workspace-relative and back to 1-based.
	require.Equal(t, "main.go:42:8", result.Content)
}

func TestNavigateTypeDefinition(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{typeDefinition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 5}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 10, "column": 3, "action": "type_definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Position is converted 1-based -> 0-based and routed to TypeDefinition.
	require.Equal(t, 9, src.lastLine)
	require.Equal(t, 2, src.lastCol)
	require.Equal(t, "main.go:4:6", result.Content)
}

func TestNavigateTypeDefinitionEmpty(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "type_definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No type definition found.", result.Content)
}

func TestNavigateImplementation(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{implementation: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8, Character: 4}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5, "action": "implementation",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Results are sorted by position, workspace-relative, and 1-based.
	require.Equal(t, "main.go:3:1\nmain.go:9:5", result.Content)
}

func TestNavigateImplementationEmpty(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "implementation",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No implementations found.", result.Content)
}

func TestNavigateDefaultsToDefinitionAndColumnOne(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 4, src.lastLine)
	require.Equal(t, 0, src.lastCol) // column defaulted to 1 -> 0-based 0
	// Line 1 is readable, so its trimmed source is appended after the coordinates.
	require.Equal(t, "main.go:1:1: package main", result.Content)
}

func TestNavigateReferencesSortsAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8, Character: 4}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}}, // dup
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// References carry a scope summary header ahead of the deduplicated list.
	require.Equal(t, "2 references across 1 file:\nmain.go:3:1\nmain.go:9:5", result.Content)
}

func TestNavigateReferencesCountsDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	other := filepath.Join(dir, "other.go")
	require.NoError(t, os.WriteFile(other, []byte("package main\n"), 0o644))
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 2}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Three references spanning two files; the header reflects both counts.
	require.True(t, strings.HasPrefix(result.Content, "3 references across 2 files:\n"), result.Content)
}

func TestNavigateReferencesSingularHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// A lone reference uses singular nouns in the header.
	require.Equal(t, "1 reference across 1 file:\nmain.go:1:1: package main", result.Content)
}

func TestNavigateReferencesAppendsSourceLines(t *testing.T) {
	dir := t.TempDir()
	content := "package main\n\nfunc Run() {}\n\nfunc main() { Run() }\n"
	path := writeNavFileContent(t, dir, content)
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}}},  // func Run() {}
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 13}}}, // call site
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Each entry carries the trimmed source line after its coordinates, below
	// the scope summary header.
	require.Equal(t, "2 references across 1 file:\nmain.go:3:6: func Run() {}\nmain.go:5:14: func main() { Run() }", result.Content)
}

func TestNavigateOmitsSnippetForOutOfRangeLine(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFileContent(t, dir, "package main\n")
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 99, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// No readable source line -> bare coordinates, no trailing colon-space.
	require.Equal(t, "main.go:100:1", result.Content)
}

func TestNavigateHoverReturnsText(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{hover: "func Run(ctx context.Context) error\n"}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "hover",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "func Run(ctx context.Context) error", result.Content)
}

func TestNavigateEmptyResultsReportDirectly(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	tool := &navigateTool{source: &fakeNavigate{}, workDir: dir}

	for _, tc := range []struct {
		action string
		want   string
	}{
		{"definition", "No definition found."},
		{"references", "No references found."},
		{"hover", "No hover information found."},
	} {
		result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
			"path": "main.go", "line": 1, "action": tc.action,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.Equal(t, tc.want, result.Content)
	}
}

func TestNavigateValidatesArgs(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	tool := &navigateTool{source: &fakeNavigate{}, workDir: dir}

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{"line": 1}, "requires a path"},
		{"line below one", map[string]any{"path": "main.go", "line": 0}, "1-based line"},
		{"unknown action", map[string]any{"path": "main.go", "line": 1, "action": "rename"}, "unknown navigate action"},
		{"path escape", map[string]any{"path": "../escape.go", "line": 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Run(context.Background(), mustJSON(t, tc.args))
			require.NoError(t, err)
			require.True(t, result.IsError)
			if tc.want != "" {
				require.Contains(t, result.Content, tc.want)
			}
		})
	}
}

func TestNavigateUnavailableWithoutSource(t *testing.T) {
	tool := &navigateTool{workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 1}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "no LSP manager configured")
}

func TestNavigatePropagatesServerError(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{defErr: errors.New("server down")}
	tool := &navigateTool{source: src, workDir: dir}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"path": "main.go", "line": 2}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "server down")
}

func TestNavigateRejectsMalformedJSON(t *testing.T) {
	tool := &navigateTool{source: &fakeNavigate{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid navigate arguments")
}
