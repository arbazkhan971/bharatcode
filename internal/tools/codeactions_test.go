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

type fakeCodeActions struct {
	actions []lsp.CodeAction
	err     error

	// resolved is the action ResolveCodeAction returns; resolveErr the error it
	// returns. resolveCalls counts how many times ResolveCodeAction was invoked.
	resolved     lsp.CodeAction
	resolveErr   error
	resolveCalls int

	lastPath  string
	lastRange lsp.Range
	lastOnly  []string
}

func (f *fakeCodeActions) CodeActions(_ context.Context, file string, rng lsp.Range, only []string) ([]lsp.CodeAction, error) {
	f.lastPath, f.lastRange, f.lastOnly = file, rng, only
	return f.actions, f.err
}

func (f *fakeCodeActions) ResolveCodeAction(_ context.Context, _ string, _ lsp.CodeAction) (lsp.CodeAction, error) {
	f.resolveCalls++
	return f.resolved, f.resolveErr
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

func TestCodeActionsListNotesResolvableAction(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	// gopls/rust-analyzer list refactorings with an empty Edit and resolve data;
	// the listing must flag them as applyable rather than leaving an empty note.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Extract function", Kind: "refactor.extract", Data: json.RawMessage(`{"title":"Extract function"}`)},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"1. Extract function [refactor.extract] (resolve to apply)",
		result.Content,
	)
}

func TestCodeActionsMarksPreferredAndDisabled(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	// A server marks the canonical fix preferred and may disable an action it
	// cannot offer in this context; the listing surfaces both so the model can
	// pick the recommended fix and skip the unavailable one.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Import \"fmt\"", Kind: "quickfix", IsPreferred: true, Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{filepath.Join(dir, "main.go"): {{NewText: "x"}}},
		}},
		{Title: "Extract function", Kind: "refactor.extract", Disabled: "selection spans a statement boundary"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"1. Import \"fmt\" [quickfix] (preferred) (edit, 1 file)\n"+
			"2. Extract function [refactor.extract] (disabled: selection spans a statement boundary)",
		result.Content,
	)
}

func TestCodeActionsApplyRefusesDisabledAction(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Extract function", Kind: "refactor.extract", Disabled: "selection spans a statement boundary", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{filepath.Join(dir, "main.go"): {{NewText: "x"}}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "the server disabled it (selection spans a statement boundary)")
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

func TestCodeActionsApplyWritesEditAndDiffs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package widget\n", string(got))

	require.Contains(t, result.Content, `applied "Organize Imports"`)
	require.Contains(t, result.Content, "main.go (1 edit(s))")
	// A unified diff of the change is surfaced, like the rename/edit tools.
	require.Contains(t, result.Content, "-package main")
	require.Contains(t, result.Content, "+package widget")
	require.Equal(t, "Organize Imports", result.Metadata["applied"])
}

func TestCodeActionsApplyWarnsAboutUnappliedResourceOps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// A "move to new file" refactor bundles a create-file operation with the text
	// edits. The text edit is applied, but the file creation is not, so the result
	// must warn rather than imply the refactor is complete.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Move to new file", Kind: "refactor.move", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
			ResourceOps: []lsp.ResourceOperation{{Kind: "create", Path: filepath.Join(dir, "moved.go")}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package widget\n", string(got))

	require.Contains(t, result.Content, "file operation(s) the server requested were NOT applied")
	require.Contains(t, result.Content, "create moved.go")
	require.Equal(t, 1, result.Metadata["resource_ops"])
}

func TestCodeActionsApplyReportsResourceOpsWhenNoTextEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// An action that resolves to only a file operation has no text edits to apply,
	// but the model must still learn what the server wanted to do.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Delete file", Kind: "refactor", Edit: lsp.WorkspaceEdit{
			ResourceOps: []lsp.ResourceOperation{{Kind: "delete", Path: path}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "No text edits were applied")
	require.Contains(t, result.Content, "delete main.go")
	require.Equal(t, 1, result.Metadata["resource_ops"])

	// The file is left in place.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\n", string(got))
}

