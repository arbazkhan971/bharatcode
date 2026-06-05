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
}

type diagnosticsArgs struct {
	Path string `json:"path,omitempty"`
}

var schemaDiagnostics = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {
      "type": "string",
      "description": "Optional file path to inspect. Omit to scan supported files in the workspace."
    }
  }
}`)

//go:embed diagnostics.md
var diagnosticsDescription string

func newDiagnosticsTool(deps Dependencies) Tool {
	t := &diagnosticsTool{workDir: deps.WorkDir}
	// A nil *lsp.Manager assigned to the DiagnosticSource interface would
	// produce a non-nil interface wrapping a nil pointer, defeating the
	// t.source == nil guard in Run and panicking on the first method call.
	// Only adopt the source when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *diagnosticsTool) Name() string {
	return "diagnostics"
}

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
		paths = []string{path}
	} else {
		paths, err = diagnosticFiles(ctx, root)
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
		return Result{Content: "No diagnostics found."}, nil
	}

	var b strings.Builder
	for _, d := range all {
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
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

func diagnosticFiles(ctx context.Context, root string) ([]string, error) {
	exts := map[string]struct{}{
		".c": {}, ".cc": {}, ".cpp": {}, ".cxx": {}, ".go": {}, ".h": {},
		".hh": {}, ".hpp": {}, ".js": {}, ".jsx": {}, ".py": {}, ".rs": {},
		".ts": {}, ".tsx": {},
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking diagnostics path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				if path != root {
					return filepath.SkipDir
				}
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
