package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	got, err := diagnosticFiles(context.Background(), root, nil)
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

	got, err := diagnosticFiles(context.Background(), root, nil)
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

// TestDiagnosticsCapsRenderedEntries asserts that when a scan surfaces more than
// diagnosticMatchCap diagnostics, only the cap is rendered in full, a trailing
// "... and N more not shown" notice records what was elided, and the summary
// header plus metadata still report the true total — mirroring the navigate and
// symbols tools' bounded-output behaviour.
func TestDiagnosticsCapsRenderedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	total := diagnosticMatchCap + 25
	items := make([]lsp.Diagnostic, total)
	for i := range items {
		items[i] = lsp.Diagnostic{
			Path:     path,
			Range:    lsp.Range{Start: lsp.Position{Line: i}},
			Severity: lsp.Error,
			Message:  fmt.Sprintf("boom %d", i),
		}
	}
	tool := &diagnosticsTool{source: fakeDiagnostics{items: items}, workDir: dir}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The header and metadata report every diagnostic, even the elided ones.
	require.Contains(t, result.Content, fmt.Sprintf("%d diagnostics across 1 file", total))
	require.Equal(t, total, result.Metadata[MetadataDiagnosticCount])

	// Exactly diagnosticMatchCap message lines are rendered; the rest are elided.
	rendered := strings.Count(result.Content, ": error: boom ")
	require.Equal(t, diagnosticMatchCap, rendered)
	require.Contains(t, result.Content, fmt.Sprintf("... and %d more not shown (%d total)", total-diagnosticMatchCap, total))

	// The first diagnostic survives the cut; one past the cap does not.
	require.Contains(t, result.Content, "boom 0\n")
	require.NotContains(t, result.Content, fmt.Sprintf("boom %d\n", diagnosticMatchCap))
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

