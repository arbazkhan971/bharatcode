package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/stretchr/testify/require"
)

func TestDoctorRunsAndReportsSections(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())

	t.Setenv("DEEPSEEK_API_KEY", "present-but-never-printed")
	clearEnv(t, "MOONSHOT_API_KEY")

	stdout, stderr, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Empty(t, stderr)

	for _, header := range []string{
		"Runtime:",
		"Configuration:",
		"Provider API keys:",
		"External tools:",
		"Data directory:",
	} {
		require.Contains(t, stdout, header, "missing section header %q", header)
	}

	// Go runtime version is reported verbatim.
	require.Contains(t, stdout, runtime.Version())

	// A set key reports OK/set; an unset key reports WARN/not set. The
	// secret value must never appear in the output.
	require.Regexp(t, `\[OK\s*\] DEEPSEEK_API_KEY: set`, stdout)
	require.Regexp(t, `\[WARN\s*\] MOONSHOT_API_KEY: not set`, stdout)
	require.NotContains(t, stdout, "present-but-never-printed")
}

func TestDoctorReportsConfiguredProviderEnvVar(t *testing.T) {
	// A provider-declared api_key_env not in the built-in known set is
	// still reported, proving doctor reads the loaded config.
	body := `{
  "providers": [
    {
      "name": "custom",
      "type": "openai_compatible",
      "base_url": "https://example.test/v1",
      "api_key_env": "CUSTOM_PROVIDER_KEY",
      "models": ["m1"]
    }
  ],
  "models": [
    {
      "id": "m1",
      "provider": "custom",
      "context_window": 8000,
      "input_price_per_mtok_usd": 1.0,
      "output_price_per_mtok_usd": 2.0,
      "supports_tools": true
    }
  ],
  "agents": [
    {"name": "coder", "model": "m1", "system_prompt": "You are concise."}
  ],
  "ledger": {"currency": "INR", "usd_inr_rate": 83.5}
}`
	configPath := writeConfig(t, body)
	clearEnv(t, "CUSTOM_PROVIDER_KEY")

	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Regexp(t, `\[WARN\s*\] CUSTOM_PROVIDER_KEY: not set`, stdout)
	require.Contains(t, stdout, "Config valid")
}

func TestDoctorReportsChatGPTSubscriptionStatus(t *testing.T) {
	configPath := writeConfig(t, chatgptDoctorConfig())

	authPath := filepath.Join(t.TempDir(), "chatgpt_auth.json")
	t.Setenv("BHARATCODE_CHATGPT_AUTH", authPath)
	require.NoError(t, os.WriteFile(authPath, []byte(`{
  "auth_mode": "chatgpt",
  "tokens": {
    "access_token": "access-token",
    "account_id": "acct-123"
  },
  "account": {
    "email": "dev@example.com",
    "plan": "plus"
  }
}`), 0o600))

	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Regexp(t, `\[OK\s*\] ChatGPT subscription: signed in as dev@example.com on the plus plan`, stdout)
}

func TestDoctorWarnsWhenChatGPTSubscriptionMissing(t *testing.T) {
	configPath := writeConfig(t, chatgptDoctorConfig())
	t.Setenv("BHARATCODE_CHATGPT_AUTH", filepath.Join(t.TempDir(), "missing.json"))

	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Regexp(t, `\[WARN\s*\] ChatGPT subscription: not signed in`, stdout)
}

func TestDoctorWarnsWhenConfigMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	stdout, _, err := executeDoctor(t, "--config", missing, "doctor")
	require.NoError(t, err)
	require.Contains(t, stdout, "not found")
	// Built-in defaults still load and validate, so the report continues.
	require.Contains(t, stdout, "Config valid")
}

