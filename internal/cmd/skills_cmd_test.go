package cmd

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fileScanSkillsLoader is a deterministic, offline stand-in for the
// internal/skills loader. It walks each directory, reads SKILL.md
// frontmatter (name:/description: lines), and returns the discovered
// skills in directory order. It models the contract the real loader
// must satisfy so the command's directory wiring and rendering are
// exercised end-to-end against real files on disk, not a hardcoded
// list. When internal/skills lands, the production skillsLoader is
// wired to it; this helper keeps the command tests independent of that
// package's availability.
func fileScanSkillsLoader(_ context.Context, dirs ...string) ([]loadedSkill, error) {
	var out []loadedSkill
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a missing skill directory is not an error.
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			s, ok, err := readSkillMD(filepath.Join(dir, e.Name(), "SKILL.md"))
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// readSkillMD parses the name: and description: keys from a SKILL.md
// frontmatter block. It returns ok=false when the file is absent.
func readSkillMD(path string) (loadedSkill, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return loadedSkill{}, false, nil
		}
		return loadedSkill{}, false, err
	}
	defer func() { _ = f.Close() }()

	var s loadedSkill
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "name:"):
			s.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			s.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return loadedSkill{}, false, err
	}
	return s, s.Name != "", nil
}

func writeSkillMD(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
}

// executeSkills runs args against a root command that has the skills
// subcommand registered with the supplied loader injected, mirroring
// the production root wiring so flag parsing and rootOptions
// propagation are exercised rather than calling runSkills directly.
func executeSkills(t *testing.T, load func(context.Context, ...string) ([]loadedSkill, error), args ...string) (string, string, error) {
	t.Helper()
	prev := skillsLoader
	skillsLoader = load
	t.Cleanup(func() { skillsLoader = prev })

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := executeCommand(context.Background(), root)
	return stdout.String(), stderr.String(), err
}

func TestSkillsListsDiscoveredSkill(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".bharatcode", "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))
	writeSkillMD(t, skillsDir, "code-review", "---\nname: code-review\ndescription: Review a pull request for defects\n---\n\n# Code Review\n")

	stdout, stderr, err := executeSkills(t, fileScanSkillsLoader, "--project-dir", project, "skills")
	require.NoError(t, err)
	require.Empty(t, stderr)

	require.Contains(t, stdout, "code-review")
	require.Contains(t, stdout, "Review a pull request for defects")
	// The table header is rendered when at least one skill is present.
	require.Contains(t, stdout, "NAME")
	require.Contains(t, stdout, "DESCRIPTION")
	require.NotContains(t, stdout, "No skills found")
}

func TestSkillsReportsNoneWhenEmpty(t *testing.T) {
	project := t.TempDir()
	// An empty skills directory exists but holds no SKILL.md files.
	empty := filepath.Join(project, ".bharatcode", "skills")
	require.NoError(t, os.MkdirAll(empty, 0o755))

	stdout, _, err := executeSkills(t, fileScanSkillsLoader, "--project-dir", project, "skills")
	require.NoError(t, err)

	require.Contains(t, stdout, "No skills found")
	// The searched project skills directory is reported so the user
	// knows where to add skills.
	require.Contains(t, stdout, empty)
}

func TestRunSkillsRendersTwoColumnsSortedByLoader(t *testing.T) {
	// Drive runSkills directly to assert the rendered table shape is
	// independent of command wiring: header row plus one row per skill.
	load := func(_ context.Context, _ ...string) ([]loadedSkill, error) {
		return []loadedSkill{
			{Name: "alpha", Description: "first skill"},
			{Name: "beta", Description: "second skill"},
		}, nil
	}
	var buf bytes.Buffer
	require.NoError(t, runSkills(context.Background(), &buf, []string{"/tmp/skills"}, load))

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3) // header + two skills
	require.Contains(t, lines[0], "NAME")
	require.Contains(t, lines[0], "DESCRIPTION")
	require.Contains(t, lines[1], "alpha")
	require.Contains(t, lines[1], "first skill")
	require.Contains(t, lines[2], "beta")
}

func TestRunSkillsLoadErrorIsWrapped(t *testing.T) {
	load := func(_ context.Context, _ ...string) ([]loadedSkill, error) {
		return nil, context.DeadlineExceeded
	}
	var buf bytes.Buffer
	err := runSkills(context.Background(), &buf, []string{"/tmp/skills"}, load)
	require.Error(t, err)
	require.Contains(t, err.Error(), "loading skills")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSkillDirsIncludesGlobalAndProject(t *testing.T) {
	dirs := skillDirs(&rootOptions{projectDir: "/home/dev/proj"})
	// The project skills directory is always derived from project-dir.
	require.Contains(t, dirs, filepath.Join("/home/dev/proj", ".bharatcode", "skills"))
	// At least one directory (the global config skills dir) is present
	// even without a project, proving the global path is searched.
	globalOnly := skillDirs(&rootOptions{})
	require.NotEmpty(t, globalOnly)
	for _, d := range globalOnly {
		require.True(t, strings.HasSuffix(d, filepath.Join("bharatcode", "skills")) ||
			strings.HasSuffix(d, "skills"), "unexpected skills dir %q", d)
	}
}
