package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
	"github.com/spf13/cobra"
)

// Prompter asks the user a question and returns their answer. It is
// injectable so init can run under test without a TTY: tests supply a
// stub, while interactive use reads a line from stdin. The question is
// already formatted (including any option list); the implementation
// only has to render it and return the trimmed reply.
type Prompter func(question string) (string, error)

// stdinPrompter returns a Prompter that writes the question to w and
// reads a single line of reply from r. A blank reply (just Enter)
// yields the empty string, letting callers apply a default.
func stdinPrompter(r *bufio.Reader, w io.Writer) Prompter {
	return func(question string) (string, error) {
		if _, err := fmt.Fprint(w, question); err != nil {
			return "", fmt.Errorf("writing prompt: %w", err)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			// EOF with content on the line is still a valid answer.
			if line == "" {
				return "", fmt.Errorf("reading answer: %w", err)
			}
		}
		return strings.TrimSpace(line), nil
	}
}

func newInitCmd() *cobra.Command {
	var project bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a starter config file",
		Long: "Scaffold a starter bharatcode config file pre-populated with the\n" +
			"provider list and sensible defaults. Prompts for a default provider\n" +
			"and reminds you which API-key environment variable to set. Writes the\n" +
			"global config by default, or a project-local .bharatcode.json with\n" +
			"--project. An existing config is never clobbered without confirmation.",
		Example: "  bharatcode init\n  bharatcode init --project",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			prompt := stdinPrompter(bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout())
			return runInit(cmd, opts, project, prompt)
		},
	}
	cmd.Flags().BoolVar(&project, "project", false, "write a project-local .bharatcode.json instead of the global config")
	return cmd
}

// initTargetPath resolves where init should write, honoring the
// --config override first, then --project (the .bharatcode.json in the
// project dir or cwd), then the global config path.
func initTargetPath(opts *rootOptions, project bool) (string, error) {
	if opts.configPath != "" {
		return util.ExpandPath(opts.configPath), nil
	}
	if project {
		dir := opts.projectDir
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolving working directory: %w", err)
			}
			dir = cwd
		}
		return filepath.Join(dir, ".bharatcode.json"), nil
	}
	global := config.GlobalPath()
	if global == "" {
		return "", fmt.Errorf("resolving global config path")
	}
	return global, nil
}

// runInit builds a starter config, prompts for the default provider,
// and writes it without clobbering an existing file. prompt is
// injected so the flow is fully testable without a TTY.
func runInit(cmd *cobra.Command, opts *rootOptions, project bool, prompt Prompter) error {
	out := cmd.OutOrStdout()

	path, err := initTargetPath(opts, project)
	if err != nil {
		return err
	}

	cfg := config.Default()

	provider, err := promptDefaultProvider(cfg.Providers, prompt)
	if err != nil {
		return err
	}
	if err := applyDefaultProvider(cfg, provider); err != nil {
		return err
	}

	// Remind the user which secret to export. Local providers (ollama,
	// lmstudio) need no key, so only nag when one is expected.
	if provider.APIKeyEnv != "" {
		_, _ = fmt.Fprintf(out, "\nRemember to set your API key:\n  export %s=...\n", provider.APIKeyEnv)
	} else {
		_, _ = fmt.Fprintf(out, "\nProvider %q runs locally and needs no API key.\n", provider.Name)
	}

	target := path
	if _, statErr := os.Stat(path); statErr == nil {
		// A config already lives here. Never clobber silently: confirm,
		// and on anything but an explicit yes, divert to a .example file
		// so the user keeps their existing config intact.
		ok, confErr := confirmOverwrite(path, prompt)
		if confErr != nil {
			return confErr
		}
		if !ok {
			target = path + ".example"
			_, _ = fmt.Fprintf(out, "Keeping existing config; writing starter to %s instead.\n", target)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("checking %s: %w", path, statErr)
	}

	if err := writeStarterConfig(cmd.Context(), target, cfg, provider); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Wrote config to %s\n", target)
	return nil
}

// writeStarterConfig marshals cfg and writes it atomically, prepending
// commented guidance so the scaffolded file documents itself. JSON has
// no native comment syntax, so guidance rides as leading "_comment"
// string keys; the loader ignores unknown keys, so the file stays
// valid and re-loadable. The atomic-write and directory-creation
// semantics match saveConfigPath.
func writeStarterConfig(ctx context.Context, path string, cfg *config.Config, provider config.Provider) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("writing starter config: %w", err)
	}
	path = util.ExpandPath(path)
	if err := fsext.EnsureDir(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensuring config directory: %w", err)
	}

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	data, err := injectGuidance(body, provider)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := fsext.AtomicWrite(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// injectGuidance splices leading "_comment" guidance keys into an
// already-marshaled config object so the starter file explains itself.
// body must be the indented JSON of a non-empty object (it always is:
// Config marshals to an object with fields). The guidance keys are
// unknown to the Config struct and silently ignored on load.
func injectGuidance(body []byte, provider config.Provider) ([]byte, error) {
	keyReminder := "set the chosen provider's API key in your shell before running bharatcode"
	if provider.APIKeyEnv != "" {
		keyReminder = fmt.Sprintf("export %s=... before running bharatcode", provider.APIKeyEnv)
	} else {
		keyReminder = fmt.Sprintf("provider %q runs locally and needs no API key", provider.Name)
	}

	// Each guidance line becomes a "_comment*" string key. Marshaling
	// the values escapes them safely; the keys are namespaced so they
	// never collide with real Config fields.
	guidance := []struct{ key, val string }{
		{"_comment", "bharatcode starter config. Edit freely; lines beginning with _comment are guidance and are ignored on load."},
		{"_comment_provider", fmt.Sprintf("default agent is set to provider %q. Change agents[0].model to switch models/providers from the lists below.", provider.Name)},
		{"_comment_api_key", keyReminder},
	}

	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("cannot inject guidance: config did not marshal to a JSON object")
	}

	var b strings.Builder
	b.WriteString("{\n")
	for _, g := range guidance {
		k, err := json.Marshal(g.key)
		if err != nil {
			return nil, fmt.Errorf("marshaling guidance key: %w", err)
		}
		v, err := json.Marshal(g.val)
		if err != nil {
			return nil, fmt.Errorf("marshaling guidance value: %w", err)
		}
		b.WriteString("  ")
		b.Write(k)
		b.WriteString(": ")
		b.Write(v)
		b.WriteString(",\n")
	}
	// Re-attach the original object's body, dropping its opening brace
	// and the newline that followed it so our injected keys lead.
	rest := strings.TrimPrefix(trimmed, "{")
	rest = strings.TrimPrefix(rest, "\n")
	b.WriteString(rest)
	return []byte(b.String()), nil
}

