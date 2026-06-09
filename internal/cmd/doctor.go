package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/spf13/cobra"
)

// doctorKnownAPIKeyEnvVars lists the provider API-key environment
// variables that doctor always reports, even when no config file is
// present or the config fails to load. Provider-specific env vars
// discovered in the loaded config are merged on top of this set.
var doctorKnownAPIKeyEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"DEEPSEEK_API_KEY",
	"MOONSHOT_API_KEY",
	"GROQ_API_KEY",
	"TOGETHER_API_KEY",
	"FIREWORKS_API_KEY",
	"OPENROUTER_API_KEY",
}

// doctorStatus is the severity marker printed at the start of a doctor
// checklist line.
type doctorStatus string

const (
	doctorStatusOK   doctorStatus = "OK"
	doctorStatusWarn doctorStatus = "WARN"
)

// NewDoctorCmd builds the "doctor" subcommand, which inspects the local
// environment and prints a health checklist covering the Go runtime,
// configuration, provider API-key environment variables, required
// external binaries, and the resolved data directory. It never prints
// secret values: API keys are reported only as set or unset.
//
// NewDoctorCmd is exported because registering the command requires a
// one-line edit to internal/cmd/root.go (appending NewDoctorCmd() to
// the root.AddCommand(...) list), which is owned separately.
func NewDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local environment and configuration",
		Long: "Run a series of environment checks and print a health " +
			"checklist. Reports the Go runtime version, config file " +
			"presence and validity, which provider API-key environment " +
			"variables are set (never the values), whether required " +
			"binaries are on PATH, and the data directory location.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			runDoctor(cmd.Context(), cmd.OutOrStdout(), opts, doctorLookPath, config.LoadFrom)
			return nil
		},
	}
}

// doctorLookPath reports whether name resolves to an executable on PATH
// and, if so, the resolved path. It is a thin wrapper over
// exec.LookPath so tests can substitute a deterministic lookup.
func doctorLookPath(name string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}

// doctorConfigLoader matches config.LoadFrom so doctor can be exercised
// with a stub in tests without touching real config paths.
type doctorConfigLoader func(ctx context.Context, globalPath, projectPath string) (*config.Config, error)

// runDoctor performs all checks and writes the checklist to w. It is
// split from NewDoctorCmd so tests can inject a binary lookup and a
// config loader. runDoctor is deliberately non-fatal: a failed config
// load becomes a WARN line rather than an error return, so the rest of
// the report still renders.
func runDoctor(ctx context.Context, w io.Writer, opts *rootOptions, look func(string) (string, bool), load doctorConfigLoader) {
	_, _ = fmt.Fprintln(w, "BharatCode doctor")
	_, _ = fmt.Fprintln(w)

	doctorSection(w, "Runtime")
	doctorLine(w, doctorStatusOK, "Go runtime", runtime.Version())

	doctorSection(w, "Configuration")
	cfg := doctorCheckConfig(ctx, w, opts, load)

	doctorSection(w, "Provider API keys")
	doctorCheckAPIKeys(w, cfg)

	doctorCheckChatGPTSubscription(w, cfg)

	doctorSection(w, "External tools")
	doctorCheckTools(w, cfg, look)

	doctorSection(w, "Data directory")
	doctorLine(w, doctorStatusOK, "Data directory", doctorDataDir())
}

// doctorCheckConfig resolves the config path the same way the other
// subcommands do, reports whether the file exists and whether it loads
// and validates, and returns the loaded config (or nil on failure).
func doctorCheckConfig(ctx context.Context, w io.Writer, opts *rootOptions, load doctorConfigLoader) *config.Config {
	path := opts.configPath
	if path == "" {
		path = config.GlobalPath()
	}
	resolved := util.ExpandPath(path)

	project := ""
	if opts.projectDir != "" {
		project = config.ProjectPath(opts.projectDir)
	}

	switch _, err := os.Stat(resolved); {
	case resolved == "":
		doctorLine(w, doctorStatusWarn, "Config file", "could not resolve config path")
	case err == nil:
		doctorLine(w, doctorStatusOK, "Config file", resolved)
	case os.IsNotExist(err):
		doctorLine(w, doctorStatusWarn, "Config file", fmt.Sprintf("not found at %s (built-in defaults will be used)", resolved))
	default:
		doctorLine(w, doctorStatusWarn, "Config file", fmt.Sprintf("cannot stat %s: %v", resolved, err))
	}

	cfg, err := load(ctx, resolved, project)
	if err != nil {
		doctorLine(w, doctorStatusWarn, "Config valid", err.Error())
		return nil
	}
	doctorLine(w, doctorStatusOK, "Config valid", "parsed and validated")
	return cfg
}

