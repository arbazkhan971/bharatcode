package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/skills"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// projectInstructionsHeader delimits the project-instructions section
// injected into the assembled system prompt.
const projectInstructionsHeader = "# Project Instructions (AGENTS.md)"

// availableSkillsHeader delimits the available-skills section injected
// into the assembled system prompt.
const availableSkillsHeader = "## Available skills"

// skillUsageInstructions prefixes the available-skills block. It tells
// the model how to act on the advertised skills: load the manifest when
// the task matches, and resolve any relative paths the skill references
// against the skill's own directory.
const skillUsageInstructions = "Use the read tool to load a skill file when the task matches its description.\n" +
	"When a skill file references a relative path, resolve it against the skill directory and use that absolute path."

// availableRecipesHeader delimits the available-recipes section injected
// into the assembled system prompt.
const availableRecipesHeader = "## Available recipes"

// recipeUsageInstructions prefixes the available-recipes block. It tells
// the model that a recipe is a saved, parameterized workflow the user can
// launch with a slash command, so the model can suggest the relevant one
// when a request matches a recipe's description.
const recipeUsageInstructions = "Recipes are saved, parameterized prompts the user can run with /<name>.\n" +
	"When a request matches a recipe's description, suggest the user run that recipe."

// instructionFilenames lists the per-directory project-instruction files
// in priority order. AGENTS.md is preferred; CLAUDE.md is the fallback
// when AGENTS.md is absent in the same directory. This mirrors the
// discovery order used by config.LoadInstructions.
var instructionFilenames = []string{"AGENTS.md", "CLAUDE.md"}

// environmentHeader delimits the environment section. It is rendered at
// the very tail of the assembled prompt — after the (more volatile)
// instructions and skills sections — so that the stable prose above it
// stays prefix-stable for prompt-cache hits.
const environmentHeader = "## Environment"

// nowFunc supplies the timestamp rendered into the environment block's
// "Current date" line. It is a package var so tests can freeze the clock
// and get byte-stable prompts; production uses the real wall clock.
var nowFunc = time.Now

// environmentTemplate renders the trailing environment block. It uses the
// same PromptData and template funcs as the body so callers can move it
// freely relative to the injected instructions and skills.
const environmentTemplate = environmentHeader + `
- Working directory: {{.Workdir}}
- Platform: {{.OS}}/{{.Arch}}
- Git branch: {{.GitBranch}}{{if .GitStatus}}
- Git status: {{.GitStatus}}{{end}}{{if .GitRecentCommits}}
- Recent commits:
{{.GitRecentCommits}}{{end}}
- File tracker: {{.FileTrackerSummary}}
- Current date: {{now}}`

// skillSearchDirs returns the skills root directories to scan, global
// first then project, so project skills override global ones. It is a
// package var so tests can supply hermetic temp roots without touching
// the developer's real ~/.bharatcode/skills.
var skillSearchDirs = defaultSkillSearchDirs

// defaultSkillSearchDirs returns the standard skills roots: the global
// ~/.bharatcode/skills directory and the project ./.bharatcode/skills
// directory under workdir.
func defaultSkillSearchDirs(workdir string) []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".bharatcode", "skills"))
	}
	dirs = append(dirs, filepath.Join(workdir, ".bharatcode", "skills"))
	return dirs
}

// recipeSearchDirs returns the recipe directories to scan, global first
// then project, so project recipes override global ones. It is a package
// var so tests can supply hermetic temp roots without touching the
// developer's real recipe directories.
var recipeSearchDirs = defaultRecipeSearchDirs

// defaultRecipeSearchDirs returns the standard recipe roots in precedence
// order (global first, project second), mirroring recipe.DefaultDirs.
func defaultRecipeSearchDirs(workdir string) []string {
	return recipe.DefaultDirs(config.GlobalPath(), workdir)
}

//go:embed templates/*.md.tpl
var templateFS embed.FS

