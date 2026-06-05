package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/diffutil"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// NotebookEditTool replaces, inserts, or deletes a cell in a Jupyter notebook
// (.ipynb) while preserving the surrounding nbformat structure — the notebook
// metadata, format version, and every other cell's source and outputs.
//
// Editing a notebook with the plain edit/write tools is error-prone: a cell's
// source is stored as a JSON array of line strings inside a larger document, so
// a textual find/replace easily corrupts the JSON. This tool operates on whole
// cells addressed by their 1-based number (the same number the view tool
// prints), mirroring Claude Code's NotebookEdit tool.
type NotebookEditTool struct {
	deps Dependencies
}

//go:embed notebook_edit.md
var notebookEditDescription string

var notebookEditSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path"],
  "properties": {
    "path": {"type": "string", "minLength": 1, "description": "Workspace-relative path to a .ipynb notebook."},
    "cell_number": {"type": "integer", "minimum": 1, "description": "1-based cell index, as printed by the view tool. Required for replace and delete. For insert it is the position the new cell takes (existing cells shift down); omit to append at the end."},
    "new_source": {"type": "string", "description": "New cell source. Used by replace and insert; ignored for delete."},
    "cell_type": {"type": "string", "enum": ["code", "markdown"], "description": "Cell type. Required for insert. For replace, changes the cell's type when given (which clears its outputs)."},
    "edit_mode": {"type": "string", "enum": ["replace", "insert", "delete"], "description": "replace (default), insert, or delete."}
  }
}`)

// newNotebookEditTool constructs the notebook cell editor.
func newNotebookEditTool(deps Dependencies) *NotebookEditTool {
	return &NotebookEditTool{deps: deps}
}

// Name returns the tool name.
func (t *NotebookEditTool) Name() string { return "notebook_edit" }

// Description returns the model-facing tool description.
func (t *NotebookEditTool) Description() string { return notebookEditDescription }

// Schema returns the JSON argument schema.
func (t *NotebookEditTool) Schema() json.RawMessage { return notebookEditSchema }

type notebookEditArgs struct {
	Path       string `json:"path"`
	CellNumber int    `json:"cell_number,omitempty"`
	NewSource  string `json:"new_source,omitempty"`
	CellType   string `json:"cell_type,omitempty"`
	EditMode   string `json:"edit_mode,omitempty"`
}

// Run executes the notebook edit tool.
func (t *NotebookEditTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in notebookEditArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}
	if !isNotebookPath(in.Path) {
		return errorResult("notebook_edit only operates on .ipynb files; use edit/write for other files"), nil
	}

	path, err := resolveToolPath(in.Path, t.deps.WorkDir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if !isInsideWorkDir(path, t.deps.WorkDir) {
		return errorResult("path is outside the workspace: " + path), nil
	}
	if err := t.checkPermission(ctx, path, args); err != nil {
		return errorResult(err.Error()), nil
	}
	if !fsext.Exists(path) {
		return errorResult("notebook does not exist: " + path), nil
	}

	// Guard against blind and stale edits, exactly as the edit tool does: the
	// notebook must have been read this session, and must not have changed on
	// disk since, so the cell numbers the model is using still line up.
	if t.deps.FileTracker != nil && t.deps.SessionID != "" {
		read, readErr := t.deps.FileTracker.HasRead(ctx, t.deps.SessionID, path)
		if readErr != nil {
			return errorResult(fmt.Sprintf("checking read history for %s: %v", path, readErr)), nil
		}
		if !read {
			return errorResult(fmt.Sprintf(
				"notebook %s has not been read in this session — view it before editing so cell numbers line up",
				path,
			)), nil
		}
		changed, conflictErr := t.deps.FileTracker.HasConflict(ctx, t.deps.SessionID, path)
		if conflictErr != nil {
			return errorResult(fmt.Sprintf("checking file freshness for %s: %v", path, conflictErr)), nil
		}
		if changed {
			return errorResult(fmt.Sprintf(
				"notebook %s has been modified on disk since it was last read in this session — view it again before editing",
				path,
			)), nil
		}
	}

	oldContent, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading notebook %s: %w", path, err)
	}

	newContent, summary, applyErr := applyNotebookEdit(oldContent, in)
	if applyErr != nil {
		return errorResult(applyErr.Error()), nil
	}
	if string(newContent) == string(oldContent) {
		return errorResult("edit produced no changes"), nil
	}
	if err := fsext.AtomicWrite(path, newContent, 0o644); err != nil {
		return Result{}, fmt.Errorf("writing notebook %s: %w", path, err)
	}
	if err := recordToolWrite(ctx, t.deps, path, oldContent, newContent); err != nil {
		return Result{}, err
	}

	content := fmt.Sprintf("%s in %s", summary, path)
	metadata := map[string]any{"path": path}
	if d := diffutil.Unified(string(oldContent), string(newContent)); d != "" {
		content += "\n\n" + d
		metadata["diff"] = d
	}
	return Result{Content: content, Metadata: metadata}, nil
}

func (t *NotebookEditTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	var args map[string]any
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

// applyNotebookEdit applies one cell operation to raw .ipynb bytes and returns
// the re-serialized notebook plus a short human-readable summary. Only nbformat
// 4 notebooks (top-level "cells") can be edited; the unrelated parts of the
// document are preserved verbatim. The function is pure so it can be tested
// without touching the filesystem.
func applyNotebookEdit(data []byte, a notebookEditArgs) (out []byte, summary string, err error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, "", fmt.Errorf("parsing notebook JSON: %w", err)
	}
	rawCells, ok := top["cells"]
	if !ok {
		return nil, "", fmt.Errorf("unsupported notebook format: no top-level \"cells\" array (only nbformat 4 notebooks can be edited)")
	}
	var cells []json.RawMessage
	if err := json.Unmarshal(rawCells, &cells); err != nil {
		return nil, "", fmt.Errorf("parsing notebook cells: %w", err)
	}

	mode := a.EditMode
	if mode == "" {
		mode = "replace"
	}
	switch mode {
	case "replace":
		idx := a.CellNumber - 1
		if a.CellNumber < 1 || idx >= len(cells) {
			return nil, "", fmt.Errorf("cell_number %d is out of range (notebook has %d cell(s))", a.CellNumber, len(cells))
		}
		updated, repErr := replaceCell(cells[idx], a.NewSource, a.CellType)
		if repErr != nil {
			return nil, "", repErr
		}
		cells[idx] = updated
		summary = fmt.Sprintf("replaced cell %d", a.CellNumber)
	case "insert":
		if a.CellType == "" {
			return nil, "", fmt.Errorf("cell_type is required when edit_mode is insert")
		}
		newCell, buildErr := buildCell(a.CellType, a.NewSource)
		if buildErr != nil {
			return nil, "", buildErr
		}
		pos := len(cells)
		if a.CellNumber >= 1 {
			if pos = a.CellNumber - 1; pos > len(cells) {
				pos = len(cells)
			}
		}
		cells = append(cells, nil)
		copy(cells[pos+1:], cells[pos:])
		cells[pos] = newCell
		summary = fmt.Sprintf("inserted %s cell at position %d", a.CellType, pos+1)
	case "delete":
		idx := a.CellNumber - 1
		if a.CellNumber < 1 || idx >= len(cells) {
			return nil, "", fmt.Errorf("cell_number %d is out of range (notebook has %d cell(s))", a.CellNumber, len(cells))
		}
		cells = append(cells[:idx], cells[idx+1:]...)
		summary = fmt.Sprintf("deleted cell %d", a.CellNumber)
	default:
		return nil, "", fmt.Errorf("edit_mode must be one of replace, insert, delete")
	}

	newRawCells, err := json.Marshal(cells)
	if err != nil {
		return nil, "", fmt.Errorf("encoding cells: %w", err)
	}
	top["cells"] = newRawCells
	// Jupyter serializes notebooks with one-space indentation and a trailing
	// newline; match that so edits produce minimal, conventional diffs. A
	// map[string]json.RawMessage marshals its keys in sorted order, which
	// coincides with nbformat's canonical key order (cells, metadata, nbformat,
	// nbformat_minor; and within a cell cell_type, execution_count, id, metadata,
	// outputs, source).
	body, err := json.MarshalIndent(top, "", " ")
	if err != nil {
		return nil, "", fmt.Errorf("encoding notebook: %w", err)
	}
	return append(body, '\n'), summary, nil
}

// replaceCell swaps a cell's source, preserving its unrelated fields. When
// newType is non-empty and differs from the current type, the cell is converted
// (its type-specific fields are reconciled, which clears code outputs). The
// source is always reconciled to the new type's expectations via conformCellType.
func replaceCell(raw json.RawMessage, newSource, newType string) (json.RawMessage, error) {
	var cell map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cell); err != nil {
		return nil, fmt.Errorf("parsing target cell: %w", err)
	}
	resultType := jsonString(cell["cell_type"])
	if newType != "" {
		resultType = newType
		cell["cell_type"] = rawJSONString(newType)
	}
	src, err := encodeSource(newSource)
	if err != nil {
		return nil, err
	}
	cell["source"] = src
	conformCellType(cell, resultType)
	return json.Marshal(cell)
}

// buildCell constructs a fresh cell of the given type with the given source.
func buildCell(cellType, source string) (json.RawMessage, error) {
	src, err := encodeSource(source)
	if err != nil {
		return nil, err
	}
	cell := map[string]json.RawMessage{
		"cell_type": rawJSONString(cellType),
		"metadata":  json.RawMessage(`{}`),
		"source":    src,
	}
	conformCellType(cell, cellType)
	return json.Marshal(cell)
}

// conformCellType ensures a cell carries exactly the fields nbformat requires for
// its type: code cells get a (reset) outputs list and execution_count; markdown
// and raw cells must not carry those. Every cell gets a metadata object. Resetting
// a code cell's outputs is intentional — once the source changes, prior outputs no
// longer correspond to it.
func conformCellType(cell map[string]json.RawMessage, cellType string) {
	if _, ok := cell["metadata"]; !ok {
		cell["metadata"] = json.RawMessage(`{}`)
	}
	switch cellType {
	case "code":
		cell["outputs"] = json.RawMessage(`[]`)
		cell["execution_count"] = json.RawMessage(`null`)
	default: // markdown, raw, or unknown
		delete(cell, "outputs")
		delete(cell, "execution_count")
	}
}

// encodeSource stores a cell's source the way nbformat does: a JSON array of
// line strings, each line keeping its trailing newline except possibly the last.
// An empty source becomes an empty array.
func encodeSource(s string) (json.RawMessage, error) {
	b, err := json.Marshal(splitKeepEnds(s))
	if err != nil {
		return nil, fmt.Errorf("encoding cell source: %w", err)
	}
	return b, nil
}

// splitKeepEnds splits s into lines, keeping each line's trailing newline. It
// returns an empty (non-nil) slice for an empty string so it marshals to [].
func splitKeepEnds(s string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// jsonString decodes a JSON string value, returning "" when raw is empty or not
// a string.
func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// rawJSONString marshals a plain string to its JSON form. String marshaling
// cannot fail, so any error is dropped.
func rawJSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