// doctorCheckAPIKeys reports, for each known and configured provider
// env var, whether it is set in the environment. The secret value is
// never read or printed; only set/unset state is reported.
func doctorCheckAPIKeys(w io.Writer, cfg *config.Config) {
	for _, name := range doctorAPIKeyEnvVars(cfg) {
		if _, ok := os.LookupEnv(name); ok {
			doctorLine(w, doctorStatusOK, name, "set")
		} else {
			doctorLine(w, doctorStatusWarn, name, "not set")
		}
	}
}

// doctorCheckChatGPTSubscription reports the sign-in status for the configured
// ChatGPT provider. It is only shown when a chatgpt provider is enabled in the
// loaded config, because that is the only time the credential file matters.
func doctorCheckChatGPTSubscription(w io.Writer, cfg *config.Config) {
	if !doctorHasEnabledProvider(cfg, config.ProviderChatGPT) {
		return
	}

	id, err := llm.ChatGPTStatus()
	if err != nil {
		doctorLine(w, doctorStatusWarn, "ChatGPT subscription", "not signed in (run 'bharatcode auth chatgpt')")
		return
	}

	detail := "signed in"
	if id.Email != "" {
		detail = "signed in as " + id.Email
	}
	if id.Plan != "" {
		detail += " on the " + id.Plan + " plan"
	}
	if id.Expired {
		detail += " (access token expired; will refresh on next use)"
	}
	doctorLine(w, doctorStatusOK, "ChatGPT subscription", detail)
}

// doctorHasEnabledProvider reports whether cfg contains an enabled provider of
// the given type.
func doctorHasEnabledProvider(cfg *config.Config, typ config.ProviderType) bool {
	if cfg == nil {
		return false
	}
	for _, p := range cfg.Providers {
		if p.Type == typ && !p.Disabled {
			return true
		}
	}
	return false
}

// doctorAPIKeyEnvVars returns the sorted union of the always-reported
// known env vars and any api_key_env values declared by providers in
// cfg.
func doctorAPIKeyEnvVars(cfg *config.Config) []string {
	seen := make(map[string]struct{})
	for _, name := range doctorKnownAPIKeyEnvVars {
		seen[name] = struct{}{}
	}
	if cfg != nil {
		for _, p := range cfg.Providers {
			if p.APIKeyEnv != "" {
				seen[p.APIKeyEnv] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// doctorCheckTools reports whether ripgrep (rg) and each enabled LSP
// server command from cfg are present on PATH.
func doctorCheckTools(w io.Writer, cfg *config.Config, look func(string) (string, bool)) {
	if path, ok := look("rg"); ok {
		doctorLine(w, doctorStatusOK, "ripgrep (rg)", path)
	} else {
		doctorLine(w, doctorStatusWarn, "ripgrep (rg)", "not found on PATH")
	}

	if cfg == nil {
		doctorLine(w, doctorStatusWarn, "LSP servers", "config unavailable; cannot check LSP binaries")
		return
	}
	enabled := 0
	for _, lsp := range cfg.LSP {
		if lsp.Disabled || lsp.Command == "" {
			continue
		}
		enabled++
		label := fmt.Sprintf("LSP %s (%s)", lsp.Name, lsp.Command)
		if path, ok := look(lsp.Command); ok {
			doctorLine(w, doctorStatusOK, label, path)
		} else {
			doctorLine(w, doctorStatusWarn, label, "not found on PATH")
		}
	}
	if enabled == 0 {
		doctorLine(w, doctorStatusOK, "LSP servers", "none configured")
	}
}

// doctorDataDir mirrors internal/app's defaultDBPath data-home
// resolution exactly: $XDG_DATA_HOME/bharatcode, else
// ~/.local/share/bharatcode, else ./bharatcode. The app currently
// ignores options.data_dir for path building, so doctor reports the
// path the app actually uses rather than the unwired config field.
func doctorDataDir() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		dataHome = "."
	}
	return filepath.Join(util.ExpandPath(dataHome), "bharatcode")
}

// doctorSection writes a checklist section header.
func doctorSection(w io.Writer, name string) {
	_, _ = fmt.Fprintf(w, "%s:\n", name)
}

// doctorLine writes a single checklist entry: a status marker, a label,
// and a detail string.
func doctorLine(w io.Writer, status doctorStatus, label, detail string) {
	_, _ = fmt.Fprintf(w, "  [%-4s] %s: %s\n", status, label, detail)
}
