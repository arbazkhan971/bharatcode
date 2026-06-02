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
	cmdArgs := []string{"--color", "never", "--no-heading"}
	switch args.OutputMode {
	case "files_with_matches":
		cmdArgs = append(cmdArgs, "-l")
	case "count":
		cmdArgs = append(cmdArgs, "-c")
	default:
		cmdArgs = append(cmdArgs, "--line-number")
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
	return normalizeSearchOutput(root, string(out)), nil
}

func runGoGrep(ctx context.Context, root, searchPath string, args grepArgs) (string, error) {
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("compiling grep pattern: %w", err)
	}

	type fileMatch struct {
		path  string
		lines []string
		count int
	}
	var matches []fileMatch
	err = filepath.WalkDir(searchPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking grep path %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && path != searchPath {
				return filepath.SkipDir
			}
			return nil
		}
		if args.Include != "" {
			ok, err := filepath.Match(args.Include, entry.Name())
			if err != nil {
				return fmt.Errorf("matching include glob %q: %w", args.Include, err)
			}
			if !ok {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("opening grep file %s: %w", path, err)
		}
		defer f.Close()

		rel := relativeSlash(root, path)
		var fm fileMatch
		fm.path = rel
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				fm.count++
				fm.lines = append(fm.lines, fmt.Sprintf("%s:%d:%s", rel, lineNo, line))
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scanning grep file %s: %w", path, err)
		}
		if fm.count > 0 {
			matches = append(matches, fm)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching files: %w", err)
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
	return strings.TrimRight(b.String(), "\n"), nil
}

func normalizeSearchOutput(root, out string) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No matches found."
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if filepath.IsAbs(line) {
			lines[i] = relativeSlash(root, line)
			continue
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 && filepath.IsAbs(line[:idx]) {
			lines[i] = relativeSlash(root, line[:idx]) + line[idx:]
		}
	}
	return strings.Join(lines, "\n")
}

func relativeSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
