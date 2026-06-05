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
	"time"
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
	// Honor .gitignore so glob skips vendored/build directories (node_modules,
	// dist, …) the same way ls and grep (rg) already do. Nested .gitignore files
	// are respected as well — matching rg's default — so a build/.gitignore can
	// hide artifacts the root .gitignore never mentions. Without this, glob is
	// the lone file-discovery tool that floods the model with ignored paths.
	ignore := newGitignoreStack(root)
	var matches []globMatch
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking glob path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		relRoot := relativeSlash(root, path)
		if entry.IsDir() {
			if entry.Name() == ".git" && path != base {
				return filepath.SkipDir
			}
			// Prune ignored directories entirely so we never descend into them.
			if path != base && ignore.ignored(path, entry.Name(), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore.ignored(path, entry.Name(), false) {
			return nil
		}
		relBase := relativeSlash(base, path)
		if re.MatchString(relBase) || re.MatchString(relRoot) {
			// Stat the entry for its modification time so results can be ordered
			// newest-first (see sortGlobMatches). A stat failure (e.g. the file
			// was removed mid-walk) falls back to the zero time, sinking the path
			// to the bottom rather than dropping it.
			var modTime time.Time
			if fi, statErr := entry.Info(); statErr == nil {
				modTime = fi.ModTime()
			}
			matches = append(matches, globMatch{path: relRoot, modTime: modTime})
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("matching glob: %w", err)
	}
	if len(matches) == 0 {
		return Result{Content: "No files matched."}, nil
	}
	sortGlobMatches(matches)
	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = m.path
	}
	return Result{Content: strings.Join(paths, "\n")}, nil
}

// globMatch pairs a matched workspace-relative path with its modification time
// so results can be ordered by recency.
type globMatch struct {
	path    string
	modTime time.Time
}

// sortGlobMatches orders matches newest-first by modification time, matching the
// behavior of Claude Code's and opencode's glob tools — the most recently
// touched files surface first, which is what an agent asking "what changed?"
// wants. Ties (and zero-time stat failures) fall back to a lexicographic path
// order so output stays deterministic.
func sortGlobMatches(matches []globMatch) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].modTime.Equal(matches[j].modTime) {
			return matches[i].path < matches[j].path
		}
		return matches[i].modTime.After(matches[j].modTime)
	})
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	// Brace alternation ({a,b,c}) is honored only when braces are well-formed;
	// otherwise '{', '}' and ',' fall through to literal matching. This mirrors
	// ripgrep/doublestar, where an unmatched brace is just a literal character,
	// and lets common patterns such as **/*.{ts,tsx} match.
	braces := bracesBalanced(pattern)

	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch {
		case ch == '*':
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
		case ch == '?':
			b.WriteString("[^/]")
		case braces && ch == '{':
			depth++
			b.WriteString("(?:")
		case braces && ch == '}' && depth > 0:
			depth--
			b.WriteString(")")
		case braces && ch == ',' && depth > 0:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("compiling glob pattern: %w", err)
	}
	return re, nil
}

// bracesBalanced reports whether pattern contains at least one brace group and
// every '{' has a matching '}' with no stray closer appearing first. Only then
// does globRegexp treat braces as alternation; an unbalanced or absent brace is
// left to literal matching so a pattern like "a}b" or "file{" is not misread.
func bracesBalanced(pattern string) bool {
	depth := 0
	seen := false
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
			seen = true
		case '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return seen && depth == 0
}
