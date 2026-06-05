package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// touch sets a file's modification time so tests can assert recency ordering
// deterministically rather than relying on write-order timing.
func touch(t *testing.T, root, rel string, mod time.Time) {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.Chtimes(full, mod, mod))
}

func TestGlobOrdersNewestFirst(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "old.go", "package a\n")
	writeFile(t, workDir, "middle.go", "package a\n")
	writeFile(t, workDir, "new.go", "package a\n")

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Assign explicit, out-of-alphabetical-order timestamps so a lexicographic
	// sort would produce a different result than a recency sort.
	touch(t, workDir, "old.go", base)
	touch(t, workDir, "middle.go", base.Add(1*time.Hour))
	touch(t, workDir, "new.go", base.Add(2*time.Hour))

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "*.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.Equal(t, []string{"new.go", "middle.go", "old.go"}, lines)
}

func TestGlobBreaksTimeTiesLexicographically(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "charlie.txt", "c\n")
	writeFile(t, workDir, "alpha.txt", "a\n")
	writeFile(t, workDir, "bravo.txt", "b\n")

	// Identical mtimes force the deterministic path tiebreaker.
	same := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	for _, f := range []string{"charlie.txt", "alpha.txt", "bravo.txt"} {
		touch(t, workDir, f, same)
	}

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "*.txt"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.Equal(t, []string{"alpha.txt", "bravo.txt", "charlie.txt"}, lines)
}

func TestGlobBraceAlternation(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "app.ts", "x\n")
	writeFile(t, workDir, "app.tsx", "x\n")
	writeFile(t, workDir, "app.js", "x\n")
	writeFile(t, workDir, "readme.md", "x\n")

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "*.{ts,tsx}"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.ElementsMatch(t, []string{"app.ts", "app.tsx"}, lines)
}

func TestGlobBraceAlternationRecursive(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "src/a.go", "package a\n")
	writeFile(t, workDir, "src/b.py", "x\n")
	writeFile(t, workDir, "src/c.rs", "x\n")
	writeFile(t, workDir, "src/d.txt", "x\n")

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "**/*.{go,py,rs}"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.ElementsMatch(t, []string{"src/a.go", "src/b.py", "src/c.rs"}, lines)
}

func TestGlobUnbalancedBraceTreatedLiterally(t *testing.T) {
	workDir := t.TempDir()
	// A literal "{" in the name: an unbalanced brace must match it verbatim,
	// not be interpreted as an alternation opener.
	writeFile(t, workDir, "weird{name.txt", "x\n")
	writeFile(t, workDir, "normal.txt", "x\n")

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "weird{name.txt"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.Equal(t, []string{"weird{name.txt"}, lines)
}

func TestGlobRecencyAcrossDirectories(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "pkg/older.go", "package pkg\n")
	writeFile(t, workDir, "cmd/newer.go", "package main\n")

	base := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	touch(t, workDir, "pkg/older.go", base)
	touch(t, workDir, "cmd/newer.go", base.Add(30*time.Minute))

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "**/*.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	lines := strings.Split(result.Content, "\n")
	require.Equal(t, []string{"cmd/newer.go", "pkg/older.go"}, lines)
}
