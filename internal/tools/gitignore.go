package tools

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// gitignoreStack honors nested .gitignore files during a directory walk, the
// way git and ripgrep do: a build/.gitignore can hide build/ artifacts even
// when the workspace-root .gitignore says nothing about them. Without this,
// glob and ls — which previously consulted only the root .gitignore — flood the
// model with paths that grep (backed by rg, which already reads nested
// .gitignore) correctly hides, an inconsistency between the file-discovery
// tools.
//
// A stack is scoped to a single tool invocation: patterns are read lazily and
// cached per directory, so each .gitignore is read at most once per call.
type gitignoreStack struct {
	root  string
	cache map[string][]string
}

func newGitignoreStack(root string) *gitignoreStack {
	return &gitignoreStack{root: root, cache: map[string][]string{}}
}

// patternsFor returns the include patterns declared in dir's own .gitignore,
// reading and caching the file on first use. A missing or unreadable file
// caches an empty slice so it is consulted at most once.
func (g *gitignoreStack) patternsFor(dir string) []string {
	if p, ok := g.cache[dir]; ok {
		return p
	}
	p := readGitignoreFile(filepath.Join(dir, ".gitignore"))
	g.cache[dir] = p
	return p
}

// ignored reports whether the entry at the absolute path (basename name,
// directory kind isDir) is excluded by any .gitignore from the workspace root
// down to the entry's parent directory. Each .gitignore's patterns are matched
// against the path relative to that .gitignore's own directory, matching git's
// per-directory semantics: a pattern in sub/.gitignore is anchored at sub/.
func (g *gitignoreStack) ignored(path, name string, isDir bool) bool {
	for _, dir := range ancestorDirs(g.root, filepath.Dir(path)) {
		pats := g.patternsFor(dir)
		if len(pats) == 0 {
			continue
		}
		if ignored(relativeSlash(dir, path), name, isDir, pats) {
			return true
		}
	}
	return false
}

// ancestorDirs returns root and every directory between root and dir
// (inclusive), ordered from root downward. It is used to gather the .gitignore
// files that govern an entry: those in the entry's parent and all of its
// ancestors up to the workspace root. If dir is not within root the slice holds
// just dir, so a stray path is still checked against its own directory.
func ancestorDirs(root, dir string) []string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{dir}
	}
	dirs := []string{root}
	if rel == "." {
		return dirs
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dirs = append(dirs, cur)
	}
	return dirs
}

// readGitignoreFile parses the .gitignore at path and returns its positive
// include patterns. Blank lines, comments (#) and negations (!) are skipped —
// the matcher honors only positive excludes. A missing or unreadable file
// yields no patterns rather than an error, matching git's tolerance of absent
// ignore files.
func readGitignoreFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}
