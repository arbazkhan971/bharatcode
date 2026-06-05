package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/stretchr/testify/require"
)

// writeFile creates parents and writes a tiny source file at root/rel.
func writeDiagFile(t *testing.T, root, rel string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("package main\n"), 0o644))
	return full
}

// TestDiagnosticFilesSkipsIgnoredDirs asserts the workspace scan descends into
// real source directories but skips the dependency/build directories grep and
// glob already ignore, so the language servers are never asked to analyse
// vendored or generated code.
func TestDiagnosticFilesSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()

	want := writeDiagFile(t, root, "main.go")
	nested := writeDiagFile(t, root, "pkg/util.go")
	// Each of these lives under a directory grep's ignoredDirs already skips.
	writeDiagFile(t, root, "node_modules/dep/index.go")
	writeDiagFile(t, root, "vendor/lib/lib.go")
	writeDiagFile(t, root, "dist/bundle.go")
	writeDiagFile(t, root, ".git/hooks/hook.go")

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{want, nested}, got)
}

// TestDiagnosticFilesHonorsRootGitignore asserts that a directory named in the
// root .gitignore (here Rust's target/, which is not in the built-in set) is
// also skipped, matching grep's loadRootGitignore behaviour.
func TestDiagnosticFilesHonorsRootGitignore(t *testing.T) {
	root := t.TempDir()

	want := writeDiagFile(t, root, "main.go")
	writeDiagFile(t, root, "target/debug/build.go")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("target/\n"), 0o644))

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}

// TestDiagnosticsSummaryHeaderTalliesSeverities asserts the rendered result
// opens with a one-line summary counting the total, the distinct files, and the
// per-severity breakdown (errors and warnings here), in severity order.
func TestDiagnosticsSummaryHeaderTalliesSeverities(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(a, []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: a, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom"},
			{Path: a, Range: lsp.Range{Start: lsp.Position{Line: 1}}, Severity: lsp.Warning, Message: "meh"},
			{Path: b, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "bang"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "a.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The fake returns the same set for every queried path; a single-path query
	// therefore reports the full three-diagnostic tally across both files.
	require.Contains(t, result.Content, "3 diagnostics across 2 files (2 errors, 1 warning):")
	require.Equal(t, 3, result.Metadata[MetadataDiagnosticCount])
	require.Equal(t, 2, result.Metadata[MetadataDiagnosticErrors])
	require.Equal(t, 1, result.Metadata[MetadataDiagnosticWarnings])
}

// TestDiagnosticsSummarySingularGrammar checks the header uses singular nouns
// for a lone diagnostic in a single file.
func TestDiagnosticsSummarySingularGrammar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.Contains(t, result.Content, "1 diagnostic across 1 file (1 error):")
}

// TestDiagnosticsSeverityFilterDropsLessSevere asserts that a "warning" filter
// keeps errors and warnings but drops the info/hint diagnostics, and that the
// summary and metadata reflect only the surviving set.
func TestDiagnosticsSeverityFilterDropsLessSevere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom"},
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 1}}, Severity: lsp.Warning, Message: "meh"},
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2}}, Severity: lsp.Information, Message: "fyi"},
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 3}}, Severity: lsp.Hint, Message: "tip"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go", "severity": "warning"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "2 diagnostics across 1 file (1 error, 1 warning):")
	require.Contains(t, result.Content, "boom")
	require.Contains(t, result.Content, "meh")
	require.NotContains(t, result.Content, "fyi")
	require.NotContains(t, result.Content, "tip")
	require.Equal(t, 2, result.Metadata[MetadataDiagnosticCount])
}

// TestDiagnosticsSeverityFilterEmptyMessage checks that when the filter removes
// every diagnostic, the tool reports the threshold rather than the unfiltered
// "No diagnostics found." message.
func TestDiagnosticsSeverityFilterEmptyMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Warning, Message: "meh"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go", "severity": "error"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "No diagnostics at or above error severity.", result.Content)
}

// TestDiagnosticsSeverityFilterRejectsUnknown asserts an unrecognized severity
// value is a tool error rather than a silent no-op.
func TestDiagnosticsSeverityFilterRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	tool := &diagnosticsTool{source: fakeDiagnostics{}, workDir: dir}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"severity": "fatal"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "unknown severity")
}

