package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile (shared with filetree_test.go) creates a file and parent dirs under
// a temp root, used here to lay down .gitignore fixtures and sample trees.

// TestGitignore_BaseDirsAlwaysIgnored proves the built-in skip set excludes
// version-control and dependency directories even without a .gitignore file.
func TestGitignore_BaseDirsAlwaysIgnored(t *testing.T) {
	t.Parallel()
	m := newGitignoreMatcher(t.TempDir())
	require.True(t, m.ignored(".git", true))
	require.True(t, m.ignored("node_modules", true))
	require.True(t, m.ignored("node_modules/pkg/index.js", false))
	require.False(t, m.ignored("main.go", false))
}

// TestGitignore_SimplePatterns proves a floating basename pattern and a directory
// pattern from .gitignore are honored, including nested matches.
func TestGitignore_SimplePatterns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "*.log\nbuild/\nsecret.txt\n")
	m := newGitignoreMatcher(root)

	require.True(t, m.ignored("app.log", false))
	require.True(t, m.ignored("sub/dir/app.log", false), "floating pattern matches at any depth")
	require.True(t, m.ignored("build", true))
	require.True(t, m.ignored("build/out.o", false), "everything under an ignored dir is ignored")
	require.True(t, m.ignored("secret.txt", false))
	require.False(t, m.ignored("app.go", false))
}

// TestGitignore_DirOnly proves a trailing-slash pattern matches directories only,
// so a like-named file is not ignored.
func TestGitignore_DirOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "dist/\n")
	m := newGitignoreMatcher(root)
	require.True(t, m.ignored("dist", true))
	require.False(t, m.ignored("dist", false), "a file named dist is not ignored by dist/")
}

// TestGitignore_Anchored proves a leading-slash pattern anchors to the root, so a
// same-named file deeper in the tree is not ignored.
func TestGitignore_Anchored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "/config.yaml\n")
	m := newGitignoreMatcher(root)
	require.True(t, m.ignored("config.yaml", false))
	require.False(t, m.ignored("sub/config.yaml", false), "anchored pattern matches root only")
}

// TestGitignore_Negation proves a later "!" rule re-includes a path an earlier
// rule excluded (last match wins).
func TestGitignore_Negation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "*.env\n!keep.env\n")
	m := newGitignoreMatcher(root)
	require.True(t, m.ignored("prod.env", false))
	require.False(t, m.ignored("keep.env", false), "negation re-includes the path")
}

// TestGitignore_CommentsAndBlanks proves comment and blank lines contribute no
// rule.
func TestGitignore_CommentsAndBlanks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "# a comment\n\n*.tmp\n")
	m := newGitignoreMatcher(root)
	require.True(t, m.ignored("x.tmp", false))
	require.False(t, m.ignored("a", false))
}

// TestGitignore_DoubleStar proves a "**" pattern spans path separators.
func TestGitignore_DoubleStar(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "**/generated/*.go\n")
	m := newGitignoreMatcher(root)
	require.True(t, m.ignored("a/b/generated/x.go", false))
	require.True(t, m.ignored("generated/x.go", false), "**/ also matches at the root")
	require.False(t, m.ignored("a/generated/x.ts", false), "extension must still match")
}

// TestListFilesGitignored proves the gitignore-aware walk returns only the files
// that survive the matcher, pruning ignored directories wholesale.
func TestListFilesGitignored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".gitignore", "*.log\ndist/\n")
	writeFile(t, root, "main.go", "package main")
	writeFile(t, root, "app.log", "noise")
	writeFile(t, root, "dist/bundle.js", "built")
	writeFile(t, root, "pkg/util.go", "package pkg")
	writeFile(t, root, "node_modules/dep/index.js", "dep")

	files := listFilesGitignored(root, nil)
	require.Contains(t, files, "main.go")
	require.Contains(t, files, "pkg/util.go")
	require.Contains(t, files, ".gitignore")
	require.NotContains(t, files, "app.log")
	require.NotContains(t, files, "dist/bundle.js")
	require.NotContains(t, files, "node_modules/dep/index.js")
}
