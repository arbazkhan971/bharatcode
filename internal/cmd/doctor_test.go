package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
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
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load)
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
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load)
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
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load)
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

func TestRunDoctorIgnoresUnwiredDataDirOverride(t *testing.T) {
	// The app does not consume options.data_dir for path building, so
	// doctor must report the real XDG path even when the field is set.
	cfg := &config.Config{Options: config.Options{DataDir: "/should/not/appear"}}
	load := func(_ context.Context, _, _ string) (*config.Config, error) { return cfg, nil }
	look := func(string) (string, bool) { return "", false }

	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	var buf bytes.Buffer
	runDoctor(context.Background(), &buf, &rootOptions{}, look, load)
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
