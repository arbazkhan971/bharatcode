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
	"unicode/utf16"

	"github.com/arbazkhan971/bharatcode/internal/diffutil"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// FormatSource is the LSP capability consumed by the format tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type FormatSource interface {
	Format(ctx context.Context, path string) ([]lsp.TextEdit, error)
	FormatRange(ctx context.Context, path string, rng lsp.Range) ([]lsp.TextEdit, error)
}

type formatTool struct {
	deps   Dependencies
	source FormatSource
}

type formatArgs struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
}

var schemaFormat = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative path to the file to reformat in place."
    },
    "line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based first line to reformat. Omit to format the whole file; provide it (optionally with end_line) to reformat only a span, leaving the rest untouched."
    },
    "end_line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based last line of the span to reformat. Defaults to line (a single line). Ignored unless line is set."
    }
  }
}`)

//go:embed format.md
var formatDescription string

func newFormatTool(deps Dependencies) Tool {
	t := &formatTool{deps: deps}
	// A nil *lsp.Manager assigned to the FormatSource interface would produce a
	// non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the source
	// when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
	}
	return t
}

func (t *formatTool) Name() string {
	return "format"
}

func (t *formatTool) Description() string {
	return formatDescription
}

func (t *formatTool) Schema() json.RawMessage {
	return schemaFormat
}

func (t *formatTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args formatArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid format arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("format is unavailable: no LSP manager configured"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult("format requires a path"), nil
	}

	path, err := resolveToolPath(args.Path, t.deps.WorkDir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if !isInsideWorkDir(path, t.deps.WorkDir) {
		return errorResult("path is outside the workspace: " + path), nil
	}
	if err := t.checkPermission(ctx, path, raw); err != nil {
		return errorResult(err.Error()), nil
	}

	// A line selects range formatting (reformat just that span); without one the
	// whole document is formatted.
	scope := args.Path
	var edits []lsp.TextEdit
	if args.Line > 0 {
		endLine := args.EndLine
		if endLine < args.Line {
			endLine = args.Line
		}
		// LSP positions are 0-based. The end position addresses the start of the
		// line after the last selected one so the whole final line (including its
		// trailing newline) is covered; servers clamp it to EOF on the last line.
		rng := lsp.Range{
			Start: lsp.Position{Line: args.Line - 1, Character: 0},
			End:   lsp.Position{Line: endLine, Character: 0},
		}
		if endLine == args.Line {
			scope = fmt.Sprintf("%s:%d", args.Path, args.Line)
		} else {
			scope = fmt.Sprintf("%s:%d-%d", args.Path, args.Line, endLine)
		}
		edits, err = t.source.FormatRange(ctx, path, rng)
	} else {
		edits, err = t.source.Format(ctx, path)
	}
	if err != nil {
		return Result{}, fmt.Errorf("formatting %s: %w", scope, err)
	}
	if len(edits) == 0 {
		return Result{Content: fmt.Sprintf("%s is already formatted.", scope)}, nil
	}

	oldContent, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading file %s: %w", path, err)
	}
	newText, err := applyTextEdits(string(oldContent), edits)
	if err != nil {
		return errorResult(fmt.Sprintf("applying formatting edits to %s: %v", args.Path, err)), nil
	}
	if newText == string(oldContent) {
		return Result{Content: fmt.Sprintf("%s is already formatted.", scope)}, nil
	}

	if err := fsext.AtomicWrite(path, []byte(newText), 0o644); err != nil {
		return Result{}, fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := t.recordWrite(ctx, path, oldContent, []byte(newText)); err != nil {
		return Result{}, err
	}

	content := fmt.Sprintf("formatted %s (%d edit(s))", scope, len(edits))
	metadata := map[string]any{"path": path, "edits": len(edits)}
	// Surface a unified diff of the reformatting so the model can see exactly
	// what changed, mirroring the edit tool's output shaping.
	if d := diffutil.Unified(string(oldContent), newText); d != "" {
		content += "\n\n" + d
		metadata["diff"] = d
	}
	return Result{
		Content:  content,
		Metadata: metadata,
	}, nil
}

func (t *formatTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	args := map[string]any{}
	_ = json.Unmarshal(raw, &args)
	args["path"] = path
	decision, err := t.deps.Permission.Check(ctx, permission.Request{
		ToolName:  t.Name(),
		Args:      args,
		SessionID: t.deps.SessionID,
	})
	if err != nil {
		return fmt.Errorf("checking permission: %w", err)
	}
	if decision == permission.DecisionDeny {
		return fmt.Errorf("permission denied")
	}
	return nil
}

func (t *formatTool) recordWrite(ctx context.Context, path string, oldContent, newContent []byte) error {
	if t.deps.FileTracker == nil || t.deps.SessionID == "" {
		return nil
	}
	if _, err := t.deps.FileTracker.RecordWrite(ctx, t.deps.SessionID, path, oldContent, newContent); err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(t.deps.SessionID, path)
	return nil
}

// resourceOpsNote renders a warning listing the file create/rename/delete
// operations a WorkspaceEdit carried that the text-edit apply path cannot
// perform. It returns "" when there are none. Callers append it so the model
// knows an applied rename or code action is only partial — the language server
// wanted to create, rename, or delete files as well, and BharatCode applies
// text edits only. Paths are shown workspace-relative when they fall inside it.
func resourceOpsNote(ops []lsp.ResourceOperation, workDir string) string {
	if len(ops) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "warning: %d file operation(s) the server requested were NOT applied (BharatCode applies text edits only):", len(ops))
	for _, op := range ops {
		switch op.Kind {
		case "rename":
			fmt.Fprintf(&b, "\n  rename %s -> %s", relToWorkDir(op.OldPath, workDir), relToWorkDir(op.NewPath, workDir))
		case "create":
			fmt.Fprintf(&b, "\n  create %s", relToWorkDir(op.Path, workDir))
		case "delete":
			fmt.Fprintf(&b, "\n  delete %s", relToWorkDir(op.Path, workDir))
		default:
			fmt.Fprintf(&b, "\n  %s %s", op.Kind, relToWorkDir(op.Path, workDir))
		}
	}
	b.WriteString("\nPerform these file operations yourself to complete the change.")
	return b.String()
}

// relToWorkDir renders path relative to workDir with forward slashes when it
// lies inside the workspace, otherwise returns it unchanged.
func relToWorkDir(path, workDir string) string {
	if workDir == "" {
		return path
	}
	if r, err := filepath.Rel(workDir, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}

// applyTextEdits applies LSP text edits to src and returns the result. Edits are
// applied from the highest start offset to the lowest so earlier byte offsets
// stay valid as later edits are spliced in. Positions are LSP coordinates:
// zero-based lines and UTF-16 code-unit character offsets. Overlapping edits are
// not expected from a conforming server; if start and end resolve out of order
// they are swapped defensively.
func applyTextEdits(src string, edits []lsp.TextEdit) (string, error) {
	if len(edits) == 0 {
		return src, nil
	}

	lineStarts := lineStartOffsets(src)

	type resolvedEdit struct {
		start int
		end   int
		text  string
	}
	resolved := make([]resolvedEdit, 0, len(edits))
	for _, e := range edits {
		start, err := offsetForPosition(src, lineStarts, e.Range.Start)
		if err != nil {
			return "", err
		}
		end, err := offsetForPosition(src, lineStarts, e.Range.End)
		if err != nil {
			return "", err
		}
		if start > end {
			start, end = end, start
		}
		resolved = append(resolved, resolvedEdit{start: start, end: end, text: e.NewText})
	}

	sort.SliceStable(resolved, func(i, j int) bool {
		if resolved[i].start != resolved[j].start {
			return resolved[i].start > resolved[j].start
		}
		return resolved[i].end > resolved[j].end
	})

	out := src
	for _, e := range resolved {
		out = out[:e.start] + e.text + out[e.end:]
	}
	return out, nil
}

// lineStartOffsets returns the byte offset at which each line of src begins. The
// slice always has at least one entry (offset 0 for the first line).
func lineStartOffsets(src string) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetForPosition converts an LSP position to a byte offset within src. A
// line one past the last is clamped to the end of the document, mirroring how
// servers address an end-of-file insertion point.
func offsetForPosition(src string, lineStarts []int, pos lsp.Position) (int, error) {
	if pos.Line < 0 {
		return 0, fmt.Errorf("negative edit line %d", pos.Line)
	}
	if pos.Line >= len(lineStarts) {
		if pos.Line == len(lineStarts) {
			return len(src), nil
		}
		return 0, fmt.Errorf("edit line %d is out of range (file has %d lines)", pos.Line, len(lineStarts))
	}

	lineStart := lineStarts[pos.Line]
	lineEnd := len(src)
	if pos.Line+1 < len(lineStarts) {
		lineEnd = lineStarts[pos.Line+1]
	}
	return lineStart + utf16OffsetToByte(src[lineStart:lineEnd], pos.Character), nil
}

// utf16OffsetToByte returns the byte index in line corresponding to the given
// UTF-16 code-unit offset, the unit LSP character positions are measured in. An
// offset past the end of the line clamps to its length.
func utf16OffsetToByte(line string, utf16Offset int) int {
	if utf16Offset <= 0 {
		return 0
	}
	units := 0
	for i, r := range line {
		if units >= utf16Offset {
			return i
		}
		units += utf16.RuneLen(r)
	}
	return len(line)
}
