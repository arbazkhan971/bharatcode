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
	// Context lines — mirrors rg -C / -A / -B.  Context takes precedence over
	// Before/After when both are set (same semantics as rg).  Only meaningful
	// for output_mode "content" (ignored for files_with_matches / count).
	Context int `json:"context,omitempty"`
	Before  int `json:"before,omitempty"`
	After   int `json:"after,omitempty"`
	// Multiline enables patterns that span line boundaries (like rg
	// -U --multiline-dotall): the file is searched as a single buffer so
	// `.` matches newlines.  Context (before/after/context) is ignored in
	// this mode.
	Multiline bool `json:"multiline,omitempty"`
	// Type filters the search to a language by file type, like rg --type go.
	// It is a curated, machine-independent superset of the most common
	// languages (see grepTypeExtensions); combine with Include to narrow
	// further (both filters must pass).  Empty means no type filter.
	Type string `json:"type,omitempty"`
	// CaseInsensitive forces case-insensitive matching (like rg -i), overriding
	// the default smart-case behaviour.  Use it to match a mixed-case pattern
	// regardless of case (e.g. find "http" given the pattern "HTTP").
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
	// OnlyMatching prints only the matched (non-empty) part of each line, one
	// match per output row (like rg -o / --only-matching), instead of the whole
	// line.  It applies to content mode only and is ignored in multiline mode so
	// the rg and Go-fallback paths stay consistent; context options do not apply
	// when it is set.
	OnlyMatching bool `json:"only_matching,omitempty"`
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
    "output_mode": {"type": "string", "enum": ["content", "files_with_matches", "count"], "description": "Shape of the search results."},
    "context": {"type": "integer", "minimum": 0, "description": "Number of lines of context to show before and after each match (like rg -C). Takes precedence over before/after when set."},
    "before": {"type": "integer", "minimum": 0, "description": "Number of lines to show before each match (like rg -B). Ignored when context is set."},
    "after": {"type": "integer", "minimum": 0, "description": "Number of lines to show after each match (like rg -A). Ignored when context is set."},
    "multiline": {"type": "boolean", "description": "Match patterns across line boundaries (like rg -U --multiline-dotall); . matches newlines. Context options are ignored in this mode."},
    "type": {"type": "string", "description": "Filter to a language by file type, like rg --type go (e.g. go, py, js, ts, rust, java, c, cpp). Combine with include to narrow further."},
    "case_insensitive": {"type": "boolean", "description": "Force case-insensitive matching (like rg -i), overriding the default smart-case behaviour."},
    "only_matching": {"type": "boolean", "description": "Print only the matched (non-empty) parts of each line, one match per row (like rg -o). Content mode only; ignored in multiline mode and context options do not apply."}
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
	if args.Type = strings.TrimSpace(args.Type); args.Type != "" {
		if _, ok := resolveGrepType(args.Type); !ok {
			return errorResult(fmt.Sprintf("unknown type %q; supported types: %s", args.Type, grepTypeNames())), nil
		}
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

	// An explicit case_insensitive request forces -i, which overrides the
	// --smart-case above (rg applies the last case flag given).
	if args.CaseInsensitive {
		cmdArgs = append(cmdArgs, "--ignore-case")
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
		// -o prints only the matched substrings, one per line.  It is a
		// content-mode-only concern (the cap/-l/-c branches never reach here)
		// and is suppressed in multiline mode to keep the Go fallback aligned.
		if args.OnlyMatching && !args.Multiline {
			cmdArgs = append(cmdArgs, "--only-matching")
		}
	}

	if args.Multiline {
		// -U lets a pattern span newlines; --multiline-dotall makes `.`
		// match across them.  Context lines are not combined with
		// multiline mode (so the rg and Go paths stay consistent).
		cmdArgs = append(cmdArgs, "--multiline", "--multiline-dotall")
	} else if onlyMatchingContent(args) {
		// -o supersedes context (rg emits no context lines with --only-matching);
		// skip the context flags so both paths render identically.
	} else {
		// Context lines: -C takes precedence over -A/-B (rg semantics).
		ctxBefore, ctxAfter := contextLines(args)
		if ctxBefore > 0 {
			cmdArgs = append(cmdArgs, "--before-context", fmt.Sprintf("%d", ctxBefore))
		}
		if ctxAfter > 0 {
			cmdArgs = append(cmdArgs, "--after-context", fmt.Sprintf("%d", ctxAfter))
		}
	}

	if args.Include != "" {
		cmdArgs = append(cmdArgs, "--glob", args.Include)
	}
	if exts, ok := resolveGrepType(args.Type); ok {
		// Define a synthetic type from our shared table rather than relying on
		// rg's built-in defs, so the rg path and Go fallback select identically.
		for _, e := range exts {
			cmdArgs = append(cmdArgs, "--type-add", "bcgrep:*."+e)
		}
		cmdArgs = append(cmdArgs, "--type", "bcgrep")
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
// When ignoreCase is true the match is always case-insensitive, overriding
// smart-case (mirrors rg -i).
func compileSmartCase(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if ignoreCase || !hasUpper(pattern) {
		return regexp.Compile("(?i)" + pattern)
	}
	return regexp.Compile(pattern)
}

// hasUpper reports whether pattern contains an ASCII uppercase letter, the
// signal smart-case uses to switch to exact-case matching.
func hasUpper(pattern string) bool {
	for _, r := range pattern {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// onlyMatchingContent reports whether the request should print only matched
// substrings (rg -o).  It is honoured only in content mode and only when not
// multiline, so the rg path and the Go fallback select the same behaviour.
func onlyMatchingContent(args grepArgs) bool {
	return args.OnlyMatching && !args.Multiline &&
		(args.OutputMode == "content" || args.OutputMode == "")
}

// contextLines resolves before/after line counts from the three context
// fields.  When args.Context > 0 it overrides both Before and After (same
// precedence as rg -C vs -A/-B).
func contextLines(args grepArgs) (before, after int) {
	if args.Context > 0 {
		return args.Context, args.Context
	}
	return args.Before, args.After
}

// grepFileLines reads the file at path into a slice of raw text lines.
// It returns nil when the file is binary (contains a NUL in the first 8 KB)
// or cannot be opened/read.
func grepFileLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	probe := make([]byte, 8192)
	n, _ := f.Read(probe)
	if isBinaryChunk(probe[:n]) {
		return nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if scanner.Err() != nil {
		return nil
	}
	return lines
}

// buildContextOutput renders file lines + context into the output format used
// by content mode.  It mirrors rg's output:
//   - matching lines: "rel:lineNo:text"
//   - context lines:  "rel-lineNo-text"
//   - groups separated by "--" when there is a gap between context windows
//
// Only lines within [0, len(allLines)) are emitted.  The returned formatted
// lines are already ready to be appended to the output; count is the number
// of true match lines found (for cap accounting).
func buildContextOutput(rel string, allLines []string, re *regexp.Regexp, ctxBefore, ctxAfter int) (formatted []string, matchCount int) {
	type interval struct{ lo, hi int } // inclusive, 0-based

	// Collect match positions.
	var matchSet []int
	for i, line := range allLines {
		if re.MatchString(line) {
			matchSet = append(matchSet, i)
		}
	}
	if len(matchSet) == 0 {
		return nil, 0
	}
	matchCount = len(matchSet)

	// Build merged context windows.
	isMatch := make(map[int]bool, len(matchSet))
	for _, m := range matchSet {
		isMatch[m] = true
	}

	var windows []interval
	for _, m := range matchSet {
		lo := m - ctxBefore
		if lo < 0 {
			lo = 0
		}
		hi := m + ctxAfter
		if hi >= len(allLines) {
			hi = len(allLines) - 1
		}
		// Merge with the previous window if they overlap or are adjacent.
		if len(windows) > 0 && lo <= windows[len(windows)-1].hi+1 {
			if hi > windows[len(windows)-1].hi {
				windows[len(windows)-1].hi = hi
			}
		} else {
			windows = append(windows, interval{lo, hi})
		}
	}

	for wi, win := range windows {
		// Emit "--" group separator between non-adjacent context windows (rg style).
		if wi > 0 {
			formatted = append(formatted, "--")
		}
		for i := win.lo; i <= win.hi; i++ {
			lineNo := i + 1 // 1-based
			if isMatch[i] {
				formatted = append(formatted, fmt.Sprintf("%s:%d:%s", rel, lineNo, allLines[i]))
			} else {
				formatted = append(formatted, fmt.Sprintf("%s-%d-%s", rel, lineNo, allLines[i]))
			}
		}
	}
	return formatted, matchCount
}

// grepFileResult holds one file's contribution to the grep output: the
// formatted content-mode lines and the per-file match count.  It is shared by
// the line-oriented and multiline Go fallbacks so both render identically.
type grepFileResult struct {
	path  string
	lines []string // formatted output lines (content mode)
	count int
}

func runGoGrep(ctx context.Context, root, searchPath string, args grepArgs) (string, error) {
	if args.Multiline {
		return runGoGrepMultiline(ctx, root, searchPath, args)
	}

	re, err := compileSmartCase(args.Pattern, args.CaseInsensitive)
	if err != nil {
		return "", fmt.Errorf("compiling grep pattern: %w", err)
	}

	gitignoreDirs := loadRootGitignore(root)
	typeSet := grepTypeSet(args.Type)
	ctxBefore, ctxAfter := contextLines(args)
	onlyMatching := onlyMatchingContent(args)
	// -o supersedes context: matched substrings are emitted standalone, so the
	// context-window path is bypassed entirely when only_matching is set.
	needContext := !onlyMatching && (ctxBefore > 0 || ctxAfter > 0) &&
		(args.OutputMode == "content" || args.OutputMode == "")

	var (
		matches   []grepFileResult
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
		if !extInTypeSet(entry.Name(), typeSet) {
			return nil
		}

		rel := relativeSlash(root, path)
		var fm grepFileResult
		fm.path = rel

		if needContext {
			// Context mode: read the whole file into memory so we can look
			// forward and backward around each match.
			allLines := grepFileLines(path)
			if allLines == nil {
				return nil
			}
			formatted, cnt := buildContextOutput(rel, allLines, re, ctxBefore, ctxAfter)
			fm.count = cnt
			if fm.count == 0 {
				return nil
			}
			// Apply cap: only take as many formatted lines as the budget allows.
			// Separator lines ("--") do not count toward the cap.
			for _, fl := range formatted {
				if totalRows >= grepMatchCap {
					capped = true
					break
				}
				fm.lines = append(fm.lines, fl)
				if fl != "--" {
					totalRows++
				}
			}
		} else {
			// No context: stream line by line (memory-efficient path).
			f, ferr := os.Open(path)
			if ferr != nil {
				return nil //nolint:nilerr
			}
			defer f.Close()

			probe := make([]byte, 8192)
			n, _ := f.Read(probe)
			if isBinaryChunk(probe[:n]) {
				return nil
			}
			if _, err := f.Seek(0, 0); err != nil {
				return nil //nolint:nilerr
			}

			scanner := bufio.NewScanner(f)
			lineNo := 0
			fileCapped := false
			for scanner.Scan() {
				lineNo++
				line := scanner.Text()
				if onlyMatching {
					// Emit each non-empty matched substring on its own row,
					// mirroring rg -o.  Empty matches (possible with patterns
					// like "a*") are skipped, exactly as rg does.
					for _, m := range re.FindAllString(line, -1) {
						if m == "" {
							continue
						}
						fm.count++
						if totalRows < grepMatchCap {
							fm.lines = append(fm.lines, fmt.Sprintf("%s:%d:%s", rel, lineNo, m))
							totalRows++
							if totalRows >= grepMatchCap {
								fileCapped = true
							}
						}
					}
				} else if re.MatchString(line) {
					fm.count++
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
			if scanner.Err() != nil {
				return nil //nolint:nilerr
			}
			_ = fileCapped
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
			if capped || totalRows >= grepMatchCap {
				capped = true
				return capErr
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, capErr) {
		return "", fmt.Errorf("searching files: %w", walkErr)
	}

	return renderGrepResults(matches, args.OutputMode, capped), nil
}

// renderGrepResults sorts per-file results by path and renders them in the
// shape dictated by outputMode (content / files_with_matches / count),
// appending the cap notice when capped is true.  It is the shared tail of the
// line-oriented and multiline Go fallbacks.
func renderGrepResults(matches []grepFileResult, outputMode string, capped bool) string {
	sort.Slice(matches, func(i, j int) bool { return matches[i].path < matches[j].path })
	if len(matches) == 0 {
		return "No matches found."
	}

	var b strings.Builder
	for _, match := range matches {
		switch outputMode {
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
	return result
}

// runGoGrepMultiline is the multiline counterpart of runGoGrep.  Each file is
// read into a single buffer and searched with a dotall pattern, so a match may
// span several lines.  In content mode every line a match touches is emitted as
// "path:lineNo:text" (mirroring rg -U output); context options do not apply.
func runGoGrepMultiline(ctx context.Context, root, searchPath string, args grepArgs) (string, error) {
	re, err := compileMultilineSmartCase(args.Pattern, args.CaseInsensitive)
	if err != nil {
		return "", fmt.Errorf("compiling grep pattern: %w", err)
	}

	gitignoreDirs := loadRootGitignore(root)
	typeSet := grepTypeSet(args.Type)
	contentMode := args.OutputMode == "content" || args.OutputMode == ""

	var (
		matches   []grepFileResult
		totalRows int
		capped    bool
	)
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
		if !extInTypeSet(entry.Name(), typeSet) {
			return nil
		}

		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil //nolint:nilerr
		}
		if isBinaryChunk(probeChunk(data)) {
			return nil
		}
		content := string(data)
		locs := re.FindAllStringIndex(content, -1)
		if len(locs) == 0 {
			return nil
		}

		rel := relativeSlash(root, path)
		fm := grepFileResult{path: rel, count: len(locs)}

		if contentMode {
			allLines := strings.Split(content, "\n")
			lineHit := make(map[int]bool)
			for _, loc := range locs {
				start := offsetToLine(content, loc[0])
				end := loc[1]
				if end > loc[0] {
					end-- // last byte of the match
				}
				endLine := offsetToLine(content, end)
				for ln := start; ln <= endLine; ln++ {
					lineHit[ln] = true
				}
			}
			ordered := make([]int, 0, len(lineHit))
			for ln := range lineHit {
				ordered = append(ordered, ln)
			}
			sort.Ints(ordered)
			for _, ln := range ordered {
				if ln < 0 || ln >= len(allLines) {
					continue
				}
				if totalRows >= grepMatchCap {
					capped = true
					break
				}
				fm.lines = append(fm.lines, fmt.Sprintf("%s:%d:%s", rel, ln+1, allLines[ln]))
				totalRows++
			}
		}

		matches = append(matches, fm)

		switch args.OutputMode {
		case "files_with_matches", "count":
			if len(matches) >= grepMatchCap {
				capped = true
				return capErr
			}
		default:
			if capped || totalRows >= grepMatchCap {
				capped = true
				return capErr
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, capErr) {
		return "", fmt.Errorf("searching files: %w", walkErr)
	}

	return renderGrepResults(matches, args.OutputMode, capped), nil
}

// compileMultilineSmartCase compiles pattern for multiline matching: the dotall
// flag (?s) lets `.` cross newlines, and smart-case adds (?i) when the pattern
// is all-lowercase — mirroring compileSmartCase plus rg --multiline-dotall.
// When ignoreCase is true the (?i) flag is always added, overriding smart-case.
func compileMultilineSmartCase(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	flags := "(?s)"
	if ignoreCase || !hasUpper(pattern) {
		flags = "(?is)"
	}
	return regexp.Compile(flags + pattern)
}

// offsetToLine returns the 0-based line index containing byte offset off,
// counting the newlines that precede it.
func offsetToLine(content string, off int) int {
	if off < 0 {
		return 0
	}
	if off > len(content) {
		off = len(content)
	}
	return strings.Count(content[:off], "\n")
}

// probeChunk returns the leading bytes used for binary detection, capped at
// the same 8 KB window the streaming path probes.
func probeChunk(data []byte) []byte {
	if len(data) > 8192 {
		return data[:8192]
	}
	return data
}

func relativeSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