func TestDiagnosticsShowsCodeDescriptionHref(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.js")
	require.NoError(t, os.WriteFile(path, []byte("console.log('x')\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{
				Path:     path,
				Range:    lsp.Range{Start: lsp.Position{Line: 0, Character: 0}},
				Severity: lsp.Warning,
				Message:  "Unexpected console statement.",
				Source:   "eslint",
				Code:     "no-console",
				CodeHref: "https://eslint.org/docs/latest/rules/no-console",
			},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "app.js"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// The rule code, source, and a "see <url>" documentation link all render on
	// the diagnostic line.
	require.Contains(t, result.Content, "app.js:1:1: warning: Unexpected console statement. [no-console] (eslint) see https://eslint.org/docs/latest/rules/no-console")
}

// TestDiagnosticFilesScansRootNamedLikeIgnored guards the path != root exception:
// when the workspace root itself is named like an ignored directory, its files
// are still scanned rather than the whole tree being skipped.
func TestDiagnosticFilesScansRootNamedLikeIgnored(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "node_modules")
	want := writeDiagFile(t, root, "main.go")

	got, err := diagnosticFiles(context.Background(), root, nil)
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}

// TestDiagnosticFilesHonorsExtensionSet asserts the scan opens only files whose
// extension is in the supplied set, so the language set the LSP manager reports
// drives which files are analysed rather than a fixed list.
func TestDiagnosticFilesHonorsExtensionSet(t *testing.T) {
	root := t.TempDir()
	want := writeDiagFile(t, root, "lib.rs")
	writeDiagFile(t, root, "main.go") // outside the requested set; must be skipped

	got, err := diagnosticFiles(context.Background(), root, map[string]struct{}{".rs": {}})
	require.NoError(t, err)
	require.Equal(t, []string{want}, got)
}

// TestDiagnosticFilesNilExtensionsFallsBackToDefaults asserts that passing no
// extension set scans the built-in language extensions, so a direct call (or a
// missing manager) still has a target list rather than matching nothing.
func TestDiagnosticFilesNilExtensionsFallsBackToDefaults(t *testing.T) {
	root := t.TempDir()
	goFile := writeDiagFile(t, root, "main.go")
	tsFile := writeDiagFile(t, root, "app.ts")
	writeDiagFile(t, root, "notes.txt") // not a supported language; must be skipped

	got, err := diagnosticFiles(context.Background(), root, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{goFile, tsFile}, got)
	// The fallback must be derived from the LSP specs, not an independent list.
	require.Contains(t, lsp.DefaultExtensions(), ".go")
}

// diagnosticsByPath is a DiagnosticSource that returns the diagnostics keyed by
// the exact path queried, so a test can assert which files a scan actually
// asked about (unlike fakeDiagnostics, which returns the same items for any
// path). It records every queried path for that assertion.
type diagnosticsByPath struct {
	byPath  map[string][]lsp.Diagnostic
	queried *[]string
}

func (f diagnosticsByPath) Diagnostics(_ context.Context, path string) ([]lsp.Diagnostic, error) {
	if f.queried != nil {
		*f.queried = append(*f.queried, path)
	}
	return f.byPath[path], nil
}

// TestDiagnosticsDirectoryScopesToSubtree asserts that passing a directory path
// scans only the supported files inside that subtree — reporting diagnostics
// from files within it and never opening files in a sibling directory — a middle
// ground between one file and the whole workspace.
func TestDiagnosticsDirectoryScopesToSubtree(t *testing.T) {
	root := t.TempDir()
	inPkg := writeDiagFile(t, root, "pkg/a.go")
	alsoPkg := writeDiagFile(t, root, "pkg/b.go")
	outside := writeDiagFile(t, root, "other/c.go")

	var queried []string
	tool := &diagnosticsTool{
		source: diagnosticsByPath{
			byPath: map[string][]lsp.Diagnostic{
				inPkg:   {{Path: inPkg, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom in a"}},
				alsoPkg: {{Path: alsoPkg, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Warning, Message: "meh in b"}},
				outside: {{Path: outside, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom in c"}},
			},
			queried: &queried,
		},
		workDir: root,
		exts:    map[string]struct{}{".go": {}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "pkg"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Both files in the subtree are reported, rendered workspace-relative.
	require.Contains(t, result.Content, "pkg/a.go:1:1: error: boom in a")
	require.Contains(t, result.Content, "pkg/b.go:1:1: warning: meh in b")
	// The sibling directory's file is neither queried nor reported.
	require.NotContains(t, result.Content, "boom in c")
	require.NotContains(t, result.Content, "other/c.go")
	require.ElementsMatch(t, []string{inPkg, alsoPkg}, queried)
}

// TestDiagnosticsFilePathStillScopesToOneFile asserts the directory branch did
// not change the single-file case: a file path queries only that file, leaving
// its package siblings untouched.
func TestDiagnosticsFilePathStillScopesToOneFile(t *testing.T) {
	root := t.TempDir()
	target := writeDiagFile(t, root, "pkg/a.go")
	sibling := writeDiagFile(t, root, "pkg/b.go")

	var queried []string
	tool := &diagnosticsTool{
		source: diagnosticsByPath{
			byPath: map[string][]lsp.Diagnostic{
				target:  {{Path: target, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "only a"}},
				sibling: {{Path: sibling, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "only b"}},
			},
			queried: &queried,
		},
		workDir: root,
		exts:    map[string]struct{}{".go": {}},
	}

	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "pkg/a.go"}))
	require.NoError(t, err)
	require.Contains(t, result.Content, "only a")
	require.NotContains(t, result.Content, "only b")
	require.Equal(t, []string{target}, queried)
}

// TestDiagnosticsMetadataErrorFilesListsErrorPaths asserts that the error_files
// metadata key holds a sorted, workspace-relative list of files that carry at
// least one error-level diagnostic, omitting files that have only warnings.
// Files outside the workspace are kept as absolute paths.
func TestDiagnosticsMetadataErrorFilesListsErrorPaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	c := filepath.Join(dir, "sub", "c.go")
	for _, p := range []string{a, b, c} {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("package main\n"), 0o644))
	}

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: a, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom in a"},
			{Path: a, Range: lsp.Range{Start: lsp.Position{Line: 1}}, Severity: lsp.Error, Message: "bang in a"},   // duplicate file — counted once
			{Path: b, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Warning, Message: "warn in b"}, // warning only — excluded
			{Path: c, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Error, Message: "boom in c"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	// error_files must list a.go and sub/c.go (workspace-relative, sorted),
	// but not b.go (warnings only).
	errorFiles, ok := result.Metadata[MetadataDiagnosticErrorFiles].([]string)
	require.True(t, ok, "error_files should be []string")
	require.Equal(t, []string{"a.go", "sub/c.go"}, errorFiles)
}

// TestDiagnosticsMetadataErrorFilesAbsentWhenNoErrors asserts the error_files
// key is absent (nil) when no error-level diagnostics are reported, so callers
// can test for nil rather than an empty slice.
func TestDiagnosticsMetadataErrorFilesAbsentWhenNoErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tool := &diagnosticsTool{
		source: fakeDiagnostics{items: []lsp.Diagnostic{
			{Path: path, Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.Warning, Message: "warn"},
		}},
		workDir: dir,
	}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "main.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Nil(t, result.Metadata[MetadataDiagnosticErrorFiles], "error_files must be absent when no errors exist")
}

// TestDiagnosticsMetadataErrorFilesMultipleErrors asserts that a multi-file
// scan produces a sorted, deduplicated error_files list when several files each
// contribute at least one error-level diagnostic.
func TestDiagnosticsMetadataErrorFilesMultipleErrors(t *testing.T) {
	dir := t.TempDir()
	// Three files each with an error; verify the list is sorted and deduplicated.
	paths := []string{
		filepath.Join(dir, "z.go"),
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "m.go"),
	}
	for _, p := range paths {
		require.NoError(t, os.WriteFile(p, []byte("package main\n"), 0o644))
	}

	items := make([]lsp.Diagnostic, len(paths))
	for i, p := range paths {
		items[i] = lsp.Diagnostic{
			Path: p, Range: lsp.Range{Start: lsp.Position{Line: 0}},
			Severity: lsp.Error, Message: "err",
		}
	}
	tool := &diagnosticsTool{source: fakeDiagnostics{items: items}, workDir: dir}
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	errorFiles, ok := result.Metadata[MetadataDiagnosticErrorFiles].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"a.go", "m.go", "z.go"}, errorFiles)
}
