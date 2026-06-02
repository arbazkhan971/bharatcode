package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectSkills(t *testing.T) {
	base := "SYSTEM PROMPT BODY"

	// Empty summaries leave the base prompt untouched.
	require.Equal(t, base, injectSkills(base, ""))
	require.Equal(t, base, injectSkills(base, "   \n  "))

	// Non-empty summaries are appended under the section header.
	got := injectSkills(base, "pdf — Fill PDF forms\ngit — Manage branches")
	require.True(t, strings.HasPrefix(got, base))
	require.Contains(t, got, availableSkillsHeader)
	require.Contains(t, got, "pdf — Fill PDF forms")
	require.Contains(t, got, "git — Manage branches")
}

// writeSkillFixture creates skillsRoot/<name>/SKILL.md.
func writeSkillFixture(t *testing.T, skillsRoot, name, content string) {
	t.Helper()
	dir := filepath.Join(skillsRoot, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
}

func TestRenderPromptInjectsSkillsWhenPresent(t *testing.T) {
	workdir := t.TempDir()
	skillsRoot := filepath.Join(workdir, ".bharatcode", "skills")
	writeSkillFixture(t, skillsRoot, "pdf", "---\nname: pdf\ndescription: Fill and read PDF forms\n---\nbody\n")
	writeSkillFixture(t, skillsRoot, "release", "---\nname: release\ndescription: Cut a tagged release\n---\nbody\n")

	// Point the loader only at this temp project root so the test is
	// hermetic and never reads the developer's real skills directory.
	restore := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{skillsRoot} }
	t.Cleanup(func() { skillSearchDirs = restore })

	out := renderInWorkdir(t, workdir)
	require.Contains(t, out, availableSkillsHeader)
	require.Contains(t, out, "Fill and read PDF forms")
	require.Contains(t, out, "Cut a tagged release")
}

func TestRenderPromptNoSkillsLeavesPromptUnchanged(t *testing.T) {
	workdir := t.TempDir()
	empty := filepath.Join(workdir, ".bharatcode", "skills") // does not exist

	restore := skillSearchDirs
	t.Cleanup(func() { skillSearchDirs = restore })

	// With a skills dir present.
	writeSkillFixture(t, empty, "pdf", "---\nname: pdf\ndescription: Fill PDF forms\n---\nbody\n")
	skillSearchDirs = func(string) []string { return []string{empty} }
	withSkills := renderInWorkdir(t, workdir)

	// With no skills at all (point at a non-existent root).
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	withoutSkills := renderInWorkdir(t, workdir)

	require.NotContains(t, withoutSkills, availableSkillsHeader)
	require.Contains(t, withSkills, availableSkillsHeader)
	// Removing the section is the only difference between the two prompts.
	require.Equal(t, withoutSkills, stripSkillsSection(withSkills))
}

// stripSkillsSection removes the available-skills section that
// injectSkills appends, leaving the rest of the prompt intact.
func stripSkillsSection(prompt string) string {
	idx := strings.Index(prompt, "\n\n"+availableSkillsHeader)
	if idx < 0 {
		return prompt
	}
	return prompt[:idx]
}

// renderInWorkdir runs renderPrompt with workdir as the process working
// directory and a non-nil tool registry, returning the rendered prompt.
func renderInWorkdir(t *testing.T, workdir string) string {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out, err := renderPrompt(context.Background(), "coder", "", newFakeRegistry(), nil)
	require.NoError(t, err)
	return out
}
