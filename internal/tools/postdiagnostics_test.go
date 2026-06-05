package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/stretchr/testify/require"
)

// fakeDiagnoser is a test double for editDiagnoser. It records the path passed
// to NotifyChange and returns a canned diagnostics slice (or error).
type fakeDiagnoser struct {
	changed []string
	diags   []lsp.Diagnostic
	err     error
}

func (f *fakeDiagnoser) NotifyChange(_ context.Context, path string) error {
	f.changed = append(f.changed, path)
	return nil
}

func (f *fakeDiagnoser) Diagnostics(_ context.Context, _ string) ([]lsp.Diagnostic, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.diags, nil
}

func diag(path string, line, char int, sev lsp.Severity, msg string) lsp.Diagnostic {
	return lsp.Diagnostic{
		Path:     path,
		Range:    lsp.Range{Start: lsp.Position{Line: line, Character: char}},
		Severity: sev,
		Message:  msg,
		Source:   "fakelint",
	}
}

func TestPostWriteDiagnostics_FormatsErrorsAndWarningsAndDropsInfoHint(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "pkg", "main.go")
	src := &fakeDiagnoser{diags: []lsp.Diagnostic{
		diag(path, 4, 1, lsp.Hint, "consider simplifying"),
		diag(path, 2, 5, lsp.Error, "undefined: foo"),
		diag(path, 0, 0, lsp.Warning, "unused import"),
		diag(path, 9, 0, lsp.Information, "doc comment recommended"),
	}}

	note := postWriteDiagnostics(context.Background(), src, workDir, path)

	require.Equal(t, []string{path}, src.changed, "must notify the server the file changed")
	// Error count surfaced and a fix nudge present.
	require.Contains(t, note, "(1 error(s)) — please fix")
	// Relative, slash-normalised path in the header.
	require.Contains(t, note, "editing pkg/main.go")
	// Warning kept; sorted before the error because it is on an earlier line.
	require.Contains(t, note, "pkg/main.go:1:1: warning: unused import (fakelint)")
	require.Contains(t, note, "pkg/main.go:3:6: error: undefined: foo (fakelint)")
	// Info and hint dropped as noise.
	require.NotContains(t, note, "consider simplifying")
	require.NotContains(t, note, "doc comment recommended")
	// Warning line precedes the error line in the output.
	require.Less(t, indexOf(note, "unused import"), indexOf(note, "undefined: foo"))
}

func TestPostWriteDiagnostics_NoErrorsHeaderOmitsFixNudge(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "a.go")
	src := &fakeDiagnoser{diags: []lsp.Diagnostic{
		diag(path, 0, 0, lsp.Warning, "shadowed variable"),
	}}

	note := postWriteDiagnostics(context.Background(), src, workDir, path)
	require.Contains(t, note, "Diagnostics after editing a.go:")
	require.NotContains(t, note, "please fix")
}

func TestPostWriteDiagnostics_NilSourceReturnsEmpty(t *testing.T) {
	require.Empty(t, postWriteDiagnostics(context.Background(), nil, t.TempDir(), "x.go"))
}

func TestPostWriteDiagnostics_OnlyInfoOrHintReturnsEmpty(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "a.go")
	src := &fakeDiagnoser{diags: []lsp.Diagnostic{
		diag(path, 0, 0, lsp.Information, "fyi"),
		diag(path, 1, 0, lsp.Hint, "tip"),
	}}
	require.Empty(t, postWriteDiagnostics(context.Background(), src, workDir, path))
}

func TestPostWriteDiagnostics_DiagnosticsErrorReturnsEmpty(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "a.go")
	src := &fakeDiagnoser{err: context.DeadlineExceeded}
	note := postWriteDiagnostics(context.Background(), src, workDir, path)
	require.Empty(t, note, "a server error must not produce a note (best-effort)")
	require.Equal(t, []string{path}, src.changed, "NotifyChange still runs before the failed probe")
}

func TestPostWriteDiagnostics_CapsOutput(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "a.go")
	var ds []lsp.Diagnostic
	for i := 0; i < maxPostWriteDiagnostics+5; i++ {
		ds = append(ds, diag(path, i, 0, lsp.Error, "boom"))
	}
	src := &fakeDiagnoser{diags: ds}
	note := postWriteDiagnostics(context.Background(), src, workDir, path)
	require.Contains(t, note, "… and 5 more")
}

// TestEditToolSurfacesDiagnostics proves the wiring: a successful edit appends
// the language server's findings to the tool result and metadata.
func TestEditToolSurfacesDiagnostics(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\nvar x = 1\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "diag-edit"})
	tool.diag = &fakeDiagnoser{diags: []lsp.Diagnostic{
		diag(path, 1, 4, lsp.Error, "x declared and not used"),
	}}

	res, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "main.go",
		"old_string": "var x = 1",
		"new_string": "var x = 2",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "edited")
	require.Contains(t, res.Content, "main.go:2:5: error: x declared and not used")
	require.Contains(t, res.Metadata["diagnostics"], "x declared and not used")
}

// TestEditToolNoDiagnoserNoNote proves the common (no LSP) path is unaffected.
func TestEditToolNoDiagnoserNoNote(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "diag-none"})
	res, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "main.go",
		"old_string": "hello",
		"new_string": "namaste",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.NotContains(t, res.Content, "Diagnostics after editing")
	require.Nil(t, res.Metadata["diagnostics"])
}

// TestWriteAndMultiEditSurfaceDiagnostics covers the other two write paths.
func TestWriteAndMultiEditSurfaceDiagnostics(t *testing.T) {
	workDir := t.TempDir()

	wpath := filepath.Join(workDir, "w.go")
	wtool := newWriteTool(Dependencies{WorkDir: workDir, SessionID: "diag-w"})
	wtool.diag = &fakeDiagnoser{diags: []lsp.Diagnostic{diag(wpath, 0, 0, lsp.Error, "syntax error")}}
	wres, err := wtool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "w.go", "content": "package",
	}))
	require.NoError(t, err)
	require.Contains(t, wres.Content, "w.go:1:1: error: syntax error")

	mpath := filepath.Join(workDir, "m.go")
	require.NoError(t, os.WriteFile(mpath, []byte("package main\nfunc a(){}\n"), 0o644))
	mtool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "diag-m"})
	mtool.diag = &fakeDiagnoser{diags: []lsp.Diagnostic{diag(mpath, 1, 0, lsp.Warning, "empty function")}}
	mres, err := mtool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "m.go",
		"edits": []map[string]any{
			{"old": "func a(){}", "new": "func b(){}"},
		},
	}))
	require.NoError(t, err)
	require.Contains(t, mres.Content, "m.go:2:1: warning: empty function")
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
