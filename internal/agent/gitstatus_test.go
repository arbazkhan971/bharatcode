package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initGitRepo runs "git init" in dir so the working-tree status helper has a
// real repository to inspect, skipping the test when git is not installed.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}

func TestGitStatusNotARepoReturnsEmpty(t *testing.T) {
	// A bare temp dir is not a git repository: the helper returns "" so the
	// caller omits the status line entirely.
	require.Equal(t, "", gitStatus(context.Background(), t.TempDir()))
}

func TestGitStatusCleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	// A freshly initialized repo with no files has nothing to report.
	require.Equal(t, "clean", gitStatus(context.Background(), dir))
}

func TestGitStatusReportsUncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi"), 0o644))

	got := gitStatus(context.Background(), dir)
	require.Contains(t, got, "1 uncommitted change:")
	// Untracked files keep their porcelain "??" marker.
	require.Contains(t, got, "?? new.txt")
	require.NotContains(t, got, "and")
}

func TestGitStatusPluralizesAndPreservesEntries(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))

	got := gitStatus(context.Background(), dir)
	require.Contains(t, got, "2 uncommitted changes:")
	require.Contains(t, got, "?? a.txt")
	require.Contains(t, got, "?? b.txt")
}

func TestGitStatusTruncatesLargeTrees(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	total := gitStatusFileCap + 3
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	got := gitStatus(context.Background(), dir)
	require.Contains(t, got, fmt.Sprintf("%d uncommitted changes:", total))
	require.Contains(t, got, fmt.Sprintf("and %d more", total-gitStatusFileCap))
	// Only the capped number of entries is listed inline.
	require.Equal(t, gitStatusFileCap, strings.Count(got, "?? f"))
}

func TestRenderPromptIncludesGitStatusLine(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x"), 0o644))

	// Hermetic skills/recipes so only the environment block matters here.
	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(dir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{filepath.Join(dir, "no-such-dir")} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	out := renderInWorkdir(t, dir)
	require.Contains(t, out, "- Git status: 1 uncommitted change: ?? dirty.txt")
}