func TestCodeActionsApplyResolvesEditlessAction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// The server lists an action without an edit but with resolve data, as gopls
	// does for refactorings; the edit is computed on codeAction/resolve.
	src := &fakeCodeActions{
		actions: []lsp.CodeAction{
			{Title: "Extract function", Kind: "refactor.extract", Data: json.RawMessage(`{"title":"Extract function"}`)},
		},
		resolved: lsp.CodeAction{Title: "Extract function", Kind: "refactor.extract", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 1, src.resolveCalls, "an editless action should be resolved before applying")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package widget\n", string(got))
	require.Contains(t, result.Content, `applied "Extract function"`)
}

func TestCodeActionsApplyResolveFailureFallsThrough(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	// Resolve fails (e.g. the server does not implement codeAction/resolve), so
	// the action is still treated as carrying no edits rather than crashing.
	src := &fakeCodeActions{
		actions: []lsp.CodeAction{
			{Title: "Extract function", Kind: "refactor.extract", Data: json.RawMessage(`{"title":"Extract function"}`)},
		},
		resolveErr: errors.New("resolve unsupported"),
	}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, 1, src.resolveCalls)
	require.Contains(t, result.Content, "no edits")
}

func TestCodeActionsApplyEditPresentSkipsResolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// An action that already carries an edit must be applied directly, never
	// round-tripped through resolve.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Data: json.RawMessage(`{"title":"Organize Imports"}`), Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 0, src.resolveCalls, "an action with an edit should not be resolved")
}

func TestCodeActionsPreviewShowsDiffWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}}
	// A diagnoser is wired but must not run for a preview: nothing was written.
	tool := &codeActionsTool{
		source:  src,
		workDir: dir,
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(path, 0, 0, lsp.Error, "package name mismatch"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1, "preview": true,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The file on disk is untouched.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\n", string(got))

	// The diff is still surfaced, marked as a preview, with no diagnostics re-check.
	require.Contains(t, result.Content, `preview of "Organize Imports"`)
	require.Contains(t, result.Content, "nothing written")
	require.Contains(t, result.Content, "-package main")
	require.Contains(t, result.Content, "+package widget")
	require.NotContains(t, result.Content, "package name mismatch")
	require.Equal(t, true, result.Metadata["preview"])
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestCodeActionsPreviewSkipsPermissionCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 12}},
				NewText: "package widget",
			}}},
		}},
	}}
	// A permission policy that denies the codeactions tool outright: an applying
	// action would be blocked, but a preview writes nothing and so never consults
	// it.
	cfg := &config.Config{}
	cfg.Permissions.Deny = []string{"codeactions"}
	tool := &codeActionsTool{source: src, workDir: dir, deps: Dependencies{WorkDir: dir, Permission: permission.New(cfg, nil)}}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1, "preview": true,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, `preview of "Organize Imports"`)

	// Disk is untouched despite no permission grant.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package main\n", string(got))

	// The same action without preview is denied, confirming the policy is live.
	denied, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Contains(t, denied.Content, "permission denied")
}

func TestCodeActionsApplySurfacesPostWriteDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}}
	tool := &codeActionsTool{
		source:  src,
		workDir: dir,
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(path, 0, 0, lsp.Error, "package name mismatch"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The applied edit lands and its diff is shown, then the re-check surfaces the
	// error the action introduced, matching the edit/write/rename tools.
	require.Contains(t, result.Content, `applied "Organize Imports"`)
	require.Contains(t, result.Content, "package name mismatch")
	require.Contains(t, result.Content, "please fix")
	require.Contains(t, result.Metadata["diagnostics"], "package name mismatch")
}

func TestCodeActionsApplyOmitsDiagnosticsWhenClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 0, Character: 12},
				},
				NewText: "package widget",
			}}},
		}},
	}}
	// Only hint-level diagnostics: nothing actionable should be appended.
	tool := &codeActionsTool{
		source:  src,
		workDir: dir,
		diag: &fakeDiagnoser{diags: []lsp.Diagnostic{
			diag(path, 0, 0, lsp.Hint, "consider simplifying"),
		}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotContains(t, result.Content, "Diagnostics after editing")
	require.Nil(t, result.Metadata["diagnostics"])
}

func TestCodeActionsApplyRejectsCommandOnlyAction(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Run go generate", Kind: "source", Command: &lsp.Command{Command: "gopls.generate"}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 1,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "server-side command")
	require.Contains(t, result.Content, "gopls.generate")
}

func TestCodeActionsApplyIndexOutOfRange(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{{Title: "Quick Fix", Kind: "quickfix"}}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 5,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "only 1 action(s) available")
}

