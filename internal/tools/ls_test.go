package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile is a small helper that creates a file with the given content
// under the workspace, creating parent directories as needed.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func TestLSListsEntriesSortedWithTrailingSlashOnDirs(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "beta.txt", "b\n")
	writeFile(t, workDir, "alpha.txt", "a\n")
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "src"), 0o755))

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "."}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Sorted, with directories suffixed by a slash.
	require.Equal(t, "alpha.txt\nbeta.txt\nsrc/", result.Content)
}

func TestLSRespectsGitignoreGlob(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "keep.txt", "keep\n")
	writeFile(t, workDir, "secret.env", "token\n")
	writeFile(t, workDir, ".gitignore", "*.env\n# comment\n\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "."}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "keep.txt")
	require.NotContains(t, result.Content, "secret.env")
}

func TestLSRespectsExtraIgnorePatterns(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "main.go", "package main\n")
	writeFile(t, workDir, "main_test.go", "package main\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":   ".",
		"ignore": []string{"*_test.go"},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "main.go")
	require.NotContains(t, result.Content, "main_test.go")
}

func TestLSEmptyDirectory(t *testing.T) {
	workDir := t.TempDir()
	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "."}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "Directory is empty.", result.Content)
}

func TestLSRecursiveTreeWithDepth(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "root.txt", "r\n")
	writeFile(t, workDir, "src/a.go", "package src\n")
	writeFile(t, workDir, "src/nested/deep.go", "package nested\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  ".",
		"depth": 3,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Sorted at each level; children indented two spaces per level.
	require.Equal(t, "root.txt\nsrc/\n  a.go\n  nested/\n    deep.go", result.Content)
}

func TestLSDepthOneStaysFlat(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "top.txt", "t\n")
	writeFile(t, workDir, "src/hidden.go", "package src\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  ".",
		"depth": 1,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Depth 1 lists only immediate children with no indentation.
	require.Equal(t, "src/\ntop.txt", result.Content)
}

func TestLSRecursiveRespectsGitignore(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "keep.go", "package main\n")
	writeFile(t, workDir, "build/artifact.o", "junk\n")
	writeFile(t, workDir, ".gitignore", "build/\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  ".",
		"depth": 5,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "keep.go")
	require.NotContains(t, result.Content, "build/")
	require.NotContains(t, result.Content, "artifact.o")
}

func TestLSDepthCappedAtMax(t *testing.T) {
	workDir := t.TempDir()
	// Build a tree deeper than maxLSDepth and confirm the descent stops.
	rel := ""
	for i := 0; i < maxLSDepth+3; i++ {
		rel = filepath.Join(rel, "d")
	}
	writeFile(t, workDir, filepath.Join(rel, "leaf.txt"), "x\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  ".",
		"depth": 99, // clamped to maxLSDepth
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// At most maxLSDepth "d/" levels appear; the leaf beyond that is never reached.
	require.NotContains(t, result.Content, "leaf.txt")
	require.Equal(t, maxLSDepth, strings.Count(result.Content, "d/"))
}

func TestLSMissingPathIsError(t *testing.T) {
	workDir := t.TempDir()
	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "nope"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "path does not exist")
}

func TestLSPathIsNotADirectory(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "file.txt", "x\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "file.txt"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "not a directory")
}