// PromptData is the data rendered into agent system-prompt templates.
type PromptData struct {
	Workdir            string
	OS                 string
	Arch               string
	GitBranch          string
	GitStatus          string
	GitRecentCommits   string
	AgentName          string
	Tools              []ToolInfo
	FileTrackerSummary string
}

// ToolInfo describes one callable tool in the system prompt.
type ToolInfo struct {
	Name        string
	Description string
	Schema      string
}

func renderPrompt(ctx context.Context, agentName, promptOverride string, registry toolSource, tracker *filetracker.Tracker) (string, error) {
	workdir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	data := PromptData{
		Workdir:            workdir,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		GitBranch:          gitBranch(ctx, workdir),
		GitStatus:          gitStatus(ctx, workdir),
		GitRecentCommits:   gitRecentCommits(ctx, workdir),
		AgentName:          agentName,
		Tools:              promptTools(registry.List()),
		FileTrackerSummary: fileSummary(tracker),
	}

	// Assemble the static body — instructions, tool descriptions, skills and
	// recipes — once per config and reuse it across turns. Everything that
	// feeds the body is folded into the cache key (see staticBodyKey), so a
	// config or profile change that alters the prompt produces a different key
	// and misses the cache, while a repeated turn under the same config hits
	// it and skips the template parse and directory scans.
	assembled, err := renderStaticBody(ctx, agentName, promptOverride, workdir, data)
	if err != nil {
		return "", err
	}

	// Append the environment block last, after the cached static body, so the
	// volatile workdir/date/git lines sit at the very tail of the prompt and
	// the prefix above them stays cache-stable across turns. The environment
	// block is intentionally not part of the cached body — it changes every
	// turn — so it is rendered fresh on each call.
	env, err := renderTemplate("environment", environmentTemplate, data)
	if err != nil {
		return "", err
	}
	return assembled + "\n\n" + env, nil
}

// staticBodyCache memoizes the assembled static prompt body keyed by a hash of
// the inputs that determine it. It lets repeated turns in one session reuse the
// instructions/tool/skills/recipes blocks instead of re-parsing the template
// and re-scanning the skill and recipe directories on every provider call,
// which also keeps the body's bytes identical so a cache-aware provider can
// serve it from its own prompt cache rather than re-billing it. The cache is
// process-wide and bounded only by the number of distinct configs seen in a
// run; correctness is preserved because any change to the config or active
// profile that alters the body also alters the key (see staticBodyKey).
var staticBodyCache sync.Map // staticBodyKey -> string

// staticBodyKey identifies a cached static prompt body. Every field that can
// change the rendered body is present so that a config or profile change
// invalidates the entry by missing the cache rather than serving stale text.
type staticBodyKey struct {
	agent      string // agent name selects the coder vs task template
	override   string // explicit prompt override, when the config supplies one
	workdir    string // roots instruction/skill/recipe discovery
	toolsSig   string // hash of the active tool set (name, description, schema)
	skillDirs  string // skill search roots, so a profile that swaps them invalidates
	recipeDirs string // recipe search roots, so a profile that swaps them invalidates
}

// renderStaticBody returns the assembled static prompt body — the template
// output plus the project-instructions, skills and recipes sections — caching
// it per config so repeated turns avoid the template parse and directory scans.
// The trailing environment block is deliberately excluded; the caller appends
// it fresh because it carries per-turn volatile state.
func renderStaticBody(ctx context.Context, agentName, promptOverride, workdir string, data PromptData) (string, error) {
	key := staticBodyKey{
		agent:      agentName,
		override:   promptOverride,
		workdir:    workdir,
		toolsSig:   toolsSignature(data.Tools),
		skillDirs:  strings.Join(skillSearchDirs(workdir), string(os.PathListSeparator)),
		recipeDirs: strings.Join(recipeSearchDirs(workdir), string(os.PathListSeparator)),
	}
	if cached, ok := staticBodyCache.Load(key); ok {
		return cached.(string), nil
	}

	body, err := assembleStaticBody(ctx, agentName, promptOverride, workdir, data)
	if err != nil {
		return "", err
	}
	staticBodyCache.Store(key, body)
	return body, nil
}

