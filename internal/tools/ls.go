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
}

var schemaLS = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "Workspace-relative directory to list."},
    "ignore": {"type": "array", "items": {"type": "string"}, "description": "Additional names or glob patterns to hide."}
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, fmt.Errorf("listing directory %s: %w", dir, err)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}

	var names []string
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		rel := relativeSlash(root, full)
		if ignore.ignored(full, entry.Name(), entry.IsDir()) {
			continue
		}
		if len(args.Ignore) > 0 && ignored(rel, entry.Name(), entry.IsDir(), args.Ignore) {
			continue
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return Result{Content: "Directory is empty."}, nil
	}
	return Result{Content: strings.Join(names, "\n")}, nil
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
