package extension

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// manifestName is the file every directory extension carries to describe itself.
const manifestName = "extension.json"

// commandExt is the extension of a command body file discovered under an
// extension's commands/ subdirectory.
const commandExt = ".md"

// Manifest is the on-disk shape of an extension.json. A directory extension
// declares its identity and the commands it contributes; it cannot ship Go code,
// so it registers commands only. Each command's body is the inline Prompt, or —
// when Prompt is empty — the contents of commands/<name>.md beside the manifest.
type Manifest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Commands    []ManifestCommand `json:"commands,omitempty"`
}

// ManifestCommand declares one command in a Manifest.
type ManifestCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Prompt is the inline command body. When empty the loader reads
	// commands/<PromptFile> (or commands/<name>.md when PromptFile is empty).
	Prompt string `json:"prompt,omitempty"`
	// PromptFile names a file under the extension's commands/ directory whose
	// contents are the command body. Ignored when Prompt is non-empty.
	PromptFile string `json:"prompt_file,omitempty"`
}

// Options configures Load.
type Options struct {
	// UserDir is the per-user extensions directory (~/.bharatcode/extensions).
	UserDir string
	// ProjectDir is the project-local extensions directory
	// (<project>/.bharat/extensions). It is scanned after UserDir, so a
	// project command shadows a same-named user command.
	ProjectDir string
	// Env is the execution environment handed to compiled extensions. When nil an
	// empty environment is used.
	Env ExecEnv
}

// Load builds a Host from the compiled extension registry and the user and
// project extension directories. Compiled extensions register first (so their
// tools and handlers are available), then directory extensions are loaded
// user-first and project-second so a project command overrides a user one. A
// malformed manifest, an unreadable directory, or a failing Setup is logged and
// skipped — a single bad extension never fails the whole load, mirroring how the
// skills and recipes loaders degrade.
func Load(opts Options) (*Host, error) {
	host := NewHost(opts.Env)

	for _, ext := range registeredExtensions() {
		if err := ext.Setup(host); err != nil {
			slog.Warn("Skipping extension: setup failed", "extension", ext.Name(), "error", err)
			continue
		}
		host.addName(ext.Name())
	}

	for _, dir := range []string{opts.UserDir, opts.ProjectDir} {
		loadDir(host, dir)
	}
	return host, nil
}

// loadDir scans dir for subdirectories that carry an extension.json manifest and
// registers each one's commands into host. A missing dir is silently skipped.
func loadDir(host *Host, dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("Skipping extensions directory: cannot read", "dir", dir, "error", err)
		}
		return
	}
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		extDir := filepath.Join(dir, de.Name())
		manifest, err := readManifest(extDir)
		if err != nil {
			slog.Warn("Skipping extension: bad manifest", "dir", extDir, "error", err)
			continue
		}
		if manifest == nil {
			// No manifest in this subdirectory; it is not an extension.
			continue
		}
		name := manifest.Name
		if name == "" {
			name = de.Name()
		}
		registerManifestCommands(host, extDir, name, manifest)
		host.addName(name)
	}
}

// readManifest reads and parses the extension.json in extDir. It returns
// (nil, nil) when the directory has no manifest so the caller can skip it
// without treating the absence as an error.
func readManifest(extDir string) (*Manifest, error) {
	path := filepath.Join(extDir, manifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &m, nil
}

// registerManifestCommands resolves each manifest command's body and registers
// it on host with override semantics (project wins over user). A command with an
// empty name, or one whose body cannot be resolved, is skipped with a warning.
func registerManifestCommands(host *Host, extDir, source string, m *Manifest) {
	for _, mc := range m.Commands {
		if strings.TrimSpace(mc.Name) == "" {
			slog.Warn("Skipping extension command: empty name", "extension", source)
			continue
		}
		prompt, err := resolveCommandBody(extDir, mc)
		if err != nil {
			slog.Warn("Skipping extension command: cannot resolve body", "extension", source, "command", mc.Name, "error", err)
			continue
		}
		host.putCommand(Command{
			Name:        mc.Name,
			Description: mc.Description,
			Prompt:      prompt,
			Source:      source,
		})
	}
}

// resolveCommandBody returns the prompt body for a manifest command: the inline
// Prompt when set, otherwise the contents of commands/<PromptFile> (or
// commands/<name>.md when PromptFile is empty), relative to the extension
// directory. The file path is constrained to the extension's commands/
// subdirectory so a manifest cannot read arbitrary files via "..".
func resolveCommandBody(extDir string, mc ManifestCommand) (string, error) {
	if strings.TrimSpace(mc.Prompt) != "" {
		return mc.Prompt, nil
	}
	file := mc.PromptFile
	if strings.TrimSpace(file) == "" {
		file = mc.Name + commandExt
	}
	commandsDir := filepath.Join(extDir, "commands")
	full := filepath.Join(commandsDir, file)
	// Guard against path traversal: the resolved path must stay within commands/.
	if rel, err := filepath.Rel(commandsDir, full); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("command body path %q escapes the commands directory", file)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("reading command body %s: %w", full, err)
	}
	return string(data), nil
}

// UserDir returns the canonical per-user extensions directory,
// ~/.bharatcode/extensions. It returns "" when the home directory cannot be
// resolved.
func UserDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".bharatcode", "extensions")
}

// ProjectDir returns the canonical project-local extensions directory,
// <projectRoot>/.bharat/extensions. It returns "" when projectRoot is empty.
func ProjectDir(projectRoot string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".bharat", "extensions")
}

// NewOSEnv returns an ExecEnv backed by the real process environment, rooted at
// workDir. It is the production accessor handed to compiled extensions.
func NewOSEnv(workDir string) ExecEnv {
	return osEnv{workDir: workDir}
}

type osEnv struct {
	workDir string
}

func (e osEnv) WorkDir() string          { return e.workDir }
func (e osEnv) Getenv(key string) string { return os.Getenv(key) }
func (e osEnv) Environ() []string        { return os.Environ() }
