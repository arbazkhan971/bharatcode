package agent

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// projectInstructionsHeader delimits the project-instructions section
// injected into the assembled system prompt.
const projectInstructionsHeader = "# Project Instructions (AGENTS.md)"

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
	tpl, err := template.New("system").
		Funcs(template.FuncMap{
			"humanBytes": humanBytes,
			"shortPath":  shortPath,
			"now":        func() string { return time.Now().UTC().Format(time.RFC3339) },
		}).
		Parse(source)
	if err != nil {
		return "", fmt.Errorf("parsing prompt template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("rendering prompt template: %w", err)
	}
	base := strings.TrimSpace(buf.String())

	// Inject project instructions (AGENTS.md/CLAUDE.md chain). A load
	// failure is non-fatal: the prompt renders without the section.
	instructions, err := config.LoadInstructions(ctx)
	if err != nil {
		slog.Warn("Loading project instructions for system prompt", "error", err)
		instructions = ""
	}
	return injectInstructions(base, instructions), nil
}

// injectInstructions appends the project-instructions section to base,
// clearly delimited. When instr is empty after trimming, base is
// returned unchanged.
func injectInstructions(base, instr string) string {
	instr = strings.TrimSpace(instr)
	if instr == "" {
		return base
	}
	return base + "\n\n" + projectInstructionsHeader + "\n\n" + instr
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