func TestRunDoctorToolsAndDataDir(t *testing.T) {
	// Drive runDoctor directly with stubs so PATH/tool checks are
	// deterministic and offline.
	cfg := &config.Config{
		LSP: []config.LSPServer{
			{Name: "gopls", Command: "gopls", Languages: []string{"go"}},
			{Name: "off", Command: "pyright", Languages: []string{"python"}, Disabled: true},
		},
	}
	look := func(name string) (string, bool) {
		switch name {
		case "rg":
			return "/usr/bin/rg", true
		case "gopls":
			return "/usr/local/bin/gopls", true
		default:
			return "", false
		}
	}
	load := func(_ context.Context, _, _ string) (*config.Config, error) {
		return cfg, nil
	}

	// Pin the data home so the reported directory matches the app's
	// XDG-based resolution exactly.
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	var buf bytes.Buffer
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load, doctorDialUnreachable)
	out := buf.String()

	require.Contains(t, out, "ripgrep (rg): /usr/bin/rg")
	require.Regexp(t, `\[OK\s*\] LSP gopls \(gopls\): /usr/local/bin/gopls`, out)
	// Disabled LSP server is skipped entirely.
	require.NotContains(t, out, "pyright")
	// Data directory mirrors the app's resolution (XDG_DATA_HOME/bharatcode),
	// not the unwired options.data_dir config field.
	require.Contains(t, out, "Data directory: "+filepath.Join(dataHome, "bharatcode"))
}

func TestRunDoctorMissingRgWarns(t *testing.T) {
	look := func(string) (string, bool) { return "", false }
	load := func(_ context.Context, _, _ string) (*config.Config, error) {
		return &config.Config{}, nil
	}
	var buf bytes.Buffer
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load, doctorDialUnreachable)
	out := buf.String()
	require.Regexp(t, `\[WARN\s*\] ripgrep \(rg\): not found on PATH`, out)
	require.Contains(t, out, "none configured")
}

func TestRunDoctorConfigLoadFailureIsNonFatal(t *testing.T) {
	load := func(_ context.Context, _, _ string) (*config.Config, error) {
		return nil, context.DeadlineExceeded
	}
	look := func(string) (string, bool) { return "", false }
	var buf bytes.Buffer
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load, doctorDialUnreachable)
	out := buf.String()
	// Config validity is a WARN, but later sections still render.
	require.Regexp(t, `\[WARN\s*\] Config valid:`, out)
	require.Contains(t, out, "Provider API keys:")
	require.Contains(t, out, "Data directory:")
	require.Contains(t, out, "config unavailable")
}

// executeDoctor runs args against a root command that has the doctor
// subcommand registered. It mirrors the production root wiring (the
// one-line followup) so the test exercises real flag parsing and
// rootOptions propagation rather than calling runDoctor directly.
func executeDoctor(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	root.AddCommand(NewDoctorCmd())
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := executeCommand(context.Background(), root)
	return stdout.String(), stderr.String(), err
}

func chatgptDoctorConfig() string {
	return `{
  "providers": [
    {
      "name": "chatgpt",
      "type": "chatgpt",
      "models": ["gpt-5.1-codex"]
    }
  ],
  "models": [
    {
      "id": "gpt-5.1-codex",
      "provider": "chatgpt",
      "context_window": 128000,
      "input_price_per_mtok_usd": 0,
      "output_price_per_mtok_usd": 0,
      "supports_tools": true
    }
  ],
  "agents": [
    {
      "name": "coder",
      "model": "gpt-5.1-codex",
      "system_prompt": "You are concise."
    }
  ],
  "ledger": {
    "currency": "INR",
    "usd_inr_rate": 83.5
  }
}`
}

// doctorDialUnreachable is a dial stub that reports every endpoint as down. It
// keeps the existing checklist tests offline and deterministic; those configs
// declare no local provider, so the result is never inspected.
func doctorDialUnreachable(string) bool { return false }

