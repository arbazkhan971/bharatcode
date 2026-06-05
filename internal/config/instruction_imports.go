package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxInstructionImportDepth caps how many levels of @path imports are
// followed when expanding an instructions file. It both bounds the work
// done for deeply nested import chains and, together with cycle
// detection, guarantees termination. The value matches the 5-hop limit
// other agents (e.g. Claude Code) apply to CLAUDE.md/AGENTS.md imports.
const maxInstructionImportDepth = 5

// instructionImportPattern matches an "@path" import reference: an "@"
// at a word boundary (start of a segment or preceded by whitespace)
// followed by a path. The path may not contain whitespace or backticks,
// and its final character may not be common trailing punctuation, so a
// reference at the end of a sentence ("see @docs/setup.md.") imports
// docs/setup.md and leaves the period in place. The leading boundary is
// captured separately so it is preserved on replacement and email-style
// addresses (user@example.com) are not treated as imports.
var instructionImportPattern = regexp.MustCompile(
	"(^|\\s)@([^\\s`]*[^\\s`.,;:!?)\\]])",
)

// expandInstructionImports replaces @path references in content with the
// (recursively expanded) contents of the referenced files, mirroring the
// CLAUDE.md/AGENTS.md import feature of other agents. Paths resolve
// relative to baseDir (the directory of the file that contained the
// reference) and may be absolute or use a leading ~ for the home
// directory. References inside fenced code blocks and inline code spans
// are left untouched, as are references that fail to resolve, exceed the
// depth limit, or would form a cycle (tracked via visited, keyed by
// absolute path). A non-empty visited set seeded with the top-level
// file's own path prevents a file from importing itself.
func expandInstructionImports(content, baseDir string, depth int, visited map[string]bool) string {
	if depth >= maxInstructionImportDepth || !strings.Contains(content, "@") {
		return content
	}
	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		if isFenceMarker(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = expandLineImports(line, baseDir, depth, visited)
	}
	return strings.Join(lines, "\n")
}

// isFenceMarker reports whether a line opens or closes a fenced code
// block, i.e. its first non-space run is at least three backticks or
// tildes (the CommonMark fence markers).
func isFenceMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// expandLineImports expands @path references on a single line, skipping
// any that fall inside an inline code span. Splitting on the backtick
// places code-span text at odd indices, which are left untouched.
func expandLineImports(line, baseDir string, depth int, visited map[string]bool) string {
	if !strings.Contains(line, "@") {
		return line
	}
	segments := strings.Split(line, "`")
	for i := range segments {
		if i%2 == 1 {
			// Inside an inline code span: leave references verbatim.
			continue
		}
		segments[i] = instructionImportPattern.ReplaceAllStringFunc(segments[i], func(match string) string {
			sub := instructionImportPattern.FindStringSubmatch(match)
			boundary, ref := sub[1], sub[2]
			expanded, ok := importInstructionFile(ref, baseDir, depth, visited)
			if !ok {
				// Unresolvable, cyclic, or too deep: keep the literal text.
				return match
			}
			return boundary + expanded
		})
	}
	return strings.Join(segments, "`")
}

// importInstructionFile resolves ref against baseDir, reads the target
// file, and returns its recursively expanded contents. ok is false when
// the path cannot be resolved or read or has already been visited (a
// cycle), so callers preserve the literal reference. A resolvable but
// empty file returns ("", true) so the reference is dropped.
func importInstructionFile(ref, baseDir string, depth int, visited map[string]bool) (string, bool) {
	path := expandInstructionHome(ref)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if visited[abs] {
		return "", false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	visited[abs] = true
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", true
	}
	return expandInstructionImports(trimmed, filepath.Dir(abs), depth+1, visited), true
}

// expandInstructionHome expands a leading ~ (alone or as ~/) in path to
// the user's home directory. Any other path is returned unchanged.
func expandInstructionHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
