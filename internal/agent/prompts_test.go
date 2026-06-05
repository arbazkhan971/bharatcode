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

	// Non-empty summaries are appended under the section header, preceded by
	// the load-the-skill usage instructions.
	got := injectSkills(base, "<available_skills>\n  <skill><name>pdf</name></skill>\n</available_skills>")
	require.True(t, strings.HasPrefix(got, base))
	require.Contains(t, got, availableSkillsHeader)
	require.Contains(t, got, "<available_skills>")
	// The two usage lines tell the model how to load and resolve a skill,
	// and they appear before the skills block itself.
	require.Contains(t, got, "Use the read tool to load a skill file when the task matches its description.")
	require.Contains(t, got, "resolve it against the skill directory and use that absolute path.")
	require.Less(t, strings.Index(got, "Use the read tool"), strings.Index(got, "<available_skills>"))
}

func TestRenderPromptSkillsRenderAsXMLWithLocation(t *testing.T) {
	workdir := t.TempDir()
	skillsRoot := filepath.Join(workdir, ".bharatcode", "skills")
	writeSkillFixture(t, skillsRoot, "pdf", "---\nname: pdf\ndescription: Fill and read PDF forms\n---\nbody\n")

	restore := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{skillsRoot} }
	t.Cleanup(func() { skillSearchDirs = restore })

	out := renderInWorkdir(t, workdir)

	// Skills render as an <available_skills> XML block.
	require.Contains(t, out, "<available_skills>")
	require.Contains(t, out, "</available_skills>")
	require.Contains(t, out, "<name>pdf</name>")
	require.Contains(t, out, "<description>Fill and read PDF forms</description>")
	// Each skill advertises the absolute <location> of its directory so the
	// model can load the manifest and resolve relative paths against it.
	require.Contains(t, out, "<location>"+filepath.Join(skillsRoot, "pdf")+"</location>")
	// The read-the-file usage instruction prefixes the block.
	require.Contains(t, out, "Use the read tool to load a skill file when the task matches its description.")
}

func TestRenderPromptProjectInstructionsArePathTagged(t *testing.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("RULE: prefer table-driven tests."), 0o644))

	// No skills, hermetic.
	restore := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restore })

	out := renderInWorkdir(t, workdir)

	// The rule renders inside a <project_context>/<project_instructions>
	// block attributed to the directory it was loaded from. os.Getwd may
	// resolve symlinks, so attribute against the realized working directory.
	wd := mustGetwdIn(t, workdir)
	require.Contains(t, out, "<project_context>")
	require.Contains(t, out, `<project_instructions path="`+wd+`">`)
	require.Contains(t, out, "RULE: prefer table-driven tests.")
	require.Contains(t, out, "</project_instructions>")
	require.Contains(t, out, "</project_context>")
}

// mustGetwdIn returns the realized working directory while chdir'd into
// workdir, restoring the original directory before it returns.
func mustGetwdIn(t *testing.T, workdir string) string {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	defer func() { _ = os.Chdir(orig) }()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
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

func TestRenderPromptTaskAgentIsReadOnlyExplorer(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "grep", desc: "Search file contents."})

	prompt, err := renderPrompt(context.Background(), "task", "", registry, nil)
	require.NoError(t, err)

	// The task agent renders a distinct exploration prompt, not the coder one.
	require.Contains(t, prompt, "exploration agent")
	require.NotContains(t, prompt, "primary coding agent")

	// Read-only posture is explicit: no system-state mutation.
	require.Contains(t, prompt, "read-only")
	require.Contains(t, prompt, "modify the user's system state in any way")

	// Search discipline references the three core exploration tools.
	require.Contains(t, prompt, "glob")
	require.Contains(t, prompt, "grep")
	require.Contains(t, prompt, "view")

	// Findings are reported as absolute paths.
	require.Contains(t, prompt, "ABSOLUTE path")

	// Template-injected data is still present for the task agent: the tool
	// list it renders and the trailing environment block.
	require.Contains(t, prompt, "grep: Search file contents.")
	require.Contains(t, prompt, "Working directory:")
}

// stripSkillsSection removes the available-skills section that
// injectSkills appends, leaving the rest of the prompt intact. The
// trailing environment block is rendered after the skills, so it is
// spliced back on to mirror a prompt that never had a skills section.
func stripSkillsSection(prompt string) string {
	start := strings.Index(prompt, "\n\n"+availableSkillsHeader)
	if start < 0 {
		return prompt
	}
	if env := strings.Index(prompt, "\n\n"+environmentHeader); env > start {
		return prompt[:start] + prompt[env:]
	}
	return prompt[:start]
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
