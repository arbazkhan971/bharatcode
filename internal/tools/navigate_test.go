package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/stretchr/testify/require"
)

type fakeNavigate struct {
	definition     []lsp.Location
	declaration    []lsp.Location
	typeDefinition []lsp.Location
	implementation []lsp.Location
	references     []lsp.Location
	incomingCalls  []lsp.Location
	outgoingCalls  []lsp.Location
	hover          string
	signature      string
	prepareRename  *lsp.PrepareRenameResult

	defErr     error
	declErr    error
	typeErr    error
	implErr    error
	refErr     error
	inErr      error
	outErr     error
	hovErr     error
	sigErr     error
	prepRenErr error

	lastPath        string
	lastLine        int
	lastCol         int
	lastIncludeDecl bool
}

func (f *fakeNavigate) Definition(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.definition, f.defErr
}

func (f *fakeNavigate) Declaration(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.declaration, f.declErr
}

func (f *fakeNavigate) TypeDefinition(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.typeDefinition, f.typeErr
}

func (f *fakeNavigate) Implementation(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.implementation, f.implErr
}

func (f *fakeNavigate) References(_ context.Context, path string, line, col int, includeDeclaration bool) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	f.lastIncludeDecl = includeDeclaration
	return f.references, f.refErr
}

func (f *fakeNavigate) IncomingCalls(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.incomingCalls, f.inErr
}

func (f *fakeNavigate) OutgoingCalls(_ context.Context, path string, line, col int) ([]lsp.Location, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.outgoingCalls, f.outErr
}

func (f *fakeNavigate) Hover(_ context.Context, path string, line, col int) (string, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.hover, f.hovErr
}

func (f *fakeNavigate) SignatureHelp(_ context.Context, path string, line, col int) (string, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.signature, f.sigErr
}

func (f *fakeNavigate) PrepareRename(_ context.Context, path string, line, col int) (*lsp.PrepareRenameResult, error) {
	f.lastPath, f.lastLine, f.lastCol = path, line, col
	return f.prepareRename, f.prepRenErr
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

func TestNavigateDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{declaration: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 6}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 10, "column": 3, "action": "declaration",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Position is converted 1-based -> 0-based and routed to Declaration, not
	// Definition: the declaration action must not silently fall through to the
	// definition lookup.
	require.Equal(t, 9, src.lastLine)
	require.Equal(t, 2, src.lastCol)
	require.Equal(t, "main.go:3:7", result.Content)
}

func TestNavigateDeclarationEmpty(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "declaration",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No declaration found.", result.Content)
}

func TestNavigateDeclarationError(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{declErr: errors.New("server down")}
	tool := &navigateTool{source: src, workDir: dir}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "declaration",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolving declaration")
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
	// Multiple implementations get a scope summary header, then sorted 1-based
	// coordinates, matching the references/calls output shape.
	require.Equal(t, "2 implementations across 1 file:\nmain.go:3:1\nmain.go:9:5", result.Content)
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

func TestNavigateIncomingCalls(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{incomingCalls: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 8, Character: 4}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5, "column": 3, "action": "incoming_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Position is converted 1-based -> 0-based and routed to IncomingCalls.
	require.Equal(t, 4, src.lastLine)
	require.Equal(t, 2, src.lastCol)
	// Callers are sorted by position, workspace-relative, and 1-based, behind a
	// scope summary mirroring the references output.
	require.Equal(t, "2 callers across 1 file:\nmain.go:3:1\nmain.go:9:5", result.Content)
}

func TestNavigateIncomingCallsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "incoming_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No callers found.", result.Content)
}

func TestNavigateOutgoingCalls(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{outgoingCalls: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5, "action": "outgoing_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Line 1 is readable, so its trimmed source is appended after the coordinates,
	// behind a single-callee scope summary.
	require.Equal(t, "1 callee across 1 file:\nmain.go:1:1: package main", result.Content)
}

func TestNavigateIncomingCallsCountsDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	other := filepath.Join(dir, "other.go")
	require.NoError(t, os.WriteFile(other, []byte("package main\n"), 0o644))
	src := &fakeNavigate{incomingCalls: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 2}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "incoming_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Three callers spanning two files; the header reflects both plural counts.
	require.True(t, strings.HasPrefix(result.Content, "3 callers across 2 files:\n"), result.Content)
}

func TestNavigateOutgoingCallsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "outgoing_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No callees found.", result.Content)
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

