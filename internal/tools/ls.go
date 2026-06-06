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
)

type lsTool struct {
	deps Dependencies
}

type lsArgs struct {
	Path   string   `json:"path,omitempty"`
	Ignore []string `json:"ignore,omitempty"`
	Depth  int      `json:"depth,omitempty"`
}

// maxLSDepth caps how many levels a recursive listing descends, and maxLSEntries
// caps the total number of lines a single recursive call emits. Both guard
// against a deep or sprawling tree flooding the agent's context; the listing is
// truncated with a note rather than returning an unbounded wall of text.
const (
	maxLSDepth   = 10
	maxLSEntries = 1000
)

var schemaLS = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "Workspace-relative directory to list."},
    "ignore": {"type": "array", "items": {"type": "string"}, "description": "Additional names or glob patterns to hide."},
    "depth": {"type": "integer", "minimum": 1, "maximum": 10, "description": "How many directory levels to descend. 1 (default) lists only immediate children; higher values render an indented recursive tree (capped at 10 levels and 1000 entries)."}
  }
}`)

//go:embed ls.md
var lsDescription string

func newLSTool(deps Dependencies) Tool {
	return &lsTool{deps: deps}
}

func (t *lsTool) Name() string {
	return "ls"
}

func (t *lsTool) IsReadOnly() bool { return true }

func (t *lsTool) Description() string {
	return lsDescription
}

func (t *lsTool) Schema() json.RawMessage {
	return schemaLS
}

func (t *lsTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args lsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid ls arguments: " + err.Error()), nil
	}
	if args.Path == "" {
		args.Path = "."
	}

	root, err := workspaceRoot(t.deps.WorkDir)
	if err != nil {
		return Result{}, err
	}
	dir, err := resolveWorkspacePath(root, args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return errorResult("path does not exist: " + args.Path), nil
	}
	if !info.IsDir() {
		return errorResult("path is not a directory"), nil
	}

	// Honor .gitignore — including any in the listed directory or its ancestors
	// up to the workspace root (e.g. listing build/ respects build/.gitignore) —
	// plus the caller's extra ignore patterns.
	ignore := newGitignoreStack(root)

	depth := args.Depth
	if depth < 1 {
		depth = 1
	}
	if depth > maxLSDepth {
		depth = maxLSDepth
	}

	var lines []string
	truncated, err := t.walk(ctx, root, dir, ignore, args.Ignore, depth, "", &lines)
	if err != nil {
		return Result{}, err
	}
	if len(lines) == 0 {
		return Result{Content: "Directory is empty."}, nil
	}
	content := strings.Join(lines, "\n")
	if truncated {
		content += fmt.Sprintf("\n... (truncated at %d entries)", maxLSEntries)
	}
	return Result{Content: content}, nil
}

// walk lists dir's visible children, sorted, and recurses into subdirectories
// while remaining depth allows. Each line is indented two spaces per level so a
// recursive listing reads as a tree; directory names keep their trailing slash.
// It appends to *lines and returns true if the maxLSEntries cap was hit, in
// which case the caller stops descending.
func (t *lsTool) walk(ctx context.Context, root, dir string, ignore *gitignoreStack, extra []string, depth int, indent string, lines *[]string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("listing directory %s: %w", dir, err)
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	type child struct {
		name  string
		full  string
		isDir bool
	}
	var children []child
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		rel := relativeSlash(root, full)
		if ignore.ignored(full, entry.Name(), entry.IsDir()) {
			continue
		}
		if len(extra) > 0 && ignored(rel, entry.Name(), entry.IsDir(), extra) {
			continue
		}
		children = append(children, child{name: entry.Name(), full: full, isDir: entry.IsDir()})
	}
	sort.Slice(children, func(i, j int) bool {
		return sortName(children[i].name, children[i].isDir) < sortName(children[j].name, children[j].isDir)
	})

	for _, c := range children {
		if len(*lines) >= maxLSEntries {
			return true, nil
		}
		*lines = append(*lines, indent+sortName(c.name, c.isDir))
		if c.isDir && depth > 1 {
			truncated, err := t.walk(ctx, root, c.full, ignore, extra, depth-1, indent+"  ", lines)
			if err != nil {
				return false, err
			}
			if truncated {
				return true, nil
			}
		}
	}
	return false, nil
}

// sortName renders an entry name with a trailing slash for directories, which is
// both the displayed form and the key the listing sorts on (so "src/" orders by
// "src/"), keeping flat and recursive output consistent.
func sortName(name string, isDir bool) string {
	if isDir {
		return name + "/"
	}
	return name
}

func ignored(rel, name string, isDir bool, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		dirPattern := strings.HasSuffix(pattern, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		if dirPattern && !isDir {
			continue
		}
		if pattern == name || pattern == rel || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}
