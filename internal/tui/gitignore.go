package tui

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// gitignoreMatcher decides whether a workspace-relative path is ignored, so the
// @-file autocomplete provider can offer the files a developer actually works
// with rather than build output, vendored dependencies, or secrets. It reads the
// repository's root .gitignore (the common case) plus a small built-in base set
// that is always skipped, and applies the patterns with last-match-wins
// semantics — a later negation (!pattern) can re-include a path an earlier rule
// excluded, the way git itself resolves overlapping rules.
//
// It is a pragmatic subset of git's ignore engine: root-level .gitignore only
// (not nested per-directory files), supporting comments, negation, anchored and
// floating patterns, directory-only rules, and the *, ?, ** and [class] globs.
// That covers the rules a typical repository's root .gitignore uses; the goal is
// a clean file picker, not byte-exact git parity.
type gitignoreMatcher struct {
	patterns []gitignorePattern
}

// gitignorePattern is one compiled ignore rule.
type gitignorePattern struct {
	re       *regexp.Regexp
	negate   bool // a leading "!" re-includes a previously excluded match
	dirOnly  bool // a trailing "/" matches directories only
	original string
}

// gitignoreBaseDirs are directory names always skipped regardless of any
// .gitignore, so version-control metadata and dependency trees never clutter the
// picker even in a repository with no ignore file.
var gitignoreBaseDirs = []string{".git", "node_modules", "vendor"}

// newGitignoreMatcher builds a matcher for the repository rooted at root. It
// always installs the base directory rules, then layers the root .gitignore on
// top when present. A missing or unreadable .gitignore yields a matcher with only
// the base rules, so the picker degrades to the built-in skip set rather than
// failing.
func newGitignoreMatcher(root string) *gitignoreMatcher {
	m := &gitignoreMatcher{}
	for _, d := range gitignoreBaseDirs {
		if p, ok := compileGitignorePattern(d + "/"); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	if root == "" {
		return m
	}
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p, ok := compileGitignorePattern(sc.Text()); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return m
}

// ignored reports whether the workspace-relative slash path rel (a file when
// isDir is false, a directory when true) is ignored. Rules are applied in order
// and the last one to match wins, so a negation rule listed after an exclusion
// re-includes the path. A path with no matching rule is not ignored.
func (m *gitignoreMatcher) ignored(rel string, isDir bool) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" || rel == "." {
		return false
	}
	// An ancestor directory excluded by a rule ignores everything beneath it, and
	// git does not let a deeper negation re-include a file whose parent directory
	// is excluded. So test each ancestor directory first: the moment one is
	// ignored, rel is ignored regardless of what its own rules say.
	segs := strings.Split(rel, "/")
	for i := 1; i < len(segs); i++ {
		anc := strings.Join(segs[:i], "/")
		if m.matchDecision(anc, true) {
			return true
		}
	}
	return m.matchDecision(rel, isDir)
}

// matchDecision applies every pattern to path in order and returns the
// last-match-wins decision: true when the final matching rule is an exclusion,
// false when it is a negation or nothing matched. dir-only patterns are consulted
// only when path is a directory.
func (m *gitignoreMatcher) matchDecision(path string, isDir bool) bool {
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.re.MatchString(path) {
			ignored = !p.negate
		}
	}
	return ignored
}

// compileGitignorePattern translates one .gitignore line into a compiled rule,
// reporting ok=false for blank lines and comments (which contribute no rule). The
// translation handles the leading "!" negation, the trailing "/" directory-only
// marker, leading-slash anchoring, and the glob metacharacters; an unanchored
// pattern without a slash matches at any depth (git's basename behavior).
func compileGitignorePattern(line string) (gitignorePattern, bool) {
	raw := line
	// A leading whitespace run is significant to git only when escaped; the common
	// case is incidental indentation, so trim it. Trailing spaces are not trimmed
	// by git unless escaped, but trimming them here avoids surprising empty-suffix
	// globs and matches how editors usually save these files.
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignorePattern{}, false
	}

	var negate, dirOnly, anchored bool
	if strings.HasPrefix(line, "!") {
		negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if strings.HasPrefix(line, "/") {
		anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	// A slash anywhere but the trailing position anchors the pattern to the root,
	// matching git: "a/b" matches root-relative, "b" matches any "b".
	if strings.Contains(line, "/") {
		anchored = true
	}
	if line == "" {
		return gitignorePattern{}, false
	}

	expr := "^" + gitignoreGlobToRegexp(line)
	if !anchored {
		// A floating pattern matches a full path component anywhere: at the root or
		// after any "/".
		expr = "(^|/)" + gitignoreGlobToRegexp(line)
	}
	// A matched directory also ignores everything beneath it, so allow a trailing
	// path segment after the match.
	expr += "(/.*)?$"

	re, err := regexp.Compile(expr)
	if err != nil {
		return gitignorePattern{}, false
	}
	return gitignorePattern{re: re, negate: negate, dirOnly: dirOnly, original: raw}, true
}

// gitignoreGlobToRegexp converts a gitignore glob (already stripped of the
// leading "!", anchoring "/", and trailing "/") into a regular-expression
// fragment. It supports "**" (matches across path separators, including none),
// "*" (matches within a single path component), "?" (one non-separator rune),
// and "[...]" character classes; every other rune is matched literally.
func gitignoreGlobToRegexp(glob string) string {
	var b strings.Builder
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				// "**" spans separators; consume the pair and any immediately
				// following "/" so "**/x" still matches "x" at the root.
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					b.WriteString("(.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			// Copy a character class verbatim through its closing ']', which is valid
			// in both glob and regexp syntax; an unterminated class falls back to a
			// literal '['.
			j := i + 1
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j < len(runes) {
				b.WriteString(string(runes[i : j+1]))
				i = j
			} else {
				b.WriteString(regexp.QuoteMeta("["))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

// listFilesGitignored walks root and returns workspace-relative slash paths that
// survive the gitignore matcher, sorted lexically. It is the gitignore-aware
// counterpart to listWorkspaceFiles used by the @-file autocomplete provider:
// ignored directories are pruned wholesale (so their subtrees are never walked),
// and ignored files are dropped. An empty or unreadable root yields nil.
func listFilesGitignored(root string, m *gitignoreMatcher) []string {
	if root == "" {
		return nil
	}
	if m == nil {
		m = newGitignoreMatcher(root)
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if m.ignored(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if m.ignored(rel, false) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}
