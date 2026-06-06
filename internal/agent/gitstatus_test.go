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

// commitFile writes name with content in dir and creates a commit with the
// given subject, configuring an identity so the commit succeeds in CI.
func commitFile(t *testing.T, dir, name, content, subject string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", name},
		{"commit", "-m", subject},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run(), "git %v", args)
	}
}

func TestGitRecentCommitsNotARepoReturnsEmpty(t *testing.T) {
	require.Equal(t, "", gitRecentCommits(context.Background(), t.TempDir()))
}

func TestGitRecentCommitsNoCommitsYet(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	// A freshly initialized repo has no commits, so the section is omitted.
	require.Equal(t, "", gitRecentCommits(context.Background(), dir))
}

func TestGitRecentCommitsListsTipFirst(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	commitFile(t, dir, "a.txt", "a", "first commit")
	commitFile(t, dir, "b.txt", "b", "second commit")

	got := gitRecentCommits(context.Background(), dir)
	lines := strings.Split(got, "\n")
	require.Len(t, lines, 2)
	// Newest commit is listed first; each line is indented and carries its
	// short hash plus subject.
	require.True(t, strings.HasPrefix(lines[0], "  "), got)
	require.Contains(t, lines[0], "second commit")
	require.Contains(t, lines[1], "first commit")
}

func TestGitRecentCommitsCapsHistory(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	total := gitRecentCommitsCap + 3
	for i := 0; i < total; i++ {
		commitFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x", fmt.Sprintf("commit %02d", i))
	}

	got := gitRecentCommits(context.Background(), dir)
	require.Equal(t, gitRecentCommitsCap, strings.Count(got, "\n")+1)
}

func TestGitRecentCommitsTruncatesLongSubject(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	long := strings.Repeat("x", gitRecentCommitLineCap*2)
	commitFile(t, dir, "a.txt", "a", long)

	got := gitRecentCommits(context.Background(), dir)
	// Each rendered line stays within the cap (plus the two-space indent),
	// and overflow collapses into an ellipsis.
	for _, line := range strings.Split(got, "\n") {
		require.LessOrEqual(t, len([]rune(strings.TrimPrefix(line, "  "))), gitRecentCommitLineCap)
	}
	require.Contains(t, got, "…")
}

func TestRenderPromptIncludesRecentCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	commitFile(t, dir, "a.txt", "a", "seed the repo")

	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(dir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{filepath.Join(dir, "no-such-dir")} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	out := renderInWorkdir(t, dir)
	require.Contains(t, out, "- Recent commits:")
	require.Contains(t, out, "seed the repo")
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