func TestDoctorReportsActiveAgentForChatGPT(t *testing.T) {
	// A signed-in ChatGPT subscription makes the chatgpt-backed coder agent
	// usable: the active agent/model/provider are reported and the provider is
	// ready.
	configPath := writeConfig(t, chatgptDoctorConfig())

	authPath := filepath.Join(t.TempDir(), "chatgpt_auth.json")
	t.Setenv("BHARATCODE_CHATGPT_AUTH", authPath)
	require.NoError(t, os.WriteFile(authPath, []byte(`{
  "auth_mode": "chatgpt",
  "tokens": {"access_token": "access-token", "account_id": "acct-123"},
  "account": {"email": "dev@example.com", "plan": "plus"}
}`), 0o600))

	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Contains(t, stdout, "Active agent:")
	require.Regexp(t, `\[OK\s*\] Active agent: coder`, stdout)
	require.Regexp(t, `\[OK\s*\] Active model: gpt-5\.1-codex`, stdout)
	require.Regexp(t, `\[OK\s*\] Active provider: chatgpt \(chatgpt\)`, stdout)
	require.Regexp(t, `\[OK\s*\] Provider ready: ChatGPT sign-in present`, stdout)
}

func TestDoctorActiveAgentWarnsWhenChatGPTAuthMissing(t *testing.T) {
	// Without a stored sign-in the chatgpt provider is not ready, and doctor
	// emits the specific auth command hint.
	configPath := writeConfig(t, chatgptDoctorConfig())
	t.Setenv("BHARATCODE_CHATGPT_AUTH", filepath.Join(t.TempDir(), "missing.json"))

	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.Regexp(t, `\[OK\s*\] Active provider: chatgpt \(chatgpt\)`, stdout)
	require.Regexp(t, `\[WARN\s*\] Provider ready: not signed in \(run 'bharatcode auth chatgpt'\)`, stdout)
}

func TestDoctorActiveAgentEnvKeySetAndMissing(t *testing.T) {
	cfg := envKeyDoctorConfig()

	// Env key set -> provider ready.
	t.Run("set", func(t *testing.T) {
		t.Setenv("CODER_PROVIDER_KEY", "present-but-never-printed")
		var buf bytes.Buffer
		load := func(_ context.Context, _, _ string) (*config.Config, error) { return cfg, nil }
		runDoctor(context.Background(), &buf, &rootOptions{}, doctorLookNothing, load, doctorDialUnreachable)
		out := buf.String()
		require.Regexp(t, `\[OK\s*\] Active model: coder-model`, out)
		require.Regexp(t, `\[OK\s*\] Active provider: coder-remote \(openai_compatible\)`, out)
		require.Regexp(t, `\[OK\s*\] Provider ready: CODER_PROVIDER_KEY is set`, out)
		require.NotContains(t, out, "present-but-never-printed")
	})

	// Env key missing -> specific hint naming the variable.
	t.Run("missing", func(t *testing.T) {
		clearEnv(t, "CODER_PROVIDER_KEY")
		var buf bytes.Buffer
		load := func(_ context.Context, _, _ string) (*config.Config, error) { return cfg, nil }
		runDoctor(context.Background(), &buf, &rootOptions{}, doctorLookNothing, load, doctorDialUnreachable)
		out := buf.String()
		require.Regexp(t, `\[WARN\s*\] Provider ready: API key missing \(set CODER_PROVIDER_KEY in your environment\)`, out)
	})
}

func TestDoctorActiveAgentLocalProviderReachability(t *testing.T) {
	cfg := localProviderDoctorConfig()
	load := func(_ context.Context, _, _ string) (*config.Config, error) { return cfg, nil }

	// Reachable local endpoint -> ready, no key required.
	t.Run("reachable", func(t *testing.T) {
		dialed := ""
		dial := func(addr string) bool { dialed = addr; return true }
		var buf bytes.Buffer
		runDoctor(context.Background(), &buf, &rootOptions{}, doctorLookNothing, load, dial)
		out := buf.String()
		require.Equal(t, "127.0.0.1:11434", dialed)
		require.Regexp(t, `\[OK\s*\] Active provider: local \(ollama\)`, out)
		require.Regexp(t, `\[OK\s*\] Provider ready: endpoint 127\.0\.0\.1:11434 reachable`, out)
	})

	// Unreachable local endpoint -> warn telling the user to start the server.
	t.Run("unreachable", func(t *testing.T) {
		var buf bytes.Buffer
		runDoctor(context.Background(), &buf, &rootOptions{}, doctorLookNothing, load, doctorDialUnreachable)
		out := buf.String()
		require.Regexp(t, `\[WARN\s*\] Provider ready: endpoint 127\.0\.0\.1:11434 not reachable \(is the local server running\?\)`, out)
	})
}

