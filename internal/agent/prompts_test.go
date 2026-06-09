package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
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

func TestInjectRecipes(t *testing.T) {
	base := "SYSTEM PROMPT BODY"

	// Empty summaries leave the base prompt untouched.
	require.Equal(t, base, injectRecipes(base, ""))
	require.Equal(t, base, injectRecipes(base, "   \n  "))

	// Non-empty summaries are appended under the section header, preceded by
	// the usage instructions explaining what a recipe is.
	got := injectRecipes(base, "<available_recipes>\n  <recipe><name>ship</name></recipe>\n</available_recipes>")
	require.True(t, strings.HasPrefix(got, base))
	require.Contains(t, got, availableRecipesHeader)
	require.Contains(t, got, "<available_recipes>")
	require.Contains(t, got, "Recipes are saved, parameterized prompts the user can run with /<name>.")
	// The usage line appears before the recipes block itself.
	require.Less(t, strings.Index(got, "Recipes are saved"), strings.Index(got, "<available_recipes>"))
}

func TestRenderPromptRecipesRenderAsXML(t *testing.T) {
	workdir := t.TempDir()
	recipesRoot := filepath.Join(workdir, ".bharatcode", "recipes")
	writeRecipeFixture(t, recipesRoot, "ship", `{"title":"Ship It","description":"Cut a release","prompt":"do the release"}`)

	// Hermetic skills (none) and recipes (only our fixture dir).
	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{recipesRoot} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	out := renderInWorkdir(t, workdir)

	// Recipes render as an <available_recipes> XML block keyed by file stem.
	require.Contains(t, out, availableRecipesHeader)
	require.Contains(t, out, "<available_recipes>")
	require.Contains(t, out, "</available_recipes>")
	require.Contains(t, out, "<name>ship</name>")
	require.Contains(t, out, "<title>Ship It</title>")
	require.Contains(t, out, "<description>Cut a release</description>")
	// The usage instruction prefixes the block.
	require.Contains(t, out, "Recipes are saved, parameterized prompts the user can run with /<name>.")
}

func TestRenderPromptNoRecipesNoSection(t *testing.T) {
	workdir := t.TempDir()

	restoreSkills := skillSearchDirs
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { skillSearchDirs = restoreSkills })
	restoreRecipes := recipeSearchDirs
	recipeSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	t.Cleanup(func() { recipeSearchDirs = restoreRecipes })

	out := renderInWorkdir(t, workdir)

	// With no recipes discovered, the section must be absent entirely.
	require.NotContains(t, out, availableRecipesHeader)
	require.NotContains(t, out, "<available_recipes>")
}

// writeRecipeFixture writes a single *.recipe.json file named name under
// recipesRoot, creating the directory tree as needed.
func writeRecipeFixture(t *testing.T, recipesRoot, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(recipesRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipesRoot, name+".recipe.json"), []byte(content), 0o644))
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

func TestRenderPromptIncludesIdentityGuidance(t *testing.T) {
	workdir := t.TempDir()
	out := renderInWorkdir(t, workdir)

	require.Contains(t, out, "## Identity and product questions")
	require.Contains(t, out, "you are BharatCode, a terminal-based AI coding agent")
	require.Contains(t, out, "Do not claim to be OpenAI, ChatGPT, Codex CLI, Claude Code, OpenCode, or the underlying model")
	require.Contains(t, out, "Do not call tools for a simple identity/about question")
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

	// Freeze the clock so both renders embed the same "Current date" in the
	// environment block. The environment block is rendered second-granular
	// from the wall clock, so without this the two renders can straddle a
	// second boundary and differ on the timestamp alone — making the
	// equality assertion flaky. With the clock pinned, the skills section is
	// the only real difference between the two prompts, which is exactly
	// what this test means to assert.
	restoreNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = restoreNow })

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

func TestIsSmallTaskPrompt(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"write a CLI", "write a CLI that prints the current time", true},
		{"create a script", "create a Python script to rename files in a folder", true},
		{"generate", "generate a Dockerfile for a Go service", true},
		{"make", "make a hello-world web server in Go", true},
		{"leading whitespace and case", "  Create a Makefile with a build target", true},
		{"empty", "", false},
		{"no leading verb", "the parser is broken, please look into it", false},
		{"verb not first", "I would like you to write a function", false},
		{"refactor cue disqualifies", "write a refactor for the existing parser", false},
		{"codebase cue disqualifies", "create a summary of this codebase", false},
		{"fix cue disqualifies", "fix the broken build", false},
		{"too long", "write a service that " + strings.Repeat("does many things and ", 40), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isSmallTaskPrompt(tc.text))
		})
	}
}

