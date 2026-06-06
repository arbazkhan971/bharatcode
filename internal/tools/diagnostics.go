package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
)

// DiagnosticSource is the LSP capability consumed by the diagnostics
// tool.
type DiagnosticSource interface {
	Diagnostics(ctx context.Context, path string) ([]lsp.Diagnostic, error)
}

type diagnosticsTool struct {
	source  DiagnosticSource
	workDir string
	// exts is the set of file extensions a workspace-wide scan opens, derived
	// from the LSP manager's configured language servers so the two never drift.
	// Empty when no manager is present (Run errors before scanning in that case);
	// diagnosticFiles falls back to the default set defensively.
	exts map[string]struct{}
}

type diagnosticsArgs struct {
	Path     string `json:"path,omitempty"`
	Severity string `json:"severity,omitempty"`
}

var schemaDiagnostics = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {
      "type": "string",
      "description": "Optional path to inspect: a file (just that file) or a directory (every supported source file in that subtree). Omit to scan supported files across the whole workspace."
    },
    "severity": {
      "type": "string",
      "enum": ["error", "warning", "info", "hint"],
      "description": "Optional minimum severity to report: only diagnostics at this level or more severe are shown (error > warning > info > hint). Use \"error\" to focus on what blocks a build. Omit to report every severity."
    }
  }
}`)

//go:embed diagnostics.md
var diagnosticsDescription string

// diagnosticMatchCap bounds how many diagnostics the tool renders in full. A
// workspace-wide scan of a project mid-refactor (or one with a noisy linter) can
// surface thousands of diagnostics; rendering them all — each with its source
// line and related locations — floods the context with little marginal value.
// Capping mirrors the navigate tool's navigateLocationCap and the symbols tool's
// symbolMatchCap bounded-output philosophy: the diagnostics are already sorted
// deterministically, the summary header still reports the true total and
// per-severity breakdown, and a trailing "... and N more not shown" notice
// records what was elided so the model knows the list was truncated rather than
// complete. Narrow the scope with the path or severity argument to see the rest.
const diagnosticMatchCap = 200

func newDiagnosticsTool(deps Dependencies) Tool {
	t := &diagnosticsTool{workDir: deps.WorkDir}
	// A nil *lsp.Manager assigned to the DiagnosticSource interface would
	// produce a non-nil interface wrapping a nil pointer, defeating the
	// t.source == nil guard in Run and panicking on the first method call.
	// Only adopt the source when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
		// Derive the workspace-scan extension set from the manager's configured
		// language servers so adding a language in one place is enough.
		t.exts = extSetFromList(deps.LSP.SupportedExtensions())
	}
	return t
}

// extSetFromList turns a slice of extensions into a lookup set, lowercasing each
// so the scan matches regardless of how the path or spec cased the extension.
func extSetFromList(exts []string) map[string]struct{} {
	set := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		set[strings.ToLower(ext)] = struct{}{}
	}
	return set
}

func (t *diagnosticsTool) Name() string {
	return "diagnostics"
}

func (t *diagnosticsTool) IsReadOnly() bool { return true }

func (t *diagnosticsTool) Description() string {
	return diagnosticsDescription
}

func (t *diagnosticsTool) Schema() json.RawMessage {
	return schemaDiagnostics
}

func (t *diagnosticsTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args diagnosticsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid diagnostics arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("diagnostics are unavailable: no LSP manager configured"), nil
	}

	minSeverity, err := parseSeverityFilter(args.Severity)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	root, err := workspaceRoot(t.workDir)
	if err != nil {
		return Result{}, err
	}

	var paths []string
	if strings.TrimSpace(args.Path) != "" {
		path, err := resolveWorkspacePath(root, args.Path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		// A directory argument scans the supported source files in that subtree —
		// a middle ground between one file and the whole workspace. After editing
		// several files in one package the model can re-check just that package
		// without paying for a workspace-wide walk. A non-directory (a file, or a
		// path that cannot be stat'd) keeps the prior single-path behaviour.
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			paths, err = diagnosticFiles(ctx, path, t.exts)
			if err != nil {
				return Result{}, err
			}
		} else {
			paths = []string{path}
		}
	} else {
		paths, err = diagnosticFiles(ctx, root, t.exts)
		if err != nil {
			return Result{}, err
		}
	}

	var all []lsp.Diagnostic
	for _, path := range paths {
		diagnostics, err := t.source.Diagnostics(ctx, path)
		if err != nil {
			return Result{}, fmt.Errorf("getting diagnostics for %s: %w", path, err)
		}
		all = append(all, diagnostics...)
	}
	if minSeverity != 0 {
		all = filterBySeverity(all, minSeverity)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		if all[i].Range.Start.Line != all[j].Range.Start.Line {
			return all[i].Range.Start.Line < all[j].Range.Start.Line
		}
		return all[i].Message < all[j].Message
	})

	if len(all) == 0 {
		if minSeverity != 0 {
			return Result{Content: fmt.Sprintf("No diagnostics at or above %s severity.", severityString(minSeverity))}, nil
		}
		return Result{Content: "No diagnostics found."}, nil
	}

	counts := severityCounts(all)
	var b strings.Builder
	b.WriteString(diagnosticsSummary(all, counts))
	b.WriteByte('\n')
	// Cache file contents so the offending source line can be shown beneath each
	// diagnostic without re-reading a file once per diagnostic it carries.
	lineCache := map[string][]string{}
	shown := all
	if len(shown) > diagnosticMatchCap {
		shown = shown[:diagnosticMatchCap]
	}
	for _, d := range shown {
		path := d.Path
		if rel, err := filepath.Rel(root, d.Path); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
		fmt.Fprintf(
			&b, "%s:%d:%d: %s: %s",
			path,
			d.Range.Start.Line+1,
			d.Range.Start.Character+1,
			severityString(d.Severity),
			d.Message,
		)
		b.WriteString(diagnosticTail(d))
		b.WriteByte('\n')
		// Surface the offending source line indented beneath the message so the
		// model sees the code at fault without a separate view, matching how the
		// navigate tool and goose/opencode shape location results. Omitted when the
		// file or line cannot be read.
		if snippet := sourceLine(lineCache, d.Path, d.Range.Start.Line); snippet != "" {
			b.WriteString("    ")
			b.WriteString(snippet)
			b.WriteByte('\n')
		}
		// Surface any related locations the server linked to this diagnostic (the
		// conflicting declaration, the import's use site) so the model can act on
		// the cross-reference without a separate lookup, matching goose/opencode.
		for _, rel := range d.Related {
			relPath := rel.Location.Path
			if r, err := filepath.Rel(root, rel.Location.Path); err == nil && !strings.HasPrefix(r, "..") {
				relPath = filepath.ToSlash(r)
			}
			fmt.Fprintf(
				&b, "    related: %s:%d:%d: %s\n",
				relPath,
				rel.Location.Range.Start.Line+1,
				rel.Location.Range.Start.Character+1,
				rel.Message,
			)
		}
	}
	if len(all) > diagnosticMatchCap {
		fmt.Fprintf(&b, "... and %d more not shown (%d total)\n", len(all)-diagnosticMatchCap, len(all))
	}

	return Result{
		Content:  strings.TrimRight(b.String(), "\n"),
		Metadata: diagnosticsMetadata(root, all, counts),
	}, nil
}

// diagnosticItem is one diagnostic in the structured metadata list — a
// machine-readable complement to the human-readable Content so the TUI and
// agent loop can react to individual items without re-parsing text. Mirrors
// navigateLocation in the navigate tool.
type diagnosticItem struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

// Metadata keys the diagnostics tool sets so downstream consumers (the agent
// loop, the TUI) can react to severity tallies without re-parsing the rendered
// list.
const (
	// MetadataDiagnosticCount holds the int total of reported diagnostics.
	MetadataDiagnosticCount = "diagnostic_count"
	// MetadataDiagnosticErrors holds the int count of error-severity diagnostics.
	MetadataDiagnosticErrors = "diagnostic_errors"
	// MetadataDiagnosticWarnings holds the int count of warning-severity diagnostics.
	MetadataDiagnosticWarnings = "diagnostic_warnings"
	// MetadataDiagnosticErrorFiles holds a []string of workspace-relative paths
	// (absolute when outside the workspace) that carry at least one error-level
	// diagnostic, sorted for determinism. Absent when no error diagnostics were
	// found, so callers can test for nil/missing rather than an empty slice.
	MetadataDiagnosticErrorFiles = "error_files"
	// MetadataDiagnosticItems holds a []diagnosticItem for the reported diagnostics
	// (capped to the same diagnosticMatchCap as the rendered Content so the two are
	// always consistent). Absent when no diagnostics were found.
	MetadataDiagnosticItems = "items"
)

// parseSeverityFilter maps the optional severity argument to the minimum
// lsp.Severity to report. It returns 0 (no filter) for an empty argument and an
// error for an unrecognized value. The accepted names mirror severityString's
// output, with "information" tolerated as a synonym for "info".
func parseSeverityFilter(s string) (lsp.Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return 0, nil
	case "error":
		return lsp.Error, nil
	case "warning":
		return lsp.Warning, nil
	case "info", "information":
		return lsp.Information, nil
	case "hint":
		return lsp.Hint, nil
	default:
		return 0, fmt.Errorf("unknown severity %q (want error, warning, info, or hint)", s)
	}
}

// filterBySeverity keeps only diagnostics at least as severe as min. Severities
// are ordered Error(1) < Warning(2) < Information(3) < Hint(4), so "at least as
// severe" means a value <= min. A diagnostic whose severity falls outside that
// range (a server that omitted it) is kept regardless, since it cannot be safely
// classified as below the threshold.
func filterBySeverity(diags []lsp.Diagnostic, min lsp.Severity) []lsp.Diagnostic {
	out := diags[:0:0]
	for _, d := range diags {
		if d.Severity < lsp.Error || d.Severity > lsp.Hint || d.Severity <= min {
			out = append(out, d)
		}
	}
	return out
}

// severityCounts tallies diagnostics by severity, indexed by the lsp.Severity
// value (Error..Hint). Index 0 is unused so a severity can index directly.
func severityCounts(diags []lsp.Diagnostic) [5]int {
	var counts [5]int
	for _, d := range diags {
		if d.Severity >= lsp.Error && d.Severity <= lsp.Hint {
			counts[d.Severity]++
		}
	}
	return counts
}

// diagnosticsSummary renders the leading header line, e.g.
// "3 diagnostics across 2 files (2 errors, 1 warning):". Only severities with a
// nonzero count are listed, in error→hint order, so the breakdown stays compact.
func diagnosticsSummary(diags []lsp.Diagnostic, counts [5]int) string {
	files := map[string]struct{}{}
	for _, d := range diags {
		files[d.Path] = struct{}{}
	}
	var parts []string
	for _, sev := range []lsp.Severity{lsp.Error, lsp.Warning, lsp.Information, lsp.Hint} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, pluralize(severityString(sev), n)))
		}
	}
	header := fmt.Sprintf("%d %s across %d %s",
		len(diags), pluralize("diagnostic", len(diags)),
		len(files), pluralize("file", len(files)),
	)
	if len(parts) > 0 {
		header += " (" + strings.Join(parts, ", ") + ")"
	}
	return header + ":"
}

// diagnosticsMetadata exposes the total, error/warning tallies, the sorted
// list of files that carry at least one error-level diagnostic, and a
// structured items list so the TUI and agent loop can react to individual
// problem sites without re-parsing text. root is used to relativize paths;
// pass "" to keep them absolute.
func diagnosticsMetadata(root string, diags []lsp.Diagnostic, counts [5]int) map[string]any {
	m := map[string]any{
		MetadataDiagnosticCount:    len(diags),
		MetadataDiagnosticErrors:   counts[lsp.Error],
		MetadataDiagnosticWarnings: counts[lsp.Warning],
	}
	if counts[lsp.Error] > 0 {
		seen := make(map[string]struct{})
		for _, d := range diags {
			if d.Severity != lsp.Error {
				continue
			}
			p := d.Path
			if root != "" {
				if rel, err := filepath.Rel(root, d.Path); err == nil && !strings.HasPrefix(rel, "..") {
					p = filepath.ToSlash(rel)
				}
			}
			seen[p] = struct{}{}
		}
		files := make([]string, 0, len(seen))
		for p := range seen {
			files = append(files, p)
		}
		sort.Strings(files)
		m[MetadataDiagnosticErrorFiles] = files
	}
	// Build a structured item list capped at diagnosticMatchCap so the metadata
	// is always consistent with the rendered Content (both truncate at the same
	// boundary). Mirrors MetadataLocations in the navigate tool.
	shown := diags
	if len(shown) > diagnosticMatchCap {
		shown = shown[:diagnosticMatchCap]
	}
	items := make([]diagnosticItem, 0, len(shown))
	for _, d := range shown {
		p := d.Path
		if root != "" {
			if rel, err := filepath.Rel(root, d.Path); err == nil && !strings.HasPrefix(rel, "..") {
				p = filepath.ToSlash(rel)
			}
		}
		items = append(items, diagnosticItem{
			Path:     p,
			Line:     d.Range.Start.Line + 1,
			Column:   d.Range.Start.Character + 1,
			Severity: severityString(d.Severity),
			Message:  d.Message,
			Code:     d.Code,
		})
	}
	if len(items) > 0 {
		m[MetadataDiagnosticItems] = items
	}
	return m
}

// pluralize appends an "s" to word when n is not 1. The severity labels and the
// nouns used here ("diagnostic", "file") all pluralize regularly.
func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func diagnosticFiles(ctx context.Context, root string, exts map[string]struct{}) ([]string, error) {
	// The LSP manager supplies the extension set in production so the scan tracks
	// the configured language servers. Fall back to the default servers' set when
	// none was provided (e.g. a direct unit-test call) so the walk still has a
	// target list rather than matching nothing.
	if len(exts) == 0 {
		exts = extSetFromList(lsp.DefaultExtensions())
	}
	// Skip dependency and build directories the same way grep and glob do, so a
	// workspace scan never descends into target/, dist/, node_modules/, etc. and
	// opens thousands of generated or vendored files into the language servers.
	// Root .gitignore directory entries extend the built-in set per project.
	gitignored := loadRootGitignore(root)
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking diagnostics path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if path != root && (ignoredDirs[entry.Name()] || gitignored[entry.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := exts[strings.ToLower(filepath.Ext(path))]; ok {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking diagnostics workspace: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

// diagnosticTail renders the trailing annotations shown after a diagnostic's
// message: the rule code in brackets and the reporting source in parentheses,
// each emitted only when present. The leading space keeps it appendable to a
// message line, e.g. " [E0425] (rustc)".
func diagnosticTail(d lsp.Diagnostic) string {
	var b strings.Builder
	if d.Code != "" {
		fmt.Fprintf(&b, " [%s]", d.Code)
	}
	if d.Source != "" {
		fmt.Fprintf(&b, " (%s)", d.Source)
	}
	// Surface classification tags (unnecessary/deprecated) so the model can tell
	// dead code and deprecated usages apart from ordinary warnings at a glance.
	if len(d.Tags) > 0 {
		labels := make([]string, len(d.Tags))
		for i, t := range d.Tags {
			labels[i] = t.String()
		}
		fmt.Fprintf(&b, " <%s>", strings.Join(labels, ", "))
	}
	// Surface the rule's documentation link when the server supplies one, so the
	// model can open the canonical explanation of the diagnostic rather than
	// guessing from its code alone. The "see" label keeps it from reading as
	// another angle-bracketed tag.
	if d.CodeHref != "" {
		fmt.Fprintf(&b, " see %s", d.CodeHref)
	}
	return b.String()
}

func severityString(severity lsp.Severity) string {
	switch severity {
	case lsp.Error:
		return "error"
	case lsp.Warning:
		return "warning"
	case lsp.Information:
		return "info"
	case lsp.Hint:
		return "hint"
	default:
		return "diagnostic"
	}
}

func workspaceRoot(workDir string) (string, error) {
	if strings.TrimSpace(workDir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting working directory: %w", err)
		}
		workDir = cwd
	}
	if expanded, err := expandHome(workDir); err == nil {
		workDir = expanded
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolving workspace root: %w", err)
	}
	return filepath.Clean(root), nil
}

func resolveWorkspacePath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return root, nil
	}
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(root, expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", path, err)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("checking workspace path %q: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q escapes the workspace", path)
	}
	return abs, nil
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