// doctorLookNothing is a binary-lookup stub (rg/LSP) that reports nothing
// on PATH; it stands in for the unused tool lookup in active-agent tests.
func doctorLookNothing(string) (string, bool) { return "", false }

// envKeyDoctorConfig backs a coder agent with a remote openai_compatible
// provider that authenticates via the CODER_PROVIDER_KEY env var.
func envKeyDoctorConfig() *config.Config {
	return &config.Config{
		Providers: []config.Provider{{
			Name:      "coder-remote",
			Type:      config.ProviderOpenAICompatible,
			BaseURL:   "https://api.example.test/v1",
			APIKeyEnv: "CODER_PROVIDER_KEY",
			Models:    []string{"coder-model"},
		}},
		Models: []config.Model{{
			ID:            "coder-model",
			Provider:      "coder-remote",
			ContextWindow: 8000,
			SupportsTools: true,
		}},
		Agents: []config.Agent{{Name: "coder", Model: "coder-model", SystemPrompt: "concise"}},
	}
}

// localProviderDoctorConfig backs a coder agent with a localhost Ollama
// provider whose readiness is gated on endpoint reachability, not a key.
func localProviderDoctorConfig() *config.Config {
	return &config.Config{
		Providers: []config.Provider{{
			Name:    "local",
			Type:    config.ProviderOllama,
			BaseURL: "http://127.0.0.1:11434",
			Models:  []string{"coder-model"},
		}},
		Models: []config.Model{{
			ID:            "coder-model",
			Provider:      "local",
			ContextWindow: 8000,
			SupportsTools: true,
		}},
		Agents: []config.Agent{{Name: "coder", Model: "coder-model", SystemPrompt: "concise"}},
	}
}

func TestRunDoctorIgnoresUnwiredDataDirOverride(t *testing.T) {
	// The app does not consume options.data_dir for path building, so
	// doctor must report the real XDG path even when the field is set.
	cfg := &config.Config{Options: config.Options{DataDir: "/should/not/appear"}}
	load := func(_ context.Context, _, _ string) (*config.Config, error) { return cfg, nil }
	look := func(string) (string, bool) { return "", false }

	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	var buf bytes.Buffer
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load, doctorDialUnreachable)
	out := buf.String()

	require.NotContains(t, out, "/should/not/appear")
	require.Contains(t, out, "Data directory: "+filepath.Join(dataHome, "bharatcode"))
}

func TestDoctorWarnsOnMalformedConfigFile(t *testing.T) {
	// A real malformed config file exercises the genuine load/validate
	// failure path end-to-end through the command, not a stubbed error.
	path := writeConfig(t, "{ this is not valid json")
	stdout, _, err := executeDoctor(t, "--config", path, "doctor")
	require.NoError(t, err) // checklist command never errors on a WARN
	require.Regexp(t, `\[WARN\s*\] Config valid:`, stdout)
	// Later sections still render after the config WARN.
	require.Contains(t, stdout, "Provider API keys:")
	require.Contains(t, stdout, "Data directory:")
}

func TestDoctorDefaultMakesNoProviderCall(t *testing.T) {
	// The default checklist must stay offline-fast: it carries no provider
	// smoke-check section and produces no "Provider test" line. The probe is
	// opt-in, so running plain `doctor` never reaches a model.
	configPath := writeConfig(t, envKeyDoctorConfigJSON())
	stdout, _, err := executeDoctor(t, "--config", configPath, "doctor")
	require.NoError(t, err)
	require.NotContains(t, stdout, "Provider smoke check:")
	require.NotContains(t, stdout, "Provider test:")
}

