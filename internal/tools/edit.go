package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// EditTool performs one exact text replacement in an existing file.
type EditTool struct {
	deps Dependencies
	diag editDiagnoser
}

//go:embed edit.md
var editDescription string

var editSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "old_string", "new_string"],
  "properties": {
    "path": {"type": "string", "minLength": 1},
    "old_string": {"type": "string"},
    "new_string": {"type": "string"},
    "replace_all": {"type": "boolean"}
  }
}`)

// newEditTool constructs the exact replacement tool.
func newEditTool(deps Dependencies) *EditTool {
	t := &EditTool{deps: deps}
	// Adopt the LSP manager as the post-edit diagnoser only when present: a nil
	// *lsp.Manager stored in the interface would be a non-nil interface wrapping
	// a nil pointer, defeating the nil guard in postWriteDiagnostics.
	if deps.LSP != nil {
		t.diag = deps.LSP
	}
	return t
}

// Name returns the tool name.
func (t *EditTool) Name() string { return "edit" }

// Description returns the model-facing tool description.
func (t *EditTool) Description() string { return editDescription }

// Schema returns the JSON argument schema.
func (t *EditTool) Schema() json.RawMessage { return editSchema }

// Run executes the edit tool.
func (t *EditTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}
	if in.OldString == "" {
		return errorResult("old_string is required"), nil
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

	// Guard against stale reads: if the file changed on disk since the model
	// last read it, refuse the edit and ask for a re-read.
	if t.deps.FileTracker != nil && t.deps.SessionID != "" {
		changed, conflictErr := t.deps.FileTracker.HasConflict(ctx, t.deps.SessionID, path)
		if conflictErr != nil {
			return errorResult(fmt.Sprintf("checking file freshness for %s: %v", path, conflictErr)), nil
		}
		if changed {
			return errorResult(fmt.Sprintf(
				"file %s has been modified on disk since it was last read in this session — view the file again before editing to avoid clobbering changes",
				path,
			)), nil
		}
	}

	oldContent, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading file %s: %w", path, err)
	}
	text := string(oldContent)
	outcome := applyReplacement(text, in.OldString, in.NewString, in.ReplaceAll)
	switch outcome.status {
	case replaceNotFound:
		hint := nearestMatchHint(text, in.OldString)
		msg := "old_string was not found in " + path + ". old_string must match exactly, including all whitespace and newlines."
		if hint != "" {
			msg += "\n" + hint
		}
		return errorResult(msg), nil
	case replaceAmbiguous:
		return errorResult(fmt.Sprintf(
			"Found %d occurrences of old_string in %s. Each old_string must be unique — provide more surrounding context to make it unique (or set replace_all to true to replace every match).",
			outcome.found, path,
		)), nil
	}

	newText := outcome.text
	if newText == text {
		return errorResult("edit produced no changes"), nil
	}
	if err := fsext.AtomicWrite(path, []byte(newText), 0o644); err != nil {
		return Result{}, fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := t.recordWrite(ctx, path, oldContent, []byte(newText)); err != nil {
		return Result{}, err
	}

	content := fmt.Sprintf("edited %s (%d replacement(s))", path, outcome.count)
	if outcome.strategy != "" {
		content += fmt.Sprintf(" [matched via flexible %s matching]", outcome.strategy)
	}
	metadata := map[string]any{
		"path":         path,
		"replacements": outcome.count,
	}
	if outcome.strategy != "" {
		metadata["match_strategy"] = outcome.strategy
	}
	if note := postWriteDiagnostics(ctx, t.diag, t.deps.WorkDir, path); note != "" {
		content += "\n\n" + note
		metadata["diagnostics"] = note
	}
	return Result{Content: content, Metadata: metadata}, nil
}

func (t *EditTool) checkPermission(ctx context.Context, path string, raw json.RawMessage) error {
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

func (t *EditTool) recordWrite(ctx context.Context, path string, oldContent, newContent []byte) error {
	if t.deps.FileTracker == nil || t.deps.SessionID == "" {
		return nil
	}
	_, err := t.deps.FileTracker.RecordWrite(ctx, t.deps.SessionID, path, oldContent, newContent)
	if err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(t.deps.SessionID, path)
	return nil
}

func countForReport(count int, replaceAll bool) int {
	if replaceAll {
		return count
	}
	return 1
}

// nearestMatchHint examines text for content that resembles target and returns
// a concise, actionable hint the model can use to correct its next attempt.
// Three cases are handled, in priority order:
//
//  1. Whitespace-only difference: the stripped forms match — report the actual
//     on-disk text so the model can copy the correct indentation.
//  2. Close substring: a trimmed line in the file contains the first trimmed
//     line of target (or vice-versa) — show a few lines of context with line
//     numbers so the model can widen its anchor.
//  3. No near match found: return the empty string (the caller omits the hint).
func nearestMatchHint(text, target string) string {
	// Case 1: whitespace-normalised match.
	normalised := strings.Join(strings.Fields(text), " ")
	normTarget := strings.Join(strings.Fields(target), " ")
	if normTarget != "" && strings.Contains(normalised, normTarget) {
		// Find the byte region in the original text that matches when stripped.
		// Walk lines to locate the block that collapses to the target.
		lines := strings.Split(text, "\n")
		tLines := strings.Split(strings.TrimSpace(target), "\n")
		firstTrimmed := strings.TrimSpace(tLines[0])
		for i, l := range lines {
			if strings.TrimSpace(l) == firstTrimmed {
				lo := i
				hi := i + len(tLines)
				if hi > len(lines) {
					hi = len(lines)
				}
				snippet := buildSnippet(lines, lo, hi, 0)
				return "Near-match found — the on-disk text differs only in whitespace/indentation. Actual text:\n" + snippet
			}
		}
		return "Near-match found — the on-disk text differs only in whitespace/indentation. Re-view the file to copy the exact text."
	}

	// Case 2: a trimmed line of the file contains the first trimmed line of target.
	lines := strings.Split(text, "\n")
	tLines := strings.Split(strings.TrimSpace(target), "\n")
	if len(tLines) == 0 {
		return ""
	}
	firstTrimmed := strings.TrimSpace(tLines[0])
	if firstTrimmed == "" {
		return ""
	}
	for i, l := range lines {
		if strings.Contains(strings.TrimSpace(l), firstTrimmed) ||
			strings.Contains(firstTrimmed, strings.TrimSpace(l)) {
			snippet := buildSnippet(lines, i, i+1, 3)
			return "Closest region found at line " + itoa(i+1) + " (context shown):\n" + snippet
		}
	}

	return ""
}

// buildSnippet renders lines[lo:hi] with a context of ctx lines on each side,
// prefixed with 1-based line numbers, suitable for terminal display.
func buildSnippet(lines []string, lo, hi, ctx int) string {
	start := lo - ctx
	if start < 0 {
		start = 0
	}
	end := hi + ctx
	if end > len(lines) {
		end = len(lines)
	}
	width := len(itoa(end))
	var b strings.Builder
	for i := start; i < end; i++ {
		line := lines[i]
		// Guard against non-printable / binary content that would confuse the model.
		if !utf8.ValidString(line) {
			line = "<binary data>"
		}
		b.WriteString(fmt.Sprintf("%*d | %s\n", width, i+1, line))
	}
	return strings.TrimRight(b.String(), "\n")
}

// itoa is a small helper to avoid importing strconv throughout this file.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