// assembleStaticBody renders the static prompt body from scratch: the agent
// template followed by the project-instructions, skills and recipes sections.
// It performs the template parse and the instruction/skill/recipe scans, and is
// the cache-miss path behind renderStaticBody.
func assembleStaticBody(ctx context.Context, agentName, promptOverride, workdir string, data PromptData) (string, error) {
	source := promptOverride
	if strings.TrimSpace(source) == "" {
		name := "coder"
		if agentName == "task" {
			name = "task"
		}
		raw, err := templateFS.ReadFile("templates/" + name + ".md.tpl")
		if err != nil {
			return "", fmt.Errorf("reading prompt template: %w", err)
		}
		source = string(raw)
	}

	base, err := renderTemplate("system", source, data)
	if err != nil {
		return "", err
	}

	// Inject project instructions (AGENTS.md/CLAUDE.md chain), each
	// attributed to the directory it came from so the model can resolve
	// relative paths against the right source. A load failure is
	// non-fatal: the prompt renders without the section.
	sources, err := loadInstructionSources(ctx, workdir)
	if err != nil {
		slog.Warn("Loading project instructions for system prompt", "error", err)
		sources = nil
	}
	assembled := injectInstructions(base, sources)

	// Inject a summary of discoverable skills so the model knows which
	// capabilities it can invoke. A load failure is non-fatal: the
	// prompt renders without the section.
	if set, err := skills.LoadSkills(skillSearchDirs(workdir)...); err != nil {
		slog.Warn("Loading skills for system prompt", "error", err)
	} else {
		assembled = injectSkills(assembled, set.Summaries())
	}

	// Inject a summary of discoverable recipes so the model knows which
	// saved workflows the user can launch. A load failure is non-fatal:
	// the prompt renders without the section.
	if reg, err := recipe.NewRegistry(recipeSearchDirs(workdir)...); err != nil {
		slog.Warn("Loading recipes for system prompt", "error", err)
	} else {
		assembled = injectRecipes(assembled, reg.Summaries())
	}

	return assembled, nil
}

