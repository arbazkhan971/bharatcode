package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/diffutil"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// PatchTool applies a unified diff that may span several files in one atomic
// step. It is the multi-file complement to edit/multiedit: where those rewrite a
// single file, patch lets the model express a coherent change set — create,
// modify, and delete across many files — as one standard `diff -u` payload, and
// applies every file or none of them.
type PatchTool struct {
	deps Dependencies
	diag editDiagnoser
}

//go:embed patch.md
var patchDescription string

var patchSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["patch"],
  "properties": {
    "patch": {
      "type": "string",
      "minLength": 1,
      "description": "A unified diff (the output of git diff or diff -u). Each file section starts with '--- ' and '+++ ' headers followed by one or more @@ hunks. Use /dev/null as the old path to create a file or as the new path to delete one."
    }
  }
}`)

// newPatchTool constructs the multi-file unified-diff applier.
func newPatchTool(deps Dependencies) *PatchTool {
	t := &PatchTool{deps: deps}
	if deps.LSP != nil {
		t.diag = deps.LSP
	}
	return t
}

// Name returns the tool name.
func (t *PatchTool) Name() string { return "patch" }

// Description returns the model-facing tool description.
func (t *PatchTool) Description() string { return patchDescription }

// Schema returns the JSON argument schema.
func (t *PatchTool) Schema() json.RawMessage { return patchSchema }

// patchOp is one validated file change ready to be written once every file in
// the patch has been validated, so a malformed entry aborts the whole patch
// before any file is touched.
type patchOp struct {
	path       string
	kind       string // "create", "modify", or "delete"
	oldContent []byte
	newContent []byte
}

// Run executes the patch tool. It validates every file section first and only
// then applies them, so a patch that does not cleanly apply leaves the
// workspace untouched.
func (t *PatchTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return errorResult("patch is required"), nil
	}

	files, err := parseUnifiedPatch(in.Patch)
	if err != nil {
		return errorResult("could not parse patch: " + err.Error()), nil
	}
	if err := t.checkPermission(ctx, args); err != nil {
		return errorResult(err.Error()), nil
	}

	// Phase 1: validate and compute every file's new content. No write happens
	// until all sections succeed so the change set is all-or-nothing.
	ops := make([]patchOp, 0, len(files))
	for _, fp := range files {
		target := fp.newPath
		if fp.delete {
			target = fp.oldPath
		}
		if strings.TrimSpace(target) == "" {
			return errorResult("patch contains a file section with no target path"), nil
		}
		path, err := resolveToolPath(target, t.deps.WorkDir)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if !isInsideWorkDir(path, t.deps.WorkDir) {
			return errorResult("path is outside the workspace: " + path), nil
		}
		exists := fsext.Exists(path)

		switch {
		case fp.create:
			if exists {
				return errorResult(fmt.Sprintf("patch creates %s but the file already exists", target)), nil
			}
			ops = append(ops, patchOp{path: path, kind: "create", newContent: []byte(buildCreateContent(fp))})
		case fp.delete:
			if !exists {
				return errorResult(fmt.Sprintf("patch deletes %s but the file does not exist", target)), nil
			}
			if msg := editGuard(ctx, t.deps, path, "patching"); msg != "" {
				return errorResult(msg), nil
			}
			old, err := os.ReadFile(path)
			if err != nil {
				return Result{}, fmt.Errorf("reading file %s: %w", path, err)
			}
			ops = append(ops, patchOp{path: path, kind: "delete", oldContent: old})
		default:
			if !exists {
				return errorResult(fmt.Sprintf("patch modifies %s but the file does not exist (use /dev/null as the old path to create it)", target)), nil
			}
			if msg := editGuard(ctx, t.deps, path, "patching"); msg != "" {
				return errorResult(msg), nil
			}
			old, err := os.ReadFile(path)
			if err != nil {
				return Result{}, fmt.Errorf("reading file %s: %w", path, err)
			}
			next, err := applyFilePatch(fp, string(old))
			if err != nil {
				return errorResult(fmt.Sprintf("%s: %v", target, err)), nil
			}
			if next == string(old) {
				return errorResult(fmt.Sprintf("%s: patch produced no changes", target)), nil
			}
			ops = append(ops, patchOp{path: path, kind: "modify", oldContent: old, newContent: []byte(next)})
		}
	}

	// Phase 2: apply every validated operation and report what changed.
	var b strings.Builder
	changed := make([]string, 0, len(ops))
	for i, op := range ops {
		if i > 0 {
			b.WriteString("\n\n")
		}
		switch op.kind {
		case "create", "modify":
			dir := filepath.Dir(op.path)
			if err := fsext.EnsureDir(dir, 0o755); err != nil {
				return Result{}, fmt.Errorf("ensuring parent directory %s: %w", dir, err)
			}
			if err := fsext.AtomicWrite(op.path, op.newContent, 0o644); err != nil {
				return Result{}, fmt.Errorf("writing file %s: %w", op.path, err)
			}
			if err := recordToolWrite(ctx, t.deps, op.path, op.oldContent, op.newContent); err != nil {
				return Result{}, err
			}
			markViewed(sessionID(ctx, t.deps), op.path)
			verb := "modified"
			if op.kind == "create" {
				verb = "created"
			}
			fmt.Fprintf(&b, "%s %s (%d bytes)", verb, op.path, len(op.newContent))
			if d := diffutil.Unified(string(op.oldContent), string(op.newContent)); d != "" {
				b.WriteString("\n")
				b.WriteString(d)
			}
			if note := postWriteDiagnostics(ctx, t.diag, t.deps.WorkDir, op.path); note != "" {
				b.WriteString("\n")
				b.WriteString(note)
			}
		case "delete":
			if err := os.Remove(op.path); err != nil {
				return Result{}, fmt.Errorf("deleting file %s: %w", op.path, err)
			}
			if err := recordToolWrite(ctx, t.deps, op.path, op.oldContent, nil); err != nil {
				return Result{}, err
			}
			fmt.Fprintf(&b, "deleted %s", op.path)
		}
		changed = append(changed, op.path)
	}

	header := fmt.Sprintf("applied patch to %d %s", len(ops), pluralize("file", len(ops)))
	return Result{
		Content: header + ":\n\n" + b.String(),
		Metadata: map[string]any{
			"files": changed,
			"count": len(ops),
		},
	}, nil
}

func (t *PatchTool) checkPermission(ctx context.Context, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	decision, err := t.deps.Permission.Check(ctx, permission.Request{
		ToolName:  t.Name(),
		Args:      args,
		SessionID: sessionID(ctx, t.deps),
	})
	if err != nil {
		return fmt.Errorf("checking permission: %w", err)
	}
	if decision == permission.DecisionDeny {
		return fmt.Errorf("permission denied")
	}
	return nil
}

// editGuard enforces read-before-edit and stale-read protection for a single
// file a mutating tool will modify or delete, using the FileTracker last-read
// baseline. It is the one place this contract lives, so every mutating tool
// (edit, multiedit, patch, write, rename) enforces it identically. The action
// verb (e.g. "patching", "writing", "renaming") is woven into the model-facing
// message so each tool's refusal reads naturally. It returns a message
// describing the violation, or "" when the edit may proceed (including when no
// FileTracker is wired, as in tests).
func editGuard(ctx context.Context, deps Dependencies, path, action string) string {
	sid := sessionID(ctx, deps)
	if deps.FileTracker == nil || sid == "" {
		return ""
	}
	if fsext.Exists(path) {
		read, err := deps.FileTracker.HasRead(ctx, sid, path)
		if err != nil {
			return fmt.Sprintf("checking read history for %s: %v", path, err)
		}
		if !read {
			return fmt.Sprintf(
				"file %s has not been read in this session — view the file before %s so it applies to the file's current contents",
				path, action,
			)
		}
	}
	changed, err := deps.FileTracker.HasConflict(ctx, sid, path)
	if err != nil {
		return fmt.Sprintf("checking file freshness for %s: %v", path, err)
	}
	if changed {
		return fmt.Sprintf(
			"file %s has been modified on disk since it was last read in this session — view the file again before %s to avoid clobbering changes",
			path, action,
		)
	}
	return ""
}

// filePatch is one file's worth of a unified diff: the source and target paths
// and the ordered hunks that transform one into the other.
type filePatch struct {
	oldPath string // path after "--- ", git a/ prefix stripped; "" for /dev/null
	newPath string // path after "+++ ", git b/ prefix stripped; "" for /dev/null
	create  bool   // old path was /dev/null
	delete  bool   // new path was /dev/null
	hunks   []patchHunk
}

// patchHunk is a single @@ region. oldLines holds the context and removed lines
// (the block to locate in the file); newLines holds the context and added lines
// (its replacement). Context lines appear in both.
type patchHunk struct {
	oldStart int // 1-based line in the original file the header declares
	oldLines []string
	newLines []string
}

// parseUnifiedPatch turns a unified-diff payload into per-file hunk sets. It
// tolerates git preamble lines (diff --git, index, mode) and per-file timestamps
// after a tab, and uses each hunk header's declared line counts to bound the
// hunk body precisely so a following "--- " file header is never mistaken for a
// removed line.
func parseUnifiedPatch(text string) ([]filePatch, error) {
	lines := strings.Split(text, "\n")
	var files []filePatch
	for i := 0; i < len(lines); {
		line := lines[i]
		if !strings.HasPrefix(line, "--- ") {
			i++ // skip preamble: diff --git, index, mode lines, blank separators
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
			return nil, fmt.Errorf("'---' header not followed by a '+++' header")
		}
		var fp filePatch
		fp.oldPath, fp.create = parsePatchPath(strings.TrimPrefix(line, "--- "), true)
		fp.newPath, fp.delete = parsePatchPath(strings.TrimPrefix(lines[i+1], "+++ "), false)
		i += 2
		for i < len(lines) && strings.HasPrefix(lines[i], "@@") {
			h, next, err := parseHunk(lines, i)
			if err != nil {
				return nil, err
			}
			fp.hunks = append(fp.hunks, h)
			i = next
		}
		if len(fp.hunks) == 0 {
			return nil, fmt.Errorf("file section has no @@ hunks")
		}
		files = append(files, fp)
	}
	if len(files) == 0 {
		return nil, errors.New("no file sections found; expected '--- '/'+++ ' headers")
	}
	return files, nil
}

// parsePatchPath extracts a clean workspace path from a "--- "/"+++ " header
// value, stripping a trailing tab-delimited timestamp and the git a/ (old) or
// b/ (new) prefix. It reports whether the path was /dev/null.
func parsePatchPath(raw string, isOld bool) (string, bool) {
	raw = strings.TrimRight(raw, "\r")
	if tab := strings.IndexByte(raw, '\t'); tab >= 0 {
		raw = raw[:tab]
	}
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return "", true
	}
	prefix := "b/"
	if isOld {
		prefix = "a/"
	}
	raw = strings.TrimPrefix(raw, prefix)
	return raw, false
}

// parseHunk reads one @@ hunk starting at lines[start], consuming exactly the
// number of removed/added lines the header declares so the body cannot run past
// its end. It returns the hunk and the index of the first line after it.
func parseHunk(lines []string, start int) (patchHunk, int, error) {
	oldStart, oldCount, newCount, err := parseHunkHeader(lines[start])
	if err != nil {
		return patchHunk{}, 0, err
	}
	h := patchHunk{oldStart: oldStart}
	oldSeen, newSeen := 0, 0
	i := start + 1
	for i < len(lines) && (oldSeen < oldCount || newSeen < newCount) {
		l := lines[i]
		marker := byte(' ')
		rest := ""
		if l != "" {
			marker = l[0]
			rest = l[1:]
		}
		switch marker {
		case ' ':
			h.oldLines = append(h.oldLines, rest)
			h.newLines = append(h.newLines, rest)
			oldSeen++
			newSeen++
		case '-':
			h.oldLines = append(h.oldLines, rest)
			oldSeen++
		case '+':
			h.newLines = append(h.newLines, rest)
			newSeen++
		case '\\':
			// "\ No newline at end of file" — metadata, does not count toward a line.
		default:
			return patchHunk{}, 0, fmt.Errorf("unexpected line in hunk body: %q", l)
		}
		i++
	}
	if oldSeen != oldCount || newSeen != newCount {
		return patchHunk{}, 0, fmt.Errorf("hunk header declared -%d/+%d lines but body had -%d/+%d", oldCount, newCount, oldSeen, newSeen)
	}
	return h, i, nil
}

// parseHunkHeader parses "@@ -l,s +l,s @@ heading" into the old start line and
// the old and new line counts. A missing count defaults to 1 per the unified
// diff format.
func parseHunkHeader(line string) (oldStart, oldCount, newCount int, err error) {
	body := strings.TrimPrefix(line, "@@")
	end := strings.Index(body, "@@")
	if end < 0 {
		return 0, 0, 0, fmt.Errorf("malformed hunk header: %q", line)
	}
	fields := strings.Fields(strings.TrimSpace(body[:end]))
	if len(fields) != 2 {
		return 0, 0, 0, fmt.Errorf("malformed hunk header: %q", line)
	}
	oldStart, oldCount, err = parseHunkRange(fields[0], '-')
	if err != nil {
		return 0, 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
	}
	_, newCount, err = parseHunkRange(fields[1], '+')
	if err != nil {
		return 0, 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
	}
	return oldStart, oldCount, newCount, nil
}

// parseHunkRange parses a "-l,s" or "+l,s" range token, defaulting the count to
// 1 when only a start line is present.
func parseHunkRange(tok string, sign byte) (start, count int, err error) {
	if len(tok) == 0 || tok[0] != sign {
		return 0, 0, fmt.Errorf("range %q missing %q", tok, string(sign))
	}
	tok = tok[1:]
	if comma := strings.IndexByte(tok, ','); comma >= 0 {
		if start, err = strconv.Atoi(tok[:comma]); err != nil {
			return 0, 0, err
		}
		if count, err = strconv.Atoi(tok[comma+1:]); err != nil {
			return 0, 0, err
		}
		return start, count, nil
	}
	if start, err = strconv.Atoi(tok); err != nil {
		return 0, 0, err
	}
	return start, 1, nil
}

// applyFilePatch applies fp's hunks to original and returns the new content,
// preserving the file's trailing-newline state. Hunks are applied in order; each
// is located by its declared line and verified against the file's actual lines,
// falling back to a forward content search so small line-number drift still
// applies cleanly. An error is returned (and no partial result) when a hunk's
// context cannot be found.
func applyFilePatch(fp filePatch, original string) (string, error) {
	lines, trailingNL := splitKeepEOL(original)
	out := make([]string, 0, len(lines))
	cursor := 0
	for hi, h := range fp.hunks {
		pos, err := locateHunk(lines, h, cursor)
		if err != nil {
			return "", fmt.Errorf("hunk #%d does not apply: %w", hi+1, err)
		}
		out = append(out, lines[cursor:pos]...)
		out = append(out, h.newLines...)
		cursor = pos + len(h.oldLines)
	}
	out = append(out, lines[cursor:]...)
	return joinKeepEOL(out, trailingNL), nil
}

// locateHunk returns the index in lines (>= cursor) where h's old block begins.
// A pure-insertion hunk (no context or removed lines) has no block to match, so
// it is placed after the line its header names — the unified-diff convention for
// a zero-length old range is the line number preceding the insertion — clamped
// to the unconsumed region.
func locateHunk(lines []string, h patchHunk, cursor int) (int, error) {
	if len(h.oldLines) == 0 {
		pos := h.oldStart
		if pos < cursor {
			pos = cursor
		}
		if pos > len(lines) {
			pos = len(lines)
		}
		return pos, nil
	}
	if hint := h.oldStart - 1; hint >= cursor && matchAt(lines, h.oldLines, hint) {
		return hint, nil
	}
	for p := cursor; p+len(h.oldLines) <= len(lines); p++ {
		if matchAt(lines, h.oldLines, p) {
			return p, nil
		}
	}
	return 0, errors.New("context did not match the file")
}

// matchAt reports whether block appears in lines starting exactly at index pos.
func matchAt(lines, block []string, pos int) bool {
	if pos < 0 || pos+len(block) > len(lines) {
		return false
	}
	for i, l := range block {
		if lines[pos+i] != l {
			return false
		}
	}
	return true
}

// buildCreateContent assembles a new file's contents from the added lines of a
// /dev/null create patch, appending a trailing newline as files conventionally
// carry one.
func buildCreateContent(fp filePatch) string {
	var added []string
	for _, h := range fp.hunks {
		added = append(added, h.newLines...)
	}
	s := strings.Join(added, "\n")
	if s != "" {
		s += "\n"
	}
	return s
}

// splitKeepEOL splits content into lines while remembering whether it ended with
// a newline, so the file's trailing-newline state can be restored after editing.
func splitKeepEOL(s string) (lines []string, trailingNL bool) {
	if s == "" {
		return nil, false
	}
	trailingNL = strings.HasSuffix(s, "\n")
	if trailingNL {
		s = strings.TrimSuffix(s, "\n")
	}
	return strings.Split(s, "\n"), trailingNL
}

// joinKeepEOL is the inverse of splitKeepEOL.
func joinKeepEOL(lines []string, trailingNL bool) string {
	s := strings.Join(lines, "\n")
	if trailingNL {
		s += "\n"
	}
	return s
}
