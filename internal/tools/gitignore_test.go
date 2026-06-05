package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGlobHonorsNestedGitignore proves glob reads .gitignore files in
// subdirectories, not just the workspace root: a build/.gitignore hides
// artifacts the root .gitignore never mentions, matching git and ripgrep.
func TestGlobHonorsNestedGitignore(t *testing.T) {
	workDir := t.TempDir()
	// Root .gitignore is empty of *.tmp rules.
	writeFile(t, workDir, ".gitignore", "# nothing about tmp here\n")
	writeFile(t, workDir, "build/.gitignore", "*.tmp\nout/\n")

	writeFile(t, workDir, "build/keep.tmp", "x\n")    // hidden by build/.gitignore
	writeFile(t, workDir, "build/out/gen.tmp", "x\n") // dir pruned by build/.gitignore
	writeFile(t, workDir, "build/main.go", "package m\n")
	writeFile(t, workDir, "src/keep.tmp", "x\n") // sibling: NOT covered by build/.gitignore

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "**/*.tmp"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.NotContains(t, result.Content, "build/keep.tmp")
	require.NotContains(t, result.Content, "build/out/gen.tmp")
	// Anchoring: build/.gitignore must not leak to a sibling directory.
	require.Contains(t, result.Content, "src/keep.tmp")
}

// TestGlobNestedGitignorePrunesDirectory confirms a directory excluded by a
// nested .gitignore is pruned entirely, so glob never descends into it.
func TestGlobNestedGitignorePrunesDirectory(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "pkg/.gitignore", "dist/\n")
	writeFile(t, workDir, "pkg/dist/bundle.go", "package dist\n")
	writeFile(t, workDir, "pkg/lib.go", "package pkg\n")

	tool := newGlobTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"pattern": "**/*.go"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Contains(t, result.Content, "pkg/lib.go")
	require.NotContains(t, result.Content, "pkg/dist/bundle.go")
}

// TestLSHonorsNestedGitignore confirms listing a subdirectory respects that
// subdirectory's own .gitignore, which the previous root-only lookup ignored.
func TestLSHonorsNestedGitignore(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, "build/keep.txt", "k\n")
	writeFile(t, workDir, "build/secret.log", "s\n")
	writeFile(t, workDir, "build/.gitignore", "*.log\n")

	tool := newLSTool(Dependencies{WorkDir: workDir})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": "build"}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Contains(t, result.Content, "keep.txt")
	require.NotContains(t, result.Content, "secret.log")
}

func TestAncestorDirs(t *testing.T) {
	root := "/work"
	require.Equal(t, []string{"/work"}, ancestorDirs(root, "/work"))
	require.Equal(t,
		[]string{"/work", "/work/a", "/work/a/b"},
		ancestorDirs(root, "/work/a/b"),
	)
	// A path outside the root degrades to checking just that directory.
	out := ancestorDirs(root, "/elsewhere")
	require.Equal(t, []string{"/elsewhere"}, out)
}

// TestReadGitignoreFileSkipsNoise verifies blank lines, comments and negations
// are dropped, leaving only positive exclude patterns.
func TestReadGitignoreFileSkipsNoise(t *testing.T) {
	workDir := t.TempDir()
	writeFile(t, workDir, ".gitignore", "# comment\n\n*.log\n!keep.log\n  dist/  \n")
	pats := readGitignoreFile(filepath.Join(workDir, ".gitignore"))
	require.Equal(t, []string{"*.log", "dist/"}, pats)
	require.False(t, strings.Contains(strings.Join(pats, ","), "keep.log"))
}
