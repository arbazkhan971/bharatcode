package tools

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// grepMatchCap is the total number of matching lines (content mode) or files
// (files_with_matches / count mode) returned before truncation.  It mirrors
// the cap applied on the rg path so both paths produce bounded, consistent
// output regardless of whether ripgrep is installed.
const grepMatchCap = 1000

type grepTool struct {
	deps Dependencies
}

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Include    string `json:"include,omitempty"`
	OutputMode string `json:"output_mode,omitempty"`
}

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext
	schemaGrep     = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["pattern"],
  "properties": {
    "pattern": {"type": "string", "description": "Regular expression to search for."},
    "path": {"type": "string", "description": "Workspace-relative file or directory to search."},
    "include": {"type": "string", "description": "Optional file glob such as *.go."},
    "output_mode": {"type": "string", "enum": ["content", "files_with_matches", "count"], "description": "Shape of the search results."}
  }
}`)
)

//go:embed grep.md
var grepDescription string

func newGrepTool(deps Dependencies) Tool {
	return &grepTool{deps: deps}
}

func (t *grepTool) Name() string {
	return "grep"
}

func (t *grepTool) Description() string {
	return grepDescription
}

func (t *grepTool) Schema() json.RawMessage {
	return schemaGrep
}

func (t *grepTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args grepArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid grep arguments: " + err.Error()), nil
	}
	args.Pattern = strings.TrimSpace(args.Pattern)
	if args.Pattern == "" {
		return errorResult("pattern is required"), nil
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.OutputMode == "" {
		args.OutputMode = "content"
	}
	if args.OutputMode != "content" && args.OutputMode != "files_with_matches" && args.OutputMode != "count" {
		return errorResult("output_mode must be one of content, files_with_matches, or count"), nil
	}

	root, err := workspaceRoot(t.deps.WorkDir)
	if err != nil {
		return Result{}, err
	}
	searchPath, err := resolveWorkspacePath(root, args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	if rg, err := lookPath("rg"); err == nil {
		content, err := runRipgrep(ctx, rg, root, searchPath, args)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: content}, nil
	}

	content, err := runGoGrep(ctx, root, searchPath, args)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: content}, nil
}

func runRipgrep(ctx context.Context, rg, root, searchPath string, args grepArgs) (string, error) {
	cmdArgs := []string{
		"--color", "never",
		"--no-heading",
		// smart-case: lowercase pattern ⇒ case-insensitive; mixed-case ⇒ exact.
		"--smart-case",
		// skip binary files explicitly (rg default, but stated for clarity).
		"--binary",
		// cap individual line width so minified/generated files don't flood output.
		"--max-columns", "500",
		"--max-columns-preview",
	}

	switch args.OutputMode {
	case "files_with_matches":
		cmdArgs = append(cmdArgs, "-l")
		// rg has no per-mode total-file cap flag; we trim after the fact.
	case "count":
		cmdArgs = append(cmdArgs, "-c")
	default:
		cmdArgs = append(cmdArgs, "--line-number")
		// Cap matches per file so a single huge file doesn't dominate.
		cmdArgs = append(cmdArgs, "--max-count", "100")
	}

	if args.Include != "" {
		cmdArgs = append(cmdArgs, "--glob", args.Include)
	}
	cmdArgs = append(cmdArgs, args.Pattern, searchPath)

	cmd := commandContext(ctx, rg, cmdArgs...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "No matches found.", nil
		}
		return "", fmt.Errorf("running rg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return capAndNormalize(root, string(out), args.OutputMode), nil
}

// capAndNormalize trims output to grepMatchCap lines/entries, relativises
// absolute paths, and appends a notice when results were capped.
// The outputMode parameter is accepted for callers' convenience but the
// cap is applied uniformly by line count.
func capAndNormalize(root, out, _ string) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No matches found."
	}
	lines := strings.Split(out, "\n")
	// Relativise any absolute paths that rg may emit (e.g. when searchPath is absolute).
	for i, line := range lines {
		if filepath.IsAbs(line) {
			lines[i] = relativeSlash(root, line)
			continue
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 && filepath.IsAbs(line[:idx]) {
			lines[i] = relativeSlash(root, line[:idx]) + line[idx:]
		}
	}
	if len(lines) > grepMatchCap {
		trimmed := lines[:grepMatchCap]
		return strings.Join(trimmed, "\n") +
			fmt.Sprintf("\n[results capped: showing first %d of %d entries]", grepMatchCap, len(lines))
	}
	return strings.Join(lines, "\n")
}

// ignoredDirs are directories the Go fallback always skips, matching rg's
// defaults plus the most common noise sources in JS/Go projects.
var ignoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	".svn":         true,
	".hg":          true,
}

// isBinaryChunk returns true when the leading bytes of a file contain a NUL,
// which is the standard heuristic used by rg/git to classify binary files.
func isBinaryChunk(chunk []byte) bool {
	return bytes.IndexByte(chunk, 0) >= 0
}

// loadRootGitignore parses a .gitignore at the workspace root and returns a
// set of directory patterns to skip.  Only simple directory-name entries and
// patterns ending in "/" are supported — this covers the common cases
// (node_modules/, dist/, vendor/) without a full gitignore parser.
func loadRootGitignore(root string) map[string]bool {
	skip := make(map[string]bool)
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return skip
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Normalize trailing slash — "node_modules/" → "node_modules".
		name := strings.TrimRight(line, "/")
		// Only honour plain directory names without wildcards, for simplicity.
		if !strings.ContainsAny(name, "*?[") && !strings.Contains(name, "/") {
			skip[name] = true
		}
	}
	return skip
}

// compileSmartCase compiles the pattern using smart-case semantics:
// if the pattern is entirely lowercase, the returned regexp is case-insensitive;
// if it contains any uppercase letter, it is compiled verbatim (exact case).
func compileSmartCase(pattern string) (*regexp.Regexp, error) {
	caseSensitive := false
	for _, r := range pattern {
		if r >= 'A' && r <= 'Z' {
			caseSensitive = true
			break
		}
	}
	if !caseSensitive {
		return regexp.Compile("(?i)" + pattern)
	}
	return regexp.Compile(pattern)
}

func runGoGrep(ctx context.Context, root, searchPath string, args grepArgs) (string, error) {
	re, err := compileSmartCase(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("compiling grep pattern: %w", err)
	}

	gitignoreDirs := loadRootGitignore(root)

	type fileMatch struct {
		path  string
		lines []string
		count int
	}

	var (
		matches   []fileMatch
		totalRows int  // content lines accumulated across all files
		capped    bool // true when we hit grepMatchCap and bailed early
	)

	// capErr is a sentinel used to stop WalkDir early when the cap is hit.
	capErr := errors.New("cap reached")

	walkErr := filepath.WalkDir(searchPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking grep path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			name := entry.Name()
			// Always skip the source-control and noise dirs, even when path
			// equals searchPath (the user may have pointed grep into one of
			// these intentionally, in which case we skip its sub-entries but
			// still enter the top-level directory itself only if it IS the
			// searchPath so we don't lose the ability to search inside it
			// when explicitly targeted).
			if path != searchPath && (ignoredDirs[name] || gitignoreDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}

		if args.Include != "" {
			ok, matchErr := filepath.Match(args.Include, entry.Name())
			if matchErr != nil {
				return fmt.Errorf("matching include glob %q: %w", args.Include, matchErr)
			}
			if !ok {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			// Non-fatal: skip unreadable files.
			return nil //nolint:nilerr
		}
		defer f.Close()

		// Binary detection: read up to 8 KB and look for a NUL byte.
		probe := make([]byte, 8192)
		n, _ := f.Read(probe)
		if isBinaryChunk(probe[:n]) {
			return nil
		}
		// Seek back to the start for the line scanner.
		if _, err := f.Seek(0, 0); err != nil {
			return nil //nolint:nilerr
		}

		rel := relativeSlash(root, path)
		var fm fileMatch
		fm.path = rel
		scanner := bufio.NewScanner(f)
		lineNo := 0
		fileCapped := false
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				fm.count++
				// In content mode, stop adding lines from this file once the
				// global cap is hit so we never exceed it within one file.
				if args.OutputMode == "content" || args.OutputMode == "" {
					if totalRows < grepMatchCap {
						fm.lines = append(fm.lines, fmt.Sprintf("%s:%d:%s", rel, lineNo, line))
						totalRows++
						if totalRows >= grepMatchCap {
							fileCapped = true
						}
					}
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			// Non-fatal: skip files we can't fully read.
			return nil //nolint:nilerr
		}
		if fm.count > 0 {
			matches = append(matches, fm)
		}

		// Enforce the total match cap after each file so we stop early.
		switch args.OutputMode {
		case "files_with_matches", "count":
			if len(matches) >= grepMatchCap {
				capped = true
				return capErr
			}
		default:
			if fileCapped || totalRows >= grepMatchCap {
				capped = true
				return capErr
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, capErr) {
		return "", fmt.Errorf("searching files: %w", walkErr)
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].path < matches[j].path })
	if len(matches) == 0 {
		return "No matches found.", nil
	}

	var b strings.Builder
	for _, match := range matches {
		switch args.OutputMode {
		case "files_with_matches":
			b.WriteString(match.path)
			b.WriteByte('\n')
		case "count":
			fmt.Fprintf(&b, "%s:%d\n", match.path, match.count)
		default:
			for _, line := range match.lines {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	result := strings.TrimRight(b.String(), "\n")
	if capped {
		result += fmt.Sprintf("\n[results capped: showing first %d matches]", grepMatchCap)
	}
	return result, nil
}

func relativeSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
