package tools

import (
	"context"
	"os"
	"path/filepath"
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
