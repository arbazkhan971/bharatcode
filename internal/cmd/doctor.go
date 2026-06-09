package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/spf13/cobra"
)

// doctorActiveAgent is the agent doctor reports on when no other default is
// resolvable: it mirrors the "coder" fallback the run command uses.
const doctorActiveAgent = "coder"

// doctorDialTimeout bounds the reachability probe for local providers so a
// stalled endpoint cannot hang the checklist.
const doctorDialTimeout = 750 * time.Millisecond

// doctorProviderCheckTimeout bounds the optional --check-provider smoke request
// so an unreachable or slow provider cannot hang the command. It is short on
// purpose: the probe only needs the model to start answering, not to finish.
const doctorProviderCheckTimeout = 15 * time.Second

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
	var checkProvider bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local environment and configuration",
		Long: "Run a series of environment checks and print a health " +
			"checklist. Reports the Go runtime version, config file " +
			"presence and validity, which provider API-key environment " +
			"variables are set (never the values), whether required " +
			"binaries are on PATH, and the data directory location.\n\n" +
			"By default doctor is offline-fast and contacts no provider. " +
			"Pass --check-provider to additionally send one tiny test " +
			"request to the active model and report whether it can answer.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			cfg := runDoctor(cmd.Context(), cmd.OutOrStdout(), opts, doctorLookPath, config.LoadFrom, doctorDial)
			if checkProvider {
				doctorSection(cmd.OutOrStdout(), "Provider smoke check")
				doctorCheckProvider(cmd.Context(), cmd.OutOrStdout(), cfg, doctorSmoke)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkProvider, "check-provider", false,
		"send one tiny test request to the active model and report whether it can answer")
	return cmd
}

// doctorSmokeFunc matches llm.Smoke so the provider probe can be stubbed in
// tests without making a real network request.
type doctorSmokeFunc func(ctx context.Context, p llm.Provider, model string, timeout time.Duration) (llm.SmokeResult, error)