// promptDefaultProvider lists the available providers and asks the user
// to pick one to default to. A blank answer selects the first provider.
// The selection accepts either a 1-based index or a provider name.
func promptDefaultProvider(providers []config.Provider, prompt Prompter) (config.Provider, error) {
	if len(providers) == 0 {
		return config.Provider{}, fmt.Errorf("no providers available in defaults")
	}

	var b strings.Builder
	b.WriteString("Available providers:\n")
	for i, p := range providers {
		b.WriteString(fmt.Sprintf("  %d) %s\n", i+1, p.Name))
	}
	b.WriteString(fmt.Sprintf("Choose a default provider [1-%d] (default %s): ", len(providers), providers[0].Name))

	answer, err := prompt(b.String())
	if err != nil {
		return config.Provider{}, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return providers[0], nil
	}

	// A numeric answer selects by 1-based position.
	if n, convErr := strconv.Atoi(answer); convErr == nil {
		if n < 1 || n > len(providers) {
			return config.Provider{}, fmt.Errorf("provider choice %d out of range [1-%d]", n, len(providers))
		}
		return providers[n-1], nil
	}

	// Otherwise match by name, case-insensitively.
	for _, p := range providers {
		if strings.EqualFold(p.Name, answer) {
			return p, nil
		}
	}
	return config.Provider{}, fmt.Errorf("unknown provider %q", answer)
}

// applyDefaultProvider points the coder agent at the chosen provider's
// first model so the scaffolded config is immediately usable, and keeps
// the result valid (the model must exist in cfg.Models).
func applyDefaultProvider(cfg *config.Config, provider config.Provider) error {
	if len(provider.Models) == 0 {
		return fmt.Errorf("provider %q has no models to default to", provider.Name)
	}
	model := provider.Models[0]

	// Confirm the chosen model exists in the model catalog; Validate
	// would otherwise reject the agent reference.
	known := false
	for _, m := range cfg.Models {
		if m.ID == model {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("provider %q default model %q missing from model catalog", provider.Name, model)
	}

	if len(cfg.Agents) == 0 {
		cfg.Agents = []config.Agent{{
			Name:         "coder",
			SystemPrompt: "You are a helpful software engineering assistant.",
		}}
	}
	cfg.Agents[0].Model = model
	return nil
}

// confirmOverwrite asks whether to overwrite an existing config. Only an
// explicit yes returns true; a blank answer defaults to no so a stray
// Enter can never destroy a config.
func confirmOverwrite(path string, prompt Prompter) (bool, error) {
	answer, err := prompt(fmt.Sprintf("Config already exists at %s. Overwrite? [y/N]: ", path))
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