func TestDoctorCheckProviderReportsAnswer(t *testing.T) {
	// A provider that answers makes the smoke check pass, reporting the model,
	// latency, and a short reply preview. The probe must receive the model id
	// (what the wire request carries), not the provider name.
	cfg := envKeyDoctorConfig()
	var gotModel string
	smoke := func(_ context.Context, _ llm.Provider, model string, _ time.Duration) (llm.SmokeResult, error) {
		gotModel = model
		return llm.SmokeResult{OK: true, Reply: "ok", Latency: 42 * time.Millisecond}, nil
	}
	t.Setenv("CODER_PROVIDER_KEY", "present-but-never-printed")

	var buf bytes.Buffer
	doctorCheckProvider(context.Background(), &buf, cfg, smoke)
	out := buf.String()
	require.Equal(t, "coder-model", gotModel, "smoke must be probed with the model id, not the provider name")
	require.Regexp(t, `\[OK\s*\] Provider test: model "coder-model" answered in 42ms \("ok"\)`, out)
	require.NotContains(t, out, "present-but-never-printed")
}

func TestDoctorCheckProviderAuthFailureIsActionable(t *testing.T) {
	// An auth failure maps to a specific, fixable hint rather than a raw error.
	cfg := envKeyDoctorConfig()
	smoke := func(_ context.Context, _ llm.Provider, _ string, _ time.Duration) (llm.SmokeResult, error) {
		return llm.SmokeResult{}, fmt.Errorf("smoke check: %w", llm.ErrAuth)
	}
	t.Setenv("CODER_PROVIDER_KEY", "present")

	var buf bytes.Buffer
	doctorCheckProvider(context.Background(), &buf, cfg, smoke)
	out := buf.String()
	require.Regexp(t, `\[WARN\s*\] Provider test: authentication failed for model "coder-model"`, out)
}

func TestDoctorCheckProviderWarnsOnDisabledProvider(t *testing.T) {
	// A disabled provider is dropped from the registry; the smoke check names the
	// real cause instead of a generic lookup failure, and never calls smoke.
	cfg := envKeyDoctorConfig()
	cfg.Providers[0].Disabled = true
	called := false
	smoke := func(_ context.Context, _ llm.Provider, _ string, _ time.Duration) (llm.SmokeResult, error) {
		called = true
		return llm.SmokeResult{}, nil
	}

	var buf bytes.Buffer
	doctorCheckProvider(context.Background(), &buf, cfg, smoke)
	out := buf.String()
	require.Regexp(t, `\[WARN\s*\] Provider test: provider "coder-remote" is disabled in config`, out)
	require.False(t, called, "smoke must not run for a disabled provider")
}

func TestDoctorCheckProviderWarnsWhenConfigNil(t *testing.T) {
	var buf bytes.Buffer
	doctorCheckProvider(context.Background(), &buf, nil, func(context.Context, llm.Provider, string, time.Duration) (llm.SmokeResult, error) {
		t.Fatal("smoke must not be called when config is nil")
		return llm.SmokeResult{}, nil
	})
	require.Regexp(t, `\[WARN\s*\] Provider test: config unavailable`, buf.String())
}

// envKeyDoctorConfigJSON is the on-disk equivalent of envKeyDoctorConfig, used
// by the end-to-end flag test that drives the real command.
func envKeyDoctorConfigJSON() string {
	return `{
  "providers": [
    {
      "name": "coder-remote",
      "type": "openai_compatible",
      "base_url": "https://api.example.test/v1",
      "api_key_env": "CODER_PROVIDER_KEY",
      "models": ["coder-model"]
    }
  ],
  "models": [
    {
      "id": "coder-model",
      "provider": "coder-remote",
      "context_window": 8000,
      "supports_tools": true
    }
  ],
  "agents": [
    {"name": "coder", "model": "coder-model", "system_prompt": "You are concise."}
  ],
  "ledger": {"currency": "INR", "usd_inr_rate": 83.5}
}`
}

// clearEnv guarantees name is unset for the duration of the test and
// restores any prior value afterward. t.Setenv only blanks the value,
// which LookupEnv still reports as set; doctor distinguishes set from
// unset, so the variable must be genuinely removed.
func clearEnv(t *testing.T, name string) {
	t.Helper()
	prior, had := os.LookupEnv(name)
	require.NoError(t, os.Unsetenv(name))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, prior)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}