// toolsSignature returns a stable hash of the active tool set so the static-body
// cache key changes whenever a tool is added, removed, renamed, or has its
// description or schema edited — any of which alters the rendered tool block.
// The tools are folded into the hash in their listed order; the same registry
// returns tools in the same order across turns, so an unchanged tool set yields
// the same signature and a cache hit.
func toolsSignature(tools []ToolInfo) string {
	h := sha256.New()
	for _, t := range tools {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", t.Name, t.Description, t.Schema)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// renderTemplate parses and executes a single prompt template with the
// shared funcs, returning the trimmed output or a wrapped error.
func renderTemplate(name, source string, data PromptData) (string, error) {
	tpl, err := template.New(name).
		Funcs(template.FuncMap{
			"humanBytes": humanBytes,
			"shortPath":  shortPath,
			"now":        func() string { return nowFunc().UTC().Format(time.RFC3339) },
		}).
		Parse(source)
	if err != nil {
		return "", fmt.Errorf("parsing %s prompt template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("rendering %s prompt template: %w", name, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// injectSkills appends the available-skills section to base. The section
// opens with usage instructions telling the model how to load and follow
// a skill, followed by the <available_skills> XML block produced by
// SkillSet.Summaries. When summaries is empty after trimming, base is
// returned unchanged so that no skills means no change to the prompt.
func injectSkills(base, summaries string) string {
	summaries = strings.TrimSpace(summaries)
	if summaries == "" {
		return base
	}
	return base + "\n\n" + availableSkillsHeader + "\n\n" +
		skillUsageInstructions + "\n\n" + summaries
}

// injectRecipes appends the available-recipes section to base. The section
// opens with usage instructions telling the model what a recipe is and when
// to suggest one, followed by the <available_recipes> XML block produced by
// recipe.Registry.Summaries. When summaries is empty after trimming, base is
// returned unchanged so that no recipes means no change to the prompt.
func injectRecipes(base, summaries string) string {
	summaries = strings.TrimSpace(summaries)
	if summaries == "" {
		return base
	}
	return base + "\n\n" + availableRecipesHeader + "\n\n" +
		recipeUsageInstructions + "\n\n" + summaries
}

// injectInstructions appends the project-instructions section to base.
// Each source file is wrapped in its own <project_instructions> element
// carrying the absolute path of its directory, and all of them sit inside
// a single <project_context> block. Path attribution lets the model
// resolve relative paths in a rule against the directory that rule came
// from. When sources is empty, base is returned unchanged.
func injectInstructions(base string, sources []instructionSource) string {
	rendered := renderInstructionsXML(sources)
	if rendered == "" {
		return base
	}
	return base + "\n\n" + projectInstructionsHeader + "\n\n" + rendered
}

// renderInstructionsXML renders the project-instruction sources as a
// <project_context> block, one path-attributed <project_instructions>
// element per non-empty source, in the order given. It returns an empty
// string when no source carries content.
func renderInstructionsXML(sources []instructionSource) string {
	var blocks []instructionSource
	for _, s := range sources {
		if strings.TrimSpace(s.Content) != "" {
			blocks = append(blocks, s)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_context>")
	for _, s := range blocks {
		b.WriteString("\n<project_instructions path=\"")
		b.WriteString(xmlAttr(s.Dir))
		b.WriteString("\">\n")
		b.WriteString(strings.TrimSpace(s.Content))
		b.WriteString("\n</project_instructions>")
	}
	b.WriteString("\n</project_context>")
	return b.String()
}

// xmlAttr escapes the characters that would break out of a double-quoted
// XML attribute value, so a directory path with markup characters stays
// well-formed.
func xmlAttr(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(s)
}

// instructionSource is one project-instruction file resolved to the
// directory it lives in (Dir) and its trimmed Content. Dir is the
// attribution path rendered into the <project_instructions> element so
// the model can resolve relative paths against the rule's source.
type instructionSource struct {
	Dir     string
	Content string
}

// loadInstructionSources resolves the project-instruction chain for
// workdir into path-attributed sources, ordered global-first then root
// down to workdir so deeper, more specific instructions appear last. It
// mirrors config.LoadInstructions's discovery (global AGENTS.md, then
// each directory's AGENTS.md or CLAUDE.md) but preserves each source's
// directory rather than flattening into one blob. A missing file is
// skipped; the returned error is reserved for unexpected read failures.
func loadInstructionSources(ctx context.Context, workdir string) ([]instructionSource, error) {
	_ = ctx
	var sources []instructionSource

	// Global instructions live alongside the global config file. They are
	// attributed to that directory so their relative paths resolve there.
	globalDir := filepath.Dir(config.GlobalPath())
	if globalDir != "" && globalDir != "." {
		if content, ok, err := readInstructionFile(filepath.Join(globalDir, "AGENTS.md")); err != nil {
			return nil, err
		} else if ok {
			sources = append(sources, instructionSource{Dir: globalDir, Content: content})
		}
	}

	for _, dir := range instructionDirChain(workdir) {
		content, ok, err := readInstructionDir(dir)
		if err != nil {
			return nil, err
		}
		if ok {
			sources = append(sources, instructionSource{Dir: dir, Content: content})
		}
	}
	return sources, nil
}

// instructionDirChain returns the directories from the repository root
// down to workdir inclusive, root-first, so deeper directories sort last
// and override shallower ones. When workdir is not inside a repository,
// it returns just workdir.
func instructionDirChain(workdir string) []string {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	root := instructionRepoRoot(abs)

	chain := []string{abs}
	current := abs
	for current != root {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
		chain = append(chain, current)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// instructionRepoRoot walks up from dir to the nearest directory holding
// a .git entry, returning it, or dir unchanged when none is found.
func instructionRepoRoot(dir string) string {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}

// readInstructionDir reads the first present instruction file in dir,
// trying instructionFilenames in priority order, returning its trimmed
// content and whether a file was found.
func readInstructionDir(dir string) (string, bool, error) {
	for _, name := range instructionFilenames {
		content, ok, err := readInstructionFile(filepath.Join(dir, name))
		if err != nil {
			return "", false, err
		}
		if ok {
			return content, true, nil
		}
	}
	return "", false, nil
}

// readInstructionFile reads path, returning its trimmed content and true
// when the file exists and is non-empty. A missing file is not an error.
func readInstructionFile(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading instructions file %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", false, nil
	}
	return trimmed, true, nil
}

func promptTools(list []tools.Tool) []ToolInfo {
	out := make([]ToolInfo, 0, len(list))
	for _, t := range list {
		out = append(out, ToolInfo{
			Name:        t.Name(),
			Description: strings.TrimSpace(t.Description()),
			Schema:      string(t.Schema()),
		})
	}
	return out
}

// smallTaskSystemPrompt is the concise system prompt used for small tasks —
// scaffolding a one-file app or a short file-generation request in an empty or
// near-empty directory. It deliberately omits the full coder doctrine: the
// long verification-policy block, the repo-aware instruction chain, and the
// skills/recipes catalog all carry overhead that a from-scratch generation
// does not need. What remains is a tight engineering policy plus the live tool
// list, so the input-token cost of a simple "write me an X" turn drops
// materially while the agent still knows how to write correct files and report
// a verification status. Complex repo edits stay on the full coder prompt.
const smallTaskSystemPrompt = `You are BharatCode's coding agent, generating files in an empty or nearly empty directory. The task is small and self-contained: create the files the user asked for, correctly and completely, with no scaffolding they did not request.

## Identity and product questions

- If the user asks who you are, what you are, what BharatCode is, or similar "about you" questions, answer directly: you are BharatCode, a terminal-based AI coding agent that helps inspect, edit, and verify software projects from the user's command line.
- Keep identity answers short and product-grounded. Mention that BharatCode can use the configured model/provider, local tools, and repository context to help with coding tasks.
- Do not claim to be OpenAI, ChatGPT, Codex CLI, Claude Code, OpenCode, or the underlying model. If relevant, say BharatCode may be using one of those providers or a local/open-weight model depending on configuration.
- Do not call tools for a simple identity/about question unless the user also asks about the current repository, installed version, configuration, or environment.

## Tools

You have the following tools available. The tools this kind of task usually
needs carry their full usage docs; the rest are listed by a one-line summary to
save space. Every tool's complete argument schema is in its tool definition, so
call any tool whose summary fits — read its schema there if you need the detail.
%s
Other custom tools may be exposed at runtime; use whatever is available.

## Policy

- Be concise. This output is read in a terminal: lead with the result and skip preamble.
- Write complete, runnable files. Do not leave TODOs or stub bodies unless the user asked for a sketch.
- Follow idiomatic conventions for the language and toolchain you are generating.
- Read a file before you edit it; never edit blind.
- Do not add comments unless the user asks or the logic is genuinely non-obvious.
- Keep the change minimal — only the files the request implies, nothing extra.

## Verifying

- After you generate the files, verify them with the project's own tooling when one exists (build, run, or test). If nothing runnable exists yet, say so.
- Never claim the result works unless you observed it work. End a turn that produced files with one status line:
  - **Verified** — name the command you ran and the result.
  - **Failed** — what failed and what you are doing about it.
  - **Skipped (no_test_command)** — there is no build, run, or test command to execute.

Text wrapped in <system-reminder> tags carries operational instructions from the harness and overrides any conflicting guidance here.`

// smallTaskCoreTools names the tools a short, from-scratch generation actually
// reaches for: writing and editing files and running the build/test that
// verifies them. Only these carry their full usage manual in the small-task
// prompt; every other tool is advertised by its one-line summary. The model
// still sees the full argument schema for any tool through the provider's tool
// definitions, so trimming the prose manual here costs no tool-call accuracy —
// it just keeps a simple "write me an X" turn from carrying every tool's docs.
var smallTaskCoreTools = map[string]struct{}{
	"write":     {},
	"edit":      {},
	"multiedit": {},
	"view":      {},
	"bash":      {},
	"ls":        {},
}

// buildSmallTaskPrompt renders the concise small-task system prompt over the
// supplied tool list. The tools a from-scratch generation is likely to need
// (smallTaskCoreTools) carry their full markdown manual; every other tool is
// advertised by its one-line summary (tools.ShortDescription) so the prompt
// lists the whole tool set without paying for every tool's docs. The full JSON
// schemas reach the model through the provider's tool definitions regardless,
// so a summarized tool stays callable. It returns the rendered prompt ready to
// be used as the base system prompt for a small-task turn.
func buildSmallTaskPrompt(list []tools.Tool) string {
	var b strings.Builder
	for _, t := range list {
		if _, core := smallTaskCoreTools[t.Name()]; core {
			full := strings.TrimSpace(t.Description())
			// A one-line manual sits inline after the bullet; a multi-line manual
			// is indented as a block under it so the markdown stays visually
			// attached to its tool name without spilling out of the list.
			if strings.IndexByte(full, '\n') < 0 {
				fmt.Fprintf(&b, "- %s: %s\n", t.Name(), full)
			} else {
				fmt.Fprintf(&b, "- %s:\n%s\n", t.Name(), indentToolDoc(full))
			}
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), tools.ShortDescription(t))
	}
	return fmt.Sprintf(smallTaskSystemPrompt, strings.TrimRight(b.String(), "\n"))
}

// indentToolDoc indents every line of a tool's full description by two spaces so
// the manual reads as a block nested under its "- <name>:" bullet. A blank line
// is left blank rather than padded with trailing whitespace.
func indentToolDoc(doc string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// smallTaskPromptCue is the upper bound (in runes) on a user request that can
// still qualify as a "small task". Beyond this length the request carries
// enough detail that the full coder doctrine is worth its tokens, so the small
// path is declined.
const smallTaskPromptCue = 600

// smallTaskVerbs are the leading action verbs that mark a from-scratch
// file-generation request ("write a CLI that…", "create a script to…"). The
// match is on the first word so a request that merely mentions one of these
// verbs deeper in a longer instruction does not trip the small path.
var smallTaskVerbs = map[string]struct{}{
	"write":     {},
	"create":    {},
	"generate":  {},
	"make":      {},
	"build":     {},
	"scaffold":  {},
	"implement": {},
	"add":       {},
}

// smallTaskComplexCues are substrings whose presence in the request signals a
// repo-aware edit rather than a clean-slate generation. Any one of them
// disqualifies the small path even in an empty directory, because the request
// is about changing or reasoning over existing code rather than producing a
// new file from nothing.
var smallTaskComplexCues = []string{
	"refactor", "debug", "fix the", "fix a", "investigate", "trace",
	"existing", "codebase", "repository", "migrate", "test suite",
}

// isSmallTaskPrompt reports whether the user's request looks like a short,
// self-contained file-generation prompt: it opens with a generation verb, is
// not longer than smallTaskPromptCue runes, and carries none of the
// complex-edit cues that mark a repo-aware change. It is one half of the
// small-task decision; the caller also requires an empty-ish working directory
// (see directoryIsEmptyish) before switching to the concise prompt.
func isSmallTaskPrompt(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) > smallTaskPromptCue {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, cue := range smallTaskComplexCues {
		if strings.Contains(lower, cue) {
			return false
		}
	}
	first := lower
	if i := strings.IndexFunc(lower, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }); i >= 0 {
		first = lower[:i]
	}
	_, ok := smallTaskVerbs[first]
	return ok
}

// directoryIsEmptyish reports whether dir holds no files that a coding agent
// would treat as existing project state. A handful of incidental entries — VCS
// metadata, OS cruft, an editor config — do not count as a project, so a
// directory holding only those still qualifies as empty for small-task
// purposes. A missing or unreadable directory is treated as empty: there is
// no existing code to reason over. A directory with real source or manifest
// files is not empty.
func directoryIsEmptyish(dir string) bool {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if _, skip := smallTaskIgnoredEntries[e.Name()]; skip {
			continue
		}
		return false
	}
	return true
}

// smallTaskIgnoredEntries names directory entries that do not constitute
// existing project state, so a directory holding only these still counts as
// empty for small-task detection.
var smallTaskIgnoredEntries = map[string]struct{}{
	".git":          {},
	".gitignore":    {},
	".DS_Store":     {},
	".bharatcode":   {},
	".idea":         {},
	".vscode":       {},
	"AGENTS.md":     {},
	"CLAUDE.md":     {},
	"LICENSE":       {},
	"README.md":     {},
	"Thumbs.db":     {},
	".editorconfig": {},
}

func gitBranch(ctx context.Context, workdir string) string {
	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "(not a git repo)"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "(detached head)"
	}
	return branch
}

// gitStatusFileCap bounds how many changed paths the environment block lists
// before collapsing the remainder into an "and N more" tail, so a large
// working tree cannot flood the system prompt.
const gitStatusFileCap = 10

// gitStatus summarizes the working tree for the environment block so the model
// starts each session aware of uncommitted changes instead of having to run a
// bash command to discover them. It returns "clean" for a tracked tree with no
// changes, a compact "N uncommitted change(s): <porcelain entries>" line
// otherwise, and the empty string when workdir is not a git repository (the
// caller then omits the status line entirely). Like the rest of the
// environment block this is a snapshot taken when the prompt is built, not a
// live view.
func gitStatus(ctx context.Context, workdir string) string {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, or git is unavailable: omit the line.
		return ""
	}

	var entries []string
	for _, line := range strings.Split(string(out), "\n") {
		// Porcelain v1 lines are "XY path"; trim the leading status padding
		// so " M file" reads as "M file" while "?? file" is preserved.
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return "clean"
	}

	shown := entries
	if len(shown) > gitStatusFileCap {
		shown = shown[:gitStatusFileCap]
	}
	noun := "change"
	if len(entries) != 1 {
		noun = "changes"
	}
	summary := fmt.Sprintf("%d uncommitted %s: %s", len(entries), noun, strings.Join(shown, ", "))
	if len(entries) > gitStatusFileCap {
		summary += fmt.Sprintf(", and %d more", len(entries)-gitStatusFileCap)
	}
	return summary
}

// gitRecentCommitsCap bounds how many recent commits the environment block
// lists so a busy history cannot flood the system prompt.
const gitRecentCommitsCap = 5

// gitRecentCommitLineCap bounds the rendered width of a single commit line
// (short hash + subject) so an unusually long subject stays compact.
const gitRecentCommitLineCap = 100

// gitRecentCommits summarizes the tip of the current branch's history for the
// environment block so the model starts each session aware of recent work and
// the repository's commit-message conventions instead of having to run a bash
// command to discover them. Each commit renders as an indented "  <short-hash>
// <subject>" line; the whole block is empty when workdir is not a git
// repository or has no commits yet (the caller then omits the section). Like
// the rest of the environment block this is a snapshot taken when the prompt is
// built, not a live view.
func gitRecentCommits(ctx context.Context, workdir string) string {
	cmd := exec.CommandContext(ctx, "git", "log", "--format=%h %s", "-n", fmt.Sprint(gitRecentCommitsCap))
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, git is unavailable, or the branch has no
		// commits yet: omit the section.
		return ""
	}

	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		if len(entry) > gitRecentCommitLineCap {
			entry = entry[:gitRecentCommitLineCap-1] + "…"
		}
		lines = append(lines, "  "+entry)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func fileSummary(tracker *filetracker.Tracker) string {
	if tracker == nil {
		return "No tracked file changes for this prompt."
	}
	return "File tracking is enabled for this session."
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