func TestNavigateReferencesDefaultsToIncludingDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	// No include_declaration key: references must keep its long-standing default
	// of asking the server to include the symbol's declaration.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.True(t, src.lastIncludeDecl, "include_declaration should default to true")
}

func TestNavigateReferencesExcludesDeclarationWhenAsked(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	// An explicit include_declaration:false must reach the source so only use
	// sites (not the declaration) are requested.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references", "include_declaration": false,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.False(t, src.lastIncludeDecl, "include_declaration:false should be passed through")
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

func TestNavigateTruncatesWideSourceLine(t *testing.T) {
	dir := t.TempDir()
	// A single very wide line, as found in a minified/generated file. The snippet
	// must be clipped so it stays a one-line annotation rather than flooding the
	// output with the whole line.
	wide := strings.Repeat("x", navigateSnippetMax+500)
	content := "package main\nvar data = \"" + wide + "\"\n"
	path := writeNavFileContent(t, dir, content)
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 4}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 2, "action": "definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The clip marker is present and the full wide line is not, so the snippet
	// cannot dominate the output budget.
	require.Contains(t, result.Content, "characters truncated]")
	require.NotContains(t, result.Content, wide)
	// The rendered snippet stays bounded: at most the cap plus the short marker,
	// far below the original line's width.
	require.Less(t, len(result.Content), navigateSnippetMax+200)
}

func TestNavigateReferencesCapsLongList(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	// More distinct sites than the cap, so the rendered body is truncated while
	// the header still reports the true total.
	total := navigateLocationCap + 25
	locs := make([]lsp.Location, 0, total)
	for i := 0; i < total; i++ {
		locs = append(locs, lsp.Location{Path: path, Range: lsp.Range{Start: lsp.Position{Line: i, Character: 0}}})
	}
	src := &fakeNavigate{references: locs}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The header counts every distinct reference, not just the shown ones.
	require.True(t, strings.HasPrefix(result.Content, "225 references across 1 file:\n"), result.Content)
	// Exactly navigateLocationCap entries are rendered, followed by the elision
	// notice, so the body never floods the context with the full list.
	require.Contains(t, result.Content, "... and 25 more (225 total) not shown")
	require.Equal(t, navigateLocationCap, strings.Count(result.Content, "main.go:"))
}

func TestNavigateReferencesNoNoticeAtCapBoundary(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	// Exactly the cap: every entry fits, so no truncation notice is emitted.
	locs := make([]lsp.Location, 0, navigateLocationCap)
	for i := 0; i < navigateLocationCap; i++ {
		locs = append(locs, lsp.Location{Path: path, Range: lsp.Range{Start: lsp.Position{Line: i, Character: 0}}})
	}
	src := &fakeNavigate{references: locs}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotContains(t, result.Content, "not shown")
	require.Equal(t, navigateLocationCap, strings.Count(result.Content, "main.go:"))
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

func TestNavigateSignatureReturnsText(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{signature: "→ Run(ctx context.Context, name string)\n    active parameter: name string\n"}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5, "column": 9, "action": "signature",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "→ Run(ctx context.Context, name string)\n    active parameter: name string", result.Content)
	// 1-based coordinates are converted to the 0-based LSP positions.
	require.Equal(t, 4, src.lastLine)
	require.Equal(t, 8, src.lastCol)
}

func TestNavigateHoverCapsLongText(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	// A verbose server hover: far more lines than the byte cap allows. Each line
	// is short, so the cut lands on a line boundary and the elision notice counts
	// the dropped lines.
	var sb strings.Builder
	const lines = 800 // ~10 bytes/line > navigateHoverByteCap
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "field%03d int\n", i)
	}
	src := &fakeNavigate{hover: sb.String()}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "hover",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The content stays within the cap plus the one-line notice, never the whole
	// multi-kilobyte hover.
	require.LessOrEqual(t, len(result.Content), navigateHoverByteCap+64)
	require.Contains(t, result.Content, "lines truncated]")
	// The cut falls on a line boundary, so the last shown field line is intact
	// rather than chopped mid-token.
	require.Contains(t, result.Content, "field000 int")
	// The notice's count plus the shown lines reconstruct the true total.
	var shown, elided int
	_, perr := fmt.Sscanf(result.Content[strings.LastIndex(result.Content, "... ["):], "... [%d more lines truncated]", &elided)
	require.NoError(t, perr)
	shown = strings.Count(result.Content[:strings.LastIndex(result.Content, "\n... [")], "\n") + 1
	require.Equal(t, lines, shown+elided)
}

func TestNavigateHoverShortTextUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{hover: "type T struct{ X int }"}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "hover",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Text within the cap is passed through verbatim, with no truncation notice.
	require.Equal(t, "type T struct{ X int }", result.Content)
	require.NotContains(t, result.Content, "truncated]")
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
		{"signature", "No signature help found."},
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

func TestNavigateImplementationSingleNoHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{implementation: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 4, Character: 2}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "implementation",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// A single implementation result must not grow a summary header — it just
	// shows the coordinate (and source snippet) so the output stays minimal,
	// matching the single-result definition/declaration behaviour.
	require.Equal(t, "main.go:5:3", result.Content)
	require.NotContains(t, result.Content, "implementation")
}

func TestNavigateImplementationMultiFileHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	other := filepath.Join(dir, "other.go")
	require.NoError(t, os.WriteFile(other, []byte("package main\n"), 0o644))
	src := &fakeNavigate{implementation: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 5, Character: 1}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "implementation",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Three implementations across two files: both count nouns are plural.
	require.True(t, strings.HasPrefix(result.Content, "3 implementations across 2 files:\n"), result.Content)
}

func TestNavigateDefinitionMultipleResultsShowsHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	other := filepath.Join(dir, "other.go")
	require.NoError(t, os.WriteFile(other, []byte("package main\n"), 0o644))
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Multiple definitions (overloads / ambiguous symbol) produce the same
	// count header as references and calls, so the model sees the scope first.
	require.True(t, strings.HasPrefix(result.Content, "2 definitions across 2 files:\n"), result.Content)
}

func TestNavigateDeclarationMultipleResultsShowsHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{declaration: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 5, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "declaration",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.True(t, strings.HasPrefix(result.Content, "2 declarations across 1 file:\n"), result.Content)
}

func TestNavigateTypeDefinitionMultipleResultsShowsHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{typeDefinition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 0}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 2, "action": "type_definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// "type definition" (two words) pluralises to "type definitions".
	require.True(t, strings.HasPrefix(result.Content, "2 type definitions across 1 file:\n"), result.Content)
}

func TestNavigateRejectsMalformedJSON(t *testing.T) {
	tool := &navigateTool{source: &fakeNavigate{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid navigate arguments")
}

func TestNavigateDefinitionHasLocationsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{definition: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 4}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "definition",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotNil(t, result.Metadata)
	locs, ok := result.Metadata[MetadataLocations].([]navigateLocation)
	require.True(t, ok, "MetadataLocations must be []navigateLocation")
	require.Len(t, locs, 1)
	require.Equal(t, "main.go", locs[0].Path)
	require.Equal(t, 3, locs[0].Line)
	require.Equal(t, 5, locs[0].Column)
	total, ok := result.Metadata[MetadataTotal].(int)
	require.True(t, ok)
	require.Equal(t, 1, total)
}

func TestNavigateReferencesHasLocationsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	other := filepath.Join(dir, "other.go")
	require.NoError(t, os.WriteFile(other, []byte("package main\n"), 0o644))
	src := &fakeNavigate{references: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		{Path: other, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 1}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}}, // duplicate
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "references",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	locs, ok := result.Metadata[MetadataLocations].([]navigateLocation)
	require.True(t, ok)
	// Duplicate is deduplicated: 2 distinct locations, sorted by path then line.
	require.Len(t, locs, 2)
	total, ok := result.Metadata[MetadataTotal].(int)
	require.True(t, ok)
	require.Equal(t, 2, total)
	// Sorted: main.go < other.go alphabetically.
	require.Equal(t, "main.go", locs[0].Path)
	require.Equal(t, 1, locs[0].Line)
	require.Equal(t, "other.go", locs[1].Path)
	require.Equal(t, 3, locs[1].Line)
}

func TestNavigateIncomingCallsHasLocationsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	src := &fakeNavigate{incomingCalls: []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 5, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 9, Character: 3}}},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "incoming_calls",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	locs, ok := result.Metadata[MetadataLocations].([]navigateLocation)
	require.True(t, ok)
	require.Len(t, locs, 2)
	require.Equal(t, 6, locs[0].Line)
	require.Equal(t, 10, locs[1].Line)
}

func TestNavigateHoverHasPositionMetadata(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{hover: "func Foo() int\n"}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 7, "column": 3, "action": "hover",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotNil(t, result.Metadata)
	require.Equal(t, "main.go", result.Metadata["path"])
	require.Equal(t, 7, result.Metadata["line"])
	require.Equal(t, 3, result.Metadata["column"])
}