func TestDirectoryIsEmptyish(t *testing.T) {
	// A truly empty directory qualifies.
	require.True(t, directoryIsEmptyish(t.TempDir()))

	// A missing/unreadable directory is treated as empty: no existing code.
	require.True(t, directoryIsEmptyish(filepath.Join(t.TempDir(), "no-such-dir")))

	// A directory holding only incidental entries (VCS metadata, an editor
	// config, a README) still counts as empty — none is project source.
	onlyIgnored := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(onlyIgnored, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(onlyIgnored, "README.md"), []byte("# x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(onlyIgnored, ".gitignore"), []byte("bin/"), 0o644))
	require.True(t, directoryIsEmptyish(onlyIgnored))

	// A directory with real source is not empty.
	withSource := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withSource, "main.go"), []byte("package main"), 0o644))
	require.False(t, directoryIsEmptyish(withSource))
}

func TestBuildSmallTaskPrompt(t *testing.T) {
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", desc: "Write a file to disk."})
	registry.Register(&recordingTool{name: "bash", desc: "Run a shell command.\nUse for builds and tests."})
	registry.Register(&recordingTool{name: "grep", desc: "Search files.\nFull regex manual the small task does not need."})

	prompt := buildSmallTaskPrompt(registry.List())

	// A core tool with a one-line description renders inline after the bullet.
	require.Contains(t, prompt, "- write: Write a file to disk.")

	// A core tool (bash) carries its full manual — the tools a from-scratch
	// generation needs keep their usage docs, so the second line survives.
	require.Contains(t, prompt, "- bash:")
	require.Contains(t, prompt, "Use for builds and tests.")

	// A non-core tool (grep) is advertised by its one-line summary only; the
	// rest of its manual is trimmed so the small task does not carry it.
	require.Contains(t, prompt, "- grep: Search files.")
	require.NotContains(t, prompt, "Full regex manual the small task does not need.")

	// It keeps a tight engineering policy and a verification status line.
	require.Contains(t, prompt, "## Identity and product questions")
	require.Contains(t, prompt, "you are BharatCode, a terminal-based AI coding agent")
	require.Contains(t, prompt, "Do not claim to be OpenAI, ChatGPT, Codex CLI, Claude Code, OpenCode, or the underlying model")
	require.Contains(t, prompt, "Be concise.")
	require.Contains(t, prompt, "Verified")
	require.Contains(t, prompt, "Skipped (no_test_command)")

	// It deliberately drops the full coder doctrine's heavy sections.
	require.NotContains(t, prompt, "### Verification policy")
	require.NotContains(t, prompt, "Operational contract")
}

// TestRunUsesSmallTaskPromptForEmptyDirGeneration asserts that a short
// from-scratch generation request in an empty workspace swaps the full coder
// prompt for the concise small-task prompt on the provider call.
func TestRunUsesSmallTaskPromptForEmptyDirGeneration(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", desc: "Write a file to disk."})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	const fullPrompt = "FULL-CODER-DOCTRINE: ### Verification policy and much more"
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		SystemPrompt: fullPrompt,
		WorkDir:      t.TempDir(), // empty workspace
	})
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write a hello-world web server in Go")))

	require.Len(t, provider.reqs, 1)
	sent := provider.reqs[0].SystemPrompt
	// The concise small-task prompt was used in place of the full doctrine.
	require.NotContains(t, sent, fullPrompt)
	require.Contains(t, sent, "empty or nearly empty directory")
	require.Contains(t, sent, "- write: Write a file to disk.")
}

// TestRunKeepsFullPromptForComplexTask asserts that a repo-aware request leaves
// the full coder prompt untouched even in an empty directory.
func TestRunKeepsFullPromptForComplexTask(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", desc: "Write a file to disk."})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	const fullPrompt = "FULL-CODER-DOCTRINE-MARKER"
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        registry,
		Sessions:     repo,
		SystemPrompt: fullPrompt,
		WorkDir:      t.TempDir(),
	})
	// "refactor" is a complex cue, so even in an empty dir the small path is declined.
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("refactor the parser to be table-driven")))

	require.Len(t, provider.reqs, 1)
	require.Contains(t, provider.reqs[0].SystemPrompt, fullPrompt)
}
