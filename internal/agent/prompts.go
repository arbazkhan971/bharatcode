package agent

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
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
- Git branch: {{.GitBranch}}
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

//go:embed templates/*.md.tpl
var templateFS embed.FS

// PromptData is the data rendered into agent system-prompt templates.
type PromptData struct {
	Workdir            string
	OS                 string
	Arch               string
	GitBranch          string
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
	source := promptOverride
	if strings.TrimSpace(source) == "" {
		name := "coder"
		if agentName == "task" {
			name = "task"
		}
		data, err := templateFS.ReadFile("templates/" + name + ".md.tpl")
		if err != nil {
			return "", fmt.Errorf("reading prompt template: %w", err)
		}
		source = string(data)
	}

	data := PromptData{
		Workdir:            workdir,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		GitBranch:          gitBranch(ctx, workdir),
		AgentName:          agentName,
		Tools:              promptTools(registry.List()),
		FileTrackerSummary: fileSummary(tracker),
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

	// Append the environment block last, after the volatile instructions
	// and skills, so workdir/date sit at the very tail of the prompt.
	env, err := renderTemplate("environment", environmentTemplate, data)
	if err != nil {
		return "", err
	}
	return assembled + "\n\n" + env, nil
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