func TestNavigateSignatureHasPositionMetadata(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{signature: "→ Foo(x int)\n"}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 4, "column": 12, "action": "signature",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "main.go", result.Metadata["path"])
	require.Equal(t, 4, result.Metadata["line"])
	require.Equal(t, 12, result.Metadata["column"])
}

func TestNavigateEmptyResultHasNoMetadata(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{}
	tool := &navigateTool{source: src, workDir: dir}

	for _, action := range []string{"definition", "declaration", "type_definition", "implementation", "references", "incoming_calls", "outgoing_calls"} {
		result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
			"path": "main.go", "line": 1, "action": action,
		}))
		require.NoError(t, err, "action %s", action)
		require.False(t, result.IsError, "action %s", action)
		require.Nil(t, result.Metadata, "action %s should have no metadata on empty result", action)
	}
}

func TestNavigatePrepareRenameRenamable(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{prepareRename: &lsp.PrepareRenameResult{
		Range: lsp.Range{
			Start: lsp.Position{Line: 4, Character: 5},
			End:   lsp.Position{Line: 4, Character: 11},
		},
		Placeholder: "myFunc",
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 5, "column": 6, "action": "prepare_rename",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// 1-based input is converted to 0-based for the LSP call.
	require.Equal(t, 4, src.lastLine)
	require.Equal(t, 5, src.lastCol)
	// Result names the symbol and range in 1-based coordinates.
	require.Contains(t, result.Content, `"myFunc"`)
	require.Contains(t, result.Content, "main.go:5:6-5:12")
	require.Contains(t, result.Content, "can be renamed")
	// Metadata carries structured fields for downstream consumers.
	require.Equal(t, true, result.Metadata["renamable"])
	require.Equal(t, "myFunc", result.Metadata["placeholder"])
	require.Equal(t, 5, result.Metadata["range_start_line"])
	require.Equal(t, 6, result.Metadata["range_start_column"])
	require.Equal(t, 5, result.Metadata["range_end_line"])
	require.Equal(t, 12, result.Metadata["range_end_column"])
}

func TestNavigatePrepareRenameNotRenamable(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	// A nil PrepareRenameResult means the server says this position is not renamable.
	src := &fakeNavigate{prepareRename: nil}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 3, "action": "prepare_rename",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "cannot be renamed")
}

func TestNavigatePrepareRenameDefaultBehavior(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	// Server returns {defaultBehavior: true}: rename is possible but the server
	// will use the word under the cursor with no explicit range.
	src := &fakeNavigate{prepareRename: &lsp.PrepareRenameResult{DefaultBehavior: true}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 2, "column": 4, "action": "prepare_rename",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "can be renamed")
	require.Contains(t, result.Content, "word under cursor")
	require.Equal(t, true, result.Metadata["renamable"])
	require.Equal(t, true, result.Metadata["default_behavior"])
}

func TestNavigatePrepareRenameNoPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	// Server returns only a range (no placeholder): symbol is renamable, but the
	// name is implied by the text at the returned range.
	src := &fakeNavigate{prepareRename: &lsp.PrepareRenameResult{
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 8},
			End:   lsp.Position{Line: 0, Character: 12},
		},
	}}
	tool := &navigateTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "column": 9, "action": "prepare_rename",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "can be renamed")
	// No placeholder text in the message (server sent bare range only).
	require.NotContains(t, result.Content, `"`)
	require.Equal(t, true, result.Metadata["renamable"])
	require.Nil(t, result.Metadata["placeholder"])
}

func TestNavigatePrepareRenamePropagatesError(t *testing.T) {
	dir := t.TempDir()
	writeNavFile(t, dir)
	src := &fakeNavigate{prepRenErr: errors.New("server unavailable")}
	tool := &navigateTool{source: src, workDir: dir}

	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "action": "prepare_rename",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "checking rename")
}

func TestNavigateLocationsMetadataDeduplicates(t *testing.T) {
	dir := t.TempDir()
	path := writeNavFile(t, dir)
	// Two identical locations plus one distinct one.
	locs := []lsp.Location{
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 0}}},
		{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 2}}},
	}
	meta := navigateLocationsMetadata(dir, locs)
	out, ok := meta[MetadataLocations].([]navigateLocation)
	require.True(t, ok)
	require.Len(t, out, 2, "duplicate location must be removed")
	require.Equal(t, 2, meta[MetadataTotal])
}