func TestCodeActionsApplyIndexMatchesListingOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// Listing sorts by kind: "command" action (kind "source") sorts before the
	// edit (kind "source.organizeImports"), so the edit is index 2.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 12}},
				NewText: "package widget",
			}}},
		}},
		{Title: "Run go generate", Kind: "source", Command: &lsp.Command{Command: "gopls.generate"}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "apply": 2,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, `applied "Organize Imports"`)
}

func TestCodeActionsKindFilterListsMatchingSubkinds(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports"},
		{Title: "Remove unused", Kind: "quickfix"},
		{Title: "Extract function", Kind: "refactor.extract.function"},
		{Title: "Run go generate", Kind: "source"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	// "source" admits the bare kind and its "source.organizeImports" sub-kind, but
	// not "quickfix" or "refactor.*".
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "kind": "source",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t,
		"1. Run go generate [source]\n"+
			"2. Organize Imports [source.organizeImports]",
		result.Content,
	)
}

func TestCodeActionsKindFilterForwardedAsOnly(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Organize Imports", Kind: "source.organizeImports"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	// A kind filter is passed through to the server as the request's "only"
	// restriction so it computes whole-file source.* actions it would otherwise
	// gate behind an explicit request.
	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "kind": "source",
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"source"}, src.lastOnly)
}

func TestCodeActionsNoKindRequestsEveryAction(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Remove unused", Kind: "quickfix"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	// Without a kind filter no "only" restriction is sent, so the server offers
	// every available action.
	_, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1,
	}))
	require.NoError(t, err)
	require.Nil(t, src.lastOnly)
}

func TestCodeActionsKindFilterIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Extract function", Kind: "Refactor.Extract"},
		{Title: "Remove unused", Kind: "quickfix"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "kind": "refactor",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "1. Extract function [Refactor.Extract]", result.Content)
}

func TestCodeActionsKindFilterReportsWhenNoneMatch(t *testing.T) {
	dir := t.TempDir()
	writeCodeActionsFile(t, dir)
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Remove unused", Kind: "quickfix"},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "kind": "refactor",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, `No code actions of kind "refactor" available.`, result.Content)
}

func TestCodeActionsKindFilterAppliesIntoFilteredOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	// Unfiltered, the quickfix would sort before the organize-imports edit. With
	// kind=source the quickfix drops out, so apply index 1 is the edit.
	src := &fakeCodeActions{actions: []lsp.CodeAction{
		{Title: "Remove unused", Kind: "quickfix"},
		{Title: "Organize Imports", Kind: "source.organizeImports", Edit: lsp.WorkspaceEdit{
			Changes: map[string][]lsp.TextEdit{path: {{
				Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 12}},
				NewText: "package widget",
			}}},
		}},
	}}
	tool := &codeActionsTool{source: src, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "main.go", "line": 1, "kind": "source", "apply": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, `applied "Organize Imports"`)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "package widget\n", string(got))
}

func TestCodeActionsRejectsMalformedJSON(t *testing.T) {
	tool := &codeActionsTool{source: &fakeCodeActions{}, workDir: t.TempDir()}
	result, err := tool.Run(context.Background(), json.RawMessage(`{bad`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid codeactions arguments")
}