// doctorSmoke is the production smoke probe; it delegates to llm.Smoke.
func doctorSmoke(ctx context.Context, p llm.Provider, model string, timeout time.Duration) (llm.SmokeResult, error) {
	return llm.Smoke(ctx, p, model, timeout)
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

// doctorDial reports whether a TCP connection to a "host:port" address can be
// established within doctorDialTimeout. It backs the local-provider
// reachability probe and is injectable so tests stay offline and deterministic.
func doctorDial(address string) bool {
	conn, err := net.DialTimeout("tcp", address, doctorDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// runDoctor performs all offline checks and writes the checklist to w. It is
// split from NewDoctorCmd so tests can inject a binary lookup and a
// config loader. runDoctor is deliberately non-fatal: a failed config
// load becomes a WARN line rather than an error return, so the rest of
// the report still renders. It returns the loaded config (or nil) so the
// optional provider smoke check can reuse it without re-reading the file.
//
// runDoctor makes no network requests of its own: the local-provider
// reachability probe is a bare TCP dial, never a model call. The model
// smoke check is opt-in and lives outside this function.
func runDoctor(ctx context.Context, w io.Writer, opts *rootOptions, look func(string) (string, bool), load doctorConfigLoader, dial func(string) bool) *config.Config {
	_, _ = fmt.Fprintln(w, "BharatCode doctor")
	_, _ = fmt.Fprintln(w)

	doctorSection(w, "Runtime")
	doctorLine(w, doctorStatusOK, "Go runtime", runtime.Version())

	doctorSection(w, "Configuration")
	cfg := doctorCheckConfig(ctx, w, opts, load)

	doctorSection(w, "Active agent")
	doctorCheckActiveAgent(w, cfg, dial)

	doctorSection(w, "Provider API keys")
	doctorCheckAPIKeys(w, cfg)

	doctorCheckChatGPTSubscription(w, cfg)

	doctorSection(w, "External tools")
	doctorCheckTools(w, cfg, look)

	doctorSection(w, "Data directory")
	doctorLine(w, doctorStatusOK, "Data directory", doctorDataDir())

	return cfg
}

// doctorCheckProvider performs the opt-in --check-provider smoke test: it
// resolves the active agent's model and provider, builds a one-off registry, and
// sends a single tiny request via smoke. Unlike the rest of doctor this does
// reach the network, which is why it only runs when the flag is set.
//
// Every failure is reported as an actionable WARN, never an error return, so the
// command exits cleanly and the user sees the exact next step (set a key, sign
// in, start the local server). The smoke func is injected so tests stay offline.
func doctorCheckProvider(ctx context.Context, w io.Writer, cfg *config.Config, smoke doctorSmokeFunc) {
	if cfg == nil {
		doctorLine(w, doctorStatusWarn, "Provider test", "config unavailable; cannot reach a provider")
		return
	}

	agent := doctorResolveAgent(cfg)
	if agent == nil || agent.Model == "" {
		doctorLine(w, doctorStatusWarn, "Provider test", "no active agent/model to test")
		return
	}

	model := doctorFindModel(cfg, agent.Model)
	if model == nil {
		doctorLine(w, doctorStatusWarn, "Provider test", fmt.Sprintf("model %q is not defined in config", agent.Model))
		return
	}

	registry, err := llm.NewRegistry(cfg)
	if err != nil {
		doctorLine(w, doctorStatusWarn, "Provider test", fmt.Sprintf("cannot build provider registry: %v", err))
		return
	}
	provider, err := registry.Get(model.Provider)
	if err != nil {
		// A disabled provider is dropped from the registry, so distinguish that
		// common, fixable case from a genuine lookup failure.
		if prov := doctorFindProvider(cfg, model.Provider); prov != nil && prov.Disabled {
			doctorLine(w, doctorStatusWarn, "Provider test", fmt.Sprintf("provider %q is disabled in config", model.Provider))
			return
		}
		doctorLine(w, doctorStatusWarn, "Provider test", fmt.Sprintf("provider %q unavailable: %v", model.Provider, err))
		return
	}

	result, err := smoke(ctx, provider, agent.Model, doctorProviderCheckTimeout)
	if err != nil {
		// The model id, not the provider name, is what the wire request carries;
		// pass it so the hint resolver can pick the right model-side fix.
		doctorLine(w, doctorStatusWarn, "Provider test", doctorSmokeHint(agent.Model, err))
		return
	}

	detail := fmt.Sprintf("model %q answered in %s", agent.Model, result.Latency.Round(time.Millisecond))
	if result.Reply != "" {
		detail += fmt.Sprintf(" (%q)", result.Reply)
	}
	doctorLine(w, doctorStatusOK, "Provider test", detail)
}

// doctorSmokeHint turns a smoke-check error into an actionable one-line message,
// mapping the llm sentinel errors to the concrete fix. Unmatched errors fall
// through to the wrapped message so nothing is swallowed.
func doctorSmokeHint(model string, err error) string {
	switch {
	case errors.Is(err, llm.ErrAuth):
		return fmt.Sprintf("authentication failed for model %q — check the provider's API key or sign-in", model)
	case errors.Is(err, llm.ErrModelNotFound):
		return fmt.Sprintf("provider rejected model %q — confirm the model id is correct and enabled for your account", model)
	case errors.Is(err, llm.ErrRateLimit):
		return fmt.Sprintf("provider rate-limited the test request for model %q — try again shortly", model)
	case errors.Is(err, llm.ErrServer):
		return fmt.Sprintf("provider unreachable or timed out for model %q — check connectivity and that the endpoint is up", model)
	default:
		return fmt.Sprintf("test request for model %q failed: %v", model, err)
	}
}

// doctorCheckActiveAgent reports the model, provider, and runtime usability of
// the default 'coder' agent: whether the provider that backs the agent can
// actually be used right now (env key set, local endpoint reachable, or ChatGPT
// auth present). When usability fails it prints a specific command hint rather
// than a bare warning, so the next step is obvious. Secrets are never read or
// printed — only set/unset and reachable/unreachable state.
func doctorCheckActiveAgent(w io.Writer, cfg *config.Config, dial func(string) bool) {
	if cfg == nil {
		doctorLine(w, doctorStatusWarn, "Active agent", "config unavailable; cannot resolve agent")
		return
	}

	agent := doctorResolveAgent(cfg)
	if agent == nil {
		doctorLine(w, doctorStatusWarn, "Active agent", "no agents configured")
		return
	}
	doctorLine(w, doctorStatusOK, "Active agent", agent.Name)

	if agent.Model == "" {
		doctorLine(w, doctorStatusWarn, "Active model", fmt.Sprintf("agent %q declares no model", agent.Name))
		return
	}
	doctorLine(w, doctorStatusOK, "Active model", agent.Model)

	model := doctorFindModel(cfg, agent.Model)
	if model == nil {
		doctorLine(w, doctorStatusWarn, "Active provider", fmt.Sprintf("model %q is not defined in config", agent.Model))
		return
	}

	provider := doctorFindProvider(cfg, model.Provider)
	if provider == nil {
		doctorLine(w, doctorStatusWarn, "Active provider", fmt.Sprintf("provider %q for model %q is not defined in config", model.Provider, agent.Model))
		return
	}
	doctorLine(w, doctorStatusOK, "Active provider", fmt.Sprintf("%s (%s)", provider.Name, provider.Type))

	status, detail := doctorProviderUsable(provider, dial)
	doctorLine(w, status, "Provider ready", detail)
}

// doctorResolveAgent returns the agent doctor reports on: the 'coder' agent
// when present (matching the run command's default), otherwise the first
// configured agent. It returns nil only when no agents are configured.
func doctorResolveAgent(cfg *config.Config) *config.Agent {
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == doctorActiveAgent {
			return &cfg.Agents[i]
		}
	}
	if len(cfg.Agents) > 0 {
		return &cfg.Agents[0]
	}
	return nil
}

// doctorFindModel returns the model with the given id, or nil.
func doctorFindModel(cfg *config.Config, id string) *config.Model {
	for i := range cfg.Models {
		if cfg.Models[i].ID == id {
			return &cfg.Models[i]
		}
	}
	return nil
}

// doctorFindProvider returns the provider with the given name, or nil.
func doctorFindProvider(cfg *config.Config, name string) *config.Provider {
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return &cfg.Providers[i]
		}
	}
	return nil
}

