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

	"github.com/arbazkhan971/bharatcode/internal/diffutil"
	"github.com/arbazkhan971/bharatcode/internal/lsp"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// CodeActionSource is the LSP capability consumed by the codeactions tool. The
// *lsp.Manager satisfies it; tests substitute a fake.
type CodeActionSource interface {
	CodeActions(ctx context.Context, file string, rng lsp.Range, only []string) ([]lsp.CodeAction, error)
	ResolveCodeAction(ctx context.Context, file string, action lsp.CodeAction) (lsp.CodeAction, error)
}

type codeActionsTool struct {
	source  CodeActionSource
	workDir string
	deps    Dependencies
	diag    editDiagnoser
}

type codeActionsArgs struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Apply     int    `json:"apply,omitempty"`
	Preview   bool   `json:"preview,omitempty"`
}

var schemaCodeActions = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "line"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative path to the file to inspect."
    },
    "line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based line where the action should apply, as reported by diagnostics/symbols/grep/view."
    },
    "column": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based start column on that line. Defaults to 1 (start of line)."
    },
    "end_line": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based end line of the selection. Defaults to line (a single-line selection)."
    },
    "end_column": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based end column. Defaults to column (a cursor position rather than a span)."
    },
    "kind": {
      "type": "string",
      "description": "Restrict to actions of this LSP CodeActionKind, including its sub-kinds: \"quickfix\" (error/warning fixes), \"refactor\" (extract/inline/rewrite), \"source\" (whole-file actions like \"source.organizeImports\"). A dotted kind narrows further, e.g. \"refactor.extract\". Omit to list every available action."
    },
    "apply": {
      "type": "integer",
      "minimum": 1,
      "description": "1-based index of an action from a prior listing to apply. Only edit-based actions (organize imports, quick fixes) can be applied; server-side commands cannot. Omit to just list the available actions."
    },
    "preview": {
      "type": "boolean",
      "description": "Used with apply: compute and show the diff the action would produce without writing anything to disk. Use it to inspect a refactoring that may touch several files before committing; re-run with preview omitted (or false) to apply."
    }
  }
}`)

//go:embed codeactions.md
var codeActionsDescription string

func newCodeActionsTool(deps Dependencies) Tool {
	t := &codeActionsTool{workDir: deps.WorkDir, deps: deps}
	// A nil *lsp.Manager assigned to the CodeActionSource interface would produce
	// a non-nil interface wrapping a nil pointer, defeating the t.source == nil
	// guard in Run and panicking on the first method call. Only adopt the source
	// when the manager is actually present.
	if deps.LSP != nil {
		t.source = deps.LSP
		// The same manager re-checks each touched file afterwards so the model
		// sees any errors the applied action introduced, matching the
		// edit/write/rename tools.
		t.diag = deps.LSP
	}
	return t
}

func (t *codeActionsTool) Name() string {
	return "codeactions"
}

func (t *codeActionsTool) Description() string {
	return codeActionsDescription
}

func (t *codeActionsTool) Schema() json.RawMessage {
	return schemaCodeActions
}

func (t *codeActionsTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args codeActionsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid codeactions arguments: " + err.Error()), nil
	}
	if t.source == nil {
		return errorResult("codeactions is unavailable: no LSP manager configured"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult("codeactions requires a path"), nil
	}
	if args.Line < 1 {
		return errorResult("codeactions requires a 1-based line (>= 1)"), nil
	}

	col := args.Column
	if col < 1 {
		col = 1
	}
	endLine := args.EndLine
	if endLine < 1 {
		endLine = args.Line
	}
	endCol := args.EndColumn
	if endCol < 1 {
		endCol = col
	}

	root, err := workspaceRoot(t.workDir)
	if err != nil {
		return Result{}, err
	}
	path, rerr := resolveWorkspacePath(root, args.Path)
	if rerr != nil {
		return errorResult(rerr.Error()), nil
	}

	// LSP positions are 0-based; the model speaks the 1-based coordinates that
	// diagnostics/symbols/grep/view emit.
	rng := lsp.Range{
		Start: lsp.Position{Line: args.Line - 1, Character: col - 1},
		End:   lsp.Position{Line: endLine - 1, Character: endCol - 1},
	}

	// A kind filter is forwarded to the server as the request's "only" restriction
	// so it computes the matching whole-file "source.*" actions some servers gate
	// behind an explicit request; the response is still filtered client-side below
	// to honour the kind hierarchy precisely (a server may ignore "only").
	kind := strings.TrimSpace(args.Kind)
	var only []string
	if kind != "" {
		only = []string{kind}
	}

	actions, err := t.source.CodeActions(ctx, path, rng, only)
	if err != nil {
		return Result{}, fmt.Errorf("getting code actions at %s:%d:%d: %w", args.Path, args.Line, col, err)
	}

	if kind != "" {
		actions = filterCodeActionsByKind(actions, kind)
	}

	ordered := orderedCodeActions(actions)
	if args.Apply > 0 {
		return t.applyCodeAction(ctx, path, ordered, args.Apply, args.Preview, raw)
	}
	if len(ordered) == 0 && kind != "" {
		return Result{Content: fmt.Sprintf("No code actions of kind %q available.", kind)}, nil
	}
	return codeActionsResult(ordered), nil
}

// filterCodeActionsByKind keeps the actions whose Kind matches the requested
// CodeActionKind, applying the LSP hierarchy rule: a kind matches the filter
// when it equals it exactly or is a sub-kind (the filter followed by a "."
// segment). So "quickfix" admits "quickfix" and "quickfix.import", and "source"
// admits "source.organizeImports", but "source" does not match a bare
// "sourcery" kind. The match is case-insensitive since servers are inconsistent
// about kind casing. Actions with an empty Kind never match a non-empty filter.
func filterCodeActionsByKind(actions []lsp.CodeAction, kind string) []lsp.CodeAction {
	want := strings.ToLower(kind)
	out := make([]lsp.CodeAction, 0, len(actions))
	for _, a := range actions {
		have := strings.ToLower(strings.TrimSpace(a.Kind))
		if have == want || strings.HasPrefix(have, want+".") {
			out = append(out, a)
		}
	}
	return out
}

// applyCodeAction applies the workspace edit of the action at the given 1-based
// index in ordered (the same numbering codeActionsResult renders). Only
// edit-bearing actions can be applied locally; server-side commands are
// rejected with guidance. Each touched file is written atomically and recorded,
// and the result carries a unified diff per file like the rename/edit tools.
// When preview is true it computes and reports the same per-file diffs but
// writes nothing, records nothing, skips the permission check, and skips the
// post-write diagnostics re-check, so the model can inspect a wide-reaching
// refactoring before committing — mirroring the rename tool's preview mode.
func (t *codeActionsTool) applyCodeAction(ctx context.Context, path string, ordered []lsp.CodeAction, idx int, preview bool, raw json.RawMessage) (Result, error) {
	if idx > len(ordered) {
		if len(ordered) == 0 {
			return errorResult("cannot apply: no code actions available for this range"), nil
		}
		return errorResult(fmt.Sprintf("cannot apply action %d: only %d action(s) available", idx, len(ordered))), nil
	}
	action := ordered[idx-1]
	title := strings.TrimSpace(action.Title)
	if title == "" {
		title = "(untitled action)"
	}
	// A server can mark an action unavailable in this context (e.g. "cannot
	// extract: selection spans a statement boundary"); applying it would be a
	// no-op or an error, so refuse up front and pass the reason through.
	if action.Disabled != "" {
		return errorResult(fmt.Sprintf("cannot apply %q: the server disabled it (%s)", title, action.Disabled)), nil
	}
	// Servers that advertise resolveProvider (gopls, rust-analyzer) often return
	// refactorings with an empty edit and defer computing it to a
	// codeAction/resolve round-trip. When the chosen action carries no edit but
	// does carry resolve data, ask the server to populate it before deciding the
	// action is unapplyable. A resolve failure is non-fatal: fall through to the
	// existing "no edits"/"server-side command" handling below.
	if len(action.Edit.Changes) == 0 && len(action.Data) > 0 {
		if resolved, rerr := t.source.ResolveCodeAction(ctx, path, action); rerr == nil {
			resolved.Title = action.Title
			action = resolved
		}
	}
	if len(action.Edit.Changes) == 0 {
		// An action may consist solely of file create/rename/delete operations (e.g.
		// a "move file" refactor). There are no text edits to apply, but the model
		// must still learn what the server wanted to do.
		if note := resourceOpsNote(action.Edit.ResourceOps, t.workDir); note != "" {
			return Result{
				Content:  fmt.Sprintf("No text edits were applied for %q.\n\n%s", title, note),
				Metadata: map[string]any{"resource_ops": len(action.Edit.ResourceOps)},
			}, nil
		}
		if action.Command != nil && action.Command.Command != "" {
			return errorResult(fmt.Sprintf("cannot apply %q: it is a server-side command (%s), not an inline edit", title, action.Command.Command)), nil
		}
		return errorResult(fmt.Sprintf("cannot apply %q: the action carries no edits", title)), nil
	}

	paths := make([]string, 0, len(action.Edit.Changes))
	for p := range action.Edit.Changes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !isInsideWorkDir(p, t.workDir) {
			return errorResult("cannot apply: action would edit a file outside the workspace: " + p), nil
		}
	}

	// A preview writes nothing, so it does not need write permission.
	if !preview {
		if err := t.checkPermission(ctx, raw); err != nil {
			return errorResult(err.Error()), nil
		}
	}

	type pending struct {
		path       string
		oldContent []byte
		newContent []byte
		edits      int
	}
	updates := make([]pending, 0, len(paths))
	totalEdits := 0
	for _, p := range paths {
		edits := action.Edit.Changes[p]
		if len(edits) == 0 {
			continue
		}
		oldContent, err := os.ReadFile(p)
		if err != nil {
			return Result{}, fmt.Errorf("reading file %s: %w", p, err)
		}
		newText, err := applyTextEdits(string(oldContent), edits)
		if err != nil {
			return errorResult(fmt.Sprintf("applying code action edits to %s: %v", p, err)), nil
		}
		if newText == string(oldContent) {
			continue
		}
		updates = append(updates, pending{path: p, oldContent: oldContent, newContent: []byte(newText), edits: len(edits)})
		totalEdits += len(edits)
	}
	if len(updates) == 0 {
		if preview {
			return Result{Content: fmt.Sprintf("preview of %q: the edits would leave every file unchanged (nothing written).", title)}, nil
		}
		return Result{Content: fmt.Sprintf("Applied %q: the edits left every file unchanged.", title)}, nil
	}

	if !preview {
		for _, u := range updates {
			if err := fsext.AtomicWrite(u.path, u.newContent, 0o644); err != nil {
				return Result{}, fmt.Errorf("writing file %s: %w", u.path, err)
			}
			if err := t.recordWrite(ctx, u.path, u.oldContent, u.newContent); err != nil {
				return Result{}, err
			}
		}
	}

	var b strings.Builder
	if preview {
		fmt.Fprintf(&b, "preview of %q: would make %d edit(s) across %d file(s) (nothing written)\n", title, totalEdits, len(updates))
	} else {
		fmt.Fprintf(&b, "applied %q: %d edit(s) across %d file(s)\n", title, totalEdits, len(updates))
	}
	diffs := make(map[string]string, len(updates))
	for _, u := range updates {
		rel := u.path
		if r, err := filepath.Rel(t.workDir, u.path); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		fmt.Fprintf(&b, "  %s (%d edit(s))\n", rel, u.edits)
		if d := diffutil.Unified(string(u.oldContent), string(u.newContent)); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
			diffs[rel] = d
		}
	}

	metadata := map[string]any{"applied": title, "files": len(updates), "edits": totalEdits}
	if preview {
		metadata["preview"] = true
	}
	if len(diffs) > 0 {
		metadata["diffs"] = diffs
	}
	// A refactor may bundle file create/rename/delete operations with its text
	// edits (gopls "extract to new file", rust-analyzer "move to module"). Those
	// are not applied, so warn rather than letting the model assume the refactor
	// is complete.
	if note := resourceOpsNote(action.Edit.ResourceOps, t.workDir); note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
		metadata["resource_ops"] = len(action.Edit.ResourceOps)
	}

	// Applying a quick fix or refactor can introduce errors (a now-unused import,
	// a name collision), so re-check each touched file and surface the problems,
	// as the edit/write/rename tools do. Files are processed in the same
	// sorted-path order for deterministic output. A preview wrote nothing, so
	// there is nothing new to re-check.
	if t.diag != nil && !preview {
		var notes []string
		for _, u := range updates {
			if note := postWriteDiagnostics(ctx, t.diag, t.workDir, u.path); note != "" {
				notes = append(notes, note)
			}
		}
		if len(notes) > 0 {
			joined := strings.Join(notes, "\n\n")
			fmt.Fprintf(&b, "\n%s", joined)
			metadata["diagnostics"] = joined
		}
	}

	return Result{Content: strings.TrimRight(b.String(), "\n"), Metadata: metadata}, nil
}

func (t *codeActionsTool) checkPermission(ctx context.Context, raw json.RawMessage) error {
	if t.deps.Permission == nil {
		return nil
	}
	args := map[string]any{}
	_ = json.Unmarshal(raw, &args)
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

func (t *codeActionsTool) recordWrite(ctx context.Context, path string, oldContent, newContent []byte) error {
	if t.deps.FileTracker == nil || t.deps.SessionID == "" {
		return nil
	}
	if _, err := t.deps.FileTracker.RecordWrite(ctx, t.deps.SessionID, path, oldContent, newContent); err != nil {
		return fmt.Errorf("recording write for %s: %w", path, err)
	}
	markViewed(t.deps.SessionID, path)
	return nil
}

// orderedCodeActions sorts actions by kind then title and drops adjacent
// duplicates (by rendered entry), yielding the stable order that both the
// listing and `apply` index into.
func orderedCodeActions(actions []lsp.CodeAction) []lsp.CodeAction {
	sorted := make([]lsp.CodeAction, len(actions))
	copy(sorted, actions)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Title < sorted[j].Title
	})

	out := make([]lsp.CodeAction, 0, len(sorted))
	var last string
	for _, a := range sorted {
		entry := codeActionEntry(a)
		if entry == last {
			continue
		}
		last = entry
		out = append(out, a)
	}
	return out
}

// codeActionEntry renders one action as its title, kind in brackets when
// present, and a note of how it would take effect.
func codeActionEntry(a lsp.CodeAction) string {
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = "(untitled action)"
	}
	var line strings.Builder
	line.WriteString(title)
	if a.Kind != "" {
		fmt.Fprintf(&line, " [%s]", a.Kind)
	}
	// The server's preferred hint marks the canonical fix for the context, so the
	// model can pick a default without weighing the alternatives.
	if a.IsPreferred {
		line.WriteString(" (preferred)")
	}
	// When the action is a quick fix keyed on one or more diagnostics, name the
	// problem(s) it resolves so the model can match a fix to the error it saw from
	// the diagnostics tool without guessing from the title alone.
	if note := codeActionFixesNote(a.Diagnostics); note != "" {
		line.WriteString(" ")
		line.WriteString(note)
	}
	if a.Disabled != "" {
		// A disabled action cannot be applied; show why instead of an apply note so
		// the model does not waste an apply call on it.
		fmt.Fprintf(&line, " (disabled: %s)", a.Disabled)
	} else if note := codeActionApplyNote(a); note != "" {
		fmt.Fprintf(&line, " (%s)", note)
	}
	return line.String()
}

// codeActionFixesMessageCap bounds how many characters of a single diagnostic
// message codeActionFixesNote renders. A server can key a quick fix on a
// diagnostic whose message runs to a paragraph (a type-mismatch dump, a long
// rustc explanation); the note is an inline annotation on a one-line list entry,
// so an over-long message is clipped to keep each entry scannable. The cap
// mirrors the navigate tool's snippet truncation philosophy.
const codeActionFixesMessageCap = 80

// codeActionFixesNote renders the parenthesized "fixes …" annotation naming the
// diagnostic(s) an action resolves, e.g. `(fixes "undefined: foo")`. The first
// message is shown (clipped to codeActionFixesMessageCap); when an action
// resolves several diagnostics a "(+N more)" tail records the rest so the entry
// stays one line. Returns "" when the action fixes no diagnostic.
func codeActionFixesNote(messages []string) string {
	if len(messages) == 0 {
		return ""
	}
	first := truncateLine(strings.TrimSpace(messages[0]), codeActionFixesMessageCap)
	if len(messages) == 1 {
		return fmt.Sprintf("(fixes %q)", first)
	}
	return fmt.Sprintf("(fixes %q +%d more)", first, len(messages)-1)
}

// Metadata keys the codeactions listing sets so downstream consumers (the agent
// loop, the TUI) can react to action counts without re-parsing the rendered list.
const (
	// MetadataCodeActionCount holds the int total of available code actions.
	MetadataCodeActionCount = "count"
	// MetadataCodeActionQuickfixes holds the int count of actions whose kind is
	// "quickfix" or a "quickfix.*" sub-kind (the set the agent should consider
	// first for error fixes).
	MetadataCodeActionQuickfixes = "quickfixes"
)

// codeActionsResult renders an already-ordered action list as a numbered list.
// The numbering matches the index `apply` expects. An empty input reports
// directly.
func codeActionsResult(ordered []lsp.CodeAction) Result {
	if len(ordered) == 0 {
		return Result{Content: "No code actions available."}
	}

	var b strings.Builder
	for i, a := range ordered {
		fmt.Fprintf(&b, "%d. %s\n", i+1, codeActionEntry(a))
	}
	return Result{
		Content:  strings.TrimRight(b.String(), "\n"),
		Metadata: codeActionsListMetadata(ordered),
	}
}

// codeActionsListMetadata tallies the action list so callers can react to
// availability without re-parsing the rendered text — mirroring diagnosticsMetadata.
func codeActionsListMetadata(ordered []lsp.CodeAction) map[string]any {
	quickfixes := 0
	for _, a := range ordered {
		k := strings.ToLower(strings.TrimSpace(a.Kind))
		if k == "quickfix" || strings.HasPrefix(k, "quickfix.") {
			quickfixes++
		}
	}
	return map[string]any{
		MetadataCodeActionCount:      len(ordered),
		MetadataCodeActionQuickfixes: quickfixes,
	}
}

// codeActionApplyNote summarizes how an action would take effect so the model
// can tell self-contained edits apart from server-side commands.
func codeActionApplyNote(a lsp.CodeAction) string {
	hasEdit := len(a.Edit.Changes) > 0
	hasCommand := a.Command != nil && a.Command.Command != ""
	switch {
	case hasEdit && hasCommand:
		return "edit + command"
	case hasEdit:
		files := len(a.Edit.Changes)
		if files == 1 {
			return "edit, 1 file"
		}
		return fmt.Sprintf("edit, %d files", files)
	case hasCommand:
		return "command: " + a.Command.Command
	case len(a.Data) > 0:
		// Servers advertising resolveProvider (gopls, rust-analyzer) list
		// refactorings with an empty Edit and defer computing it to a
		// codeAction/resolve round-trip, keyed by this Data. The action is still
		// applyable — apply resolves it first — so say so rather than leaving an
		// empty note that reads as "does nothing".
		return "resolve to apply"
	default:
		return ""
	}
}
