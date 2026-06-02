package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type globTool struct {
	deps Dependencies
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

var schemaGlob = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["pattern"],
  "properties": {
    "pattern": {"type": "string", "description": "Glob pattern. ** may cross directory boundaries."},
    "path": {"type": "string", "description": "Optional workspace-relative directory to search from."}
  }
}`)

//go:embed glob.md
var globDescription string

func newGlobTool(deps Dependencies) Tool {
	return &globTool{deps: deps}
}

func (t *globTool) Name() string {
	return "glob"
}

func (t *globTool) Description() string {
	return globDescription
}

func (t *globTool) Schema() json.RawMessage {
	return schemaGlob
}

func (t *globTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args globArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid glob arguments: " + err.Error()), nil
	}
	args.Pattern = strings.TrimSpace(filepath.ToSlash(args.Pattern))
	if args.Pattern == "" {
		return errorResult("pattern is required"), nil
	}
	if args.Path == "" {
		args.Path = "."
	}

	root, err := workspaceRoot(t.deps.WorkDir)
	if err != nil {
		return Result{}, err
	}
	base, err := resolveWorkspacePath(root, args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(base)
	if err != nil {
		return errorResult("path does not exist: " + args.Path), nil
	}
	if !info.IsDir() {
		return errorResult("path must be a directory"), nil
	}

	re, err := globRegexp(args.Pattern)
	if err != nil {
		return Result{}, err
	}
	var matches []string
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking glob path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		relBase := relativeSlash(base, path)
		relRoot := relativeSlash(root, path)
		if re.MatchString(relBase) || re.MatchString(relRoot) {
			matches = append(matches, relRoot)
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("matching glob: %w", err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return Result{Content: "No files matched."}, nil
	}
	return Result{Content: strings.Join(matches, "\n")}, nil
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
			continue
		}
		if ch == '?' {
			b.WriteString("[^/]")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(ch)))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("compiling glob pattern: %w", err)
	}
	return re, nil
}