// doctorProviderUsable reports whether the active provider can be used now and a
// human-readable detail. The check is auth-shape specific: ChatGPT providers
// need a stored sign-in, local providers (Ollama/LM Studio, or any localhost
// base_url) need a reachable endpoint, and remote API providers need their
// api_key_env set. A failing check returns a WARN with the exact command or
// variable to fix it; secrets are never inspected, only presence.
func doctorProviderUsable(p *config.Provider, dial func(string) bool) (doctorStatus, string) {
	if p.Disabled {
		return doctorStatusWarn, "provider is disabled in config"
	}

	switch p.Type {
	case config.ProviderChatGPT, config.ProviderCodexOAuth:
		if _, err := llm.ChatGPTStatus(); err != nil {
			return doctorStatusWarn, "not signed in (run 'bharatcode auth chatgpt')"
		}
		return doctorStatusOK, "ChatGPT sign-in present"

	case config.ProviderOllama, config.ProviderLMStudio:
		return doctorLocalProviderUsable(p, dial)
	}

	// Any provider can point at a localhost endpoint via base_url; treat that
	// as a local provider regardless of its declared type so reachability,
	// not a (usually absent) key, is what gates readiness.
	if doctorIsLocalEndpoint(p.BaseURL) {
		return doctorLocalProviderUsable(p, dial)
	}

	if p.APIKeyEnv == "" {
		// A remote provider with no api_key_env names no secret to check; it
		// either needs none or relies on a header. Report it as usable rather
		// than inventing a hint we cannot give.
		return doctorStatusOK, "no API key required"
	}
	if _, ok := os.LookupEnv(p.APIKeyEnv); ok {
		return doctorStatusOK, p.APIKeyEnv + " is set"
	}
	return doctorStatusWarn, fmt.Sprintf("API key missing (set %s in your environment)", p.APIKeyEnv)
}

// doctorLocalProviderUsable probes whether a local provider's endpoint accepts
// connections. An unset base_url is reported as unknown rather than failed,
// since the provider's own default URL is resolved later in the registry.
func doctorLocalProviderUsable(p *config.Provider, dial func(string) bool) (doctorStatus, string) {
	addr := doctorDialAddress(p.BaseURL)
	if addr == "" {
		return doctorStatusWarn, "base_url not set; cannot probe local endpoint (start the server and set base_url)"
	}
	if dial == nil || !dial(addr) {
		return doctorStatusWarn, fmt.Sprintf("endpoint %s not reachable (is the local server running?)", addr)
	}
	return doctorStatusOK, "endpoint " + addr + " reachable"
}

// doctorIsLocalEndpoint reports whether rawURL targets the loopback host.
func doctorIsLocalEndpoint(rawURL string) bool {
	host := doctorURLHost(rawURL)
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	default:
		return false
	}
}

// doctorDialAddress derives a "host:port" suitable for net.Dial from a base
// URL, supplying the scheme's default port when the URL omits one. It returns
// "" when rawURL has no parseable host.
func doctorDialAddress(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// doctorURLHost returns the lowercased hostname of rawURL, or "".
func doctorURLHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
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