// TestFilterBySeverityKeepsUnclassified pins that a diagnostic whose severity is
// outside the Error..Hint range (a server that omitted it) survives any filter,
// since it cannot be safely judged below the threshold.
func TestFilterBySeverityKeepsUnclassified(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Path: "a.go", Severity: lsp.Hint, Message: "tip"},
		{Path: "a.go", Severity: 0, Message: "unset"},
	}
	got := filterBySeverity(diags, lsp.Error)
	require.Len(t, got, 1)
	require.Equal(t, "unset", got[0].Message)
}

// TestDiagnosticsSummaryHeaderHelper exercises diagnosticsSummary directly so the
// severity ordering and the omission of zero-count categories are pinned without
// routing through the language-server plumbing.
func TestDiagnosticsSummaryHeaderHelper(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Path: "x.go", Severity: lsp.Hint, Message: "h"},
		{Path: "x.go", Severity: lsp.Error, Message: "e"},
	}
	got := diagnosticsSummary(diags, severityCounts(diags))
	require.Equal(t, "2 diagnostics across 1 file (1 error, 1 hint):", got)
}

// TestDiagnosticsShowsOffendingSourceLine asserts the rendered output places the
// trimmed source line at fault indented beneath each diagnostic, so the model
// sees the code without a separate view, and that a diagnostic pointing past the
// end of the file degrades to the message line alone.
func TestDiagnosticsShowsOffendingSourceLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n\nfunc main() { undefined() }\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 13}}, Severity: lsp.Error, Message: "undefined: undefined"},
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 9, Character: 0}}, Severity: lsp.Warning, Message: "phantom"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The error's source line is shown, trimmed and indented, on its own line.
	require.Contains(t, result.Content, "main.go:3:14: error: undefined: undefined")
	require.Contains(t, result.Content, "\n    func main() { undefined() }\n")
	// The out-of-range warning falls back to the message line with no snippet.
	require.Contains(t, result.Content, "main.go:10:1: warning: phantom")
	require.NotContains(t, result.Content, "    phantom")
}

func TestDiagnosticsShowsRelatedInformation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n\nvar x = 1\nvar x = 2\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{
				Path:     path,
				Range:    lsp.Range{Start: lsp.Position{Line: 3, Character: 4}},
				Severity: lsp.Error,
				Message:  "x redeclared in this block",
				Related: []lsp.RelatedInformation{{
					Location: lsp.Location{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 4}}},
					Message:  "other declaration of x",
				}},
			},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The related location is rendered indented beneath the diagnostic, with a
	// workspace-relative path and 1-based coordinates.
	require.Contains(t, result.Content, "    related: main.go:3:5: other declaration of x")
}

func TestDiagnosticsShowsTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n\nimport \"fmt\"\n\nfunc main() { OldAPI() }\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{
				Path:     path,
				Range:    lsp.Range{Start: lsp.Position{Line: 2, Character: 7}},
				Severity: lsp.Hint,
				Message:  "imported and not used",
				Source:   "gopls",
				Tags:     []lsp.DiagnosticTag{lsp.Unnecessary},
			},
			{
				Path:     path,
				Range:    lsp.Range{Start: lsp.Position{Line: 4, Character: 13}},
				Severity: lsp.Warning,
				Message:  "OldAPI is deprecated",
				Tags:     []lsp.DiagnosticTag{lsp.Deprecated},
			},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Tags are rendered as angle-bracketed labels on the diagnostic line.
	require.Contains(t, result.Content, "main.go:3:8: hint: imported and not used (gopls) <unnecessary>")
	require.Contains(t, result.Content, "main.go:5:14: warning: OldAPI is deprecated <deprecated>")
}

// TestDiagnosticFilesScansRootNamedLikeIgnored guards the path != root exception:
// when the workspace root itself is named like an ignored directory, its files
// are still scanned rather than the whole tree being skipped.
func TestDiagnosticFilesScansRootNamedLikeIgnored(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "node_modules")
	want := writeDiagFile(t, root, "main.go")

	got, err := diagnosticFiles(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}
