package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRootNoArgsStartsTUI(t *testing.T) {
	oldNewApp := newApp
	oldRunTUI := runTUI
	defer func() {
		newApp = oldNewApp
		runTUI = oldRunTUI
	}()

	var called bool
	newApp = func(ctx context.Context, opts app.Options) (*app.App, error) {
		_ = ctx
		require.True(t, opts.YOLO)
		return nil, nil
	}
	runTUI = func(ctx context.Context, application *app.App) error {
		_ = ctx
		_ = application
		called = true
		return nil
	}

	stdout, stderr, err := executeRoot(t, "--yolo")
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
	require.True(t, called)
}

func TestHelpTextListsSubcommands(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "--help")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Usage:")
	for _, name := range []string{"run", "login", "logout", "models", "sessions", "stats", "budget", "update-providers", "config", "version"} {
		require.Contains(t, stdout, name)
	}
}

func TestVersionFormat(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "version")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Regexp(t, regexp.MustCompile(`^bharatcode v\d+\.\d+\.\d+(-[a-z0-9.-]+)? \([0-9a-f]{7,}\)\n$`), stdout)
}

func TestModelsTable(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "models")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "PROVIDER")
	require.Contains(t, stdout, "deepseek-chat")
	require.Contains(t, stdout, "INPUT$/MTOK")
	require.Contains(t, stdout, "  ")
}

func TestStatsEmpty(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	stdout, stderr, err := executeRoot(t, "--config", configPath, "--project-dir", t.TempDir(), "stats")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "no usage recorded\n", stdout)
}

func TestBudgetSetPersists(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "budget", "set", "--month", "500")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Monthly budget set to ₹500\n", stdout)

	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	ledger, ok := cfg["ledger"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(500), ledger["max_inr_per_month"])
}

func TestLoginLogoutFakeKeyring(t *testing.T) {
	oldKeyring := keyring
	fake := newFakeKeyring()
	keyring = fake
	defer func() { keyring = oldKeyring }()

	stdout, stderr, err := executeRoot(t, "login", "openai", "--token", "secret")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Logged in to openai\n", stdout)
	require.Equal(t, "secret", fake.values["bharatcode/openai"])

	stdout, stderr, err = executeRoot(t, "logout", "openai")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Logged out of openai\n", stdout)
	require.NotContains(t, fake.values, "bharatcode/openai")
}

func TestLoginKeyringUnavailable(t *testing.T) {
	oldKeyring := keyring
	keyring = failingKeyring{err: errors.New("boom")}
	defer func() { keyring = oldKeyring }()

	stdout, stderr, err := executeRoot(t, "login", "openai", "--token", "secret")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "Error: keyring unavailable: boom")
}

func TestSessionShowNotFound(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "--project-dir", t.TempDir(), "sessions", "show", "bogus-id")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "Error: session bogus-id not found")
}

func TestSessionsListEmpty(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "--project-dir", t.TempDir(), "sessions", "list")
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Equal(t, "no sessions\n", stderr)
}

func TestRunStdinPromptValidation(t *testing.T) {
	cmd := newRunCmd()
	cmd.SetIn(strings.NewReader("hello\n"))
	prompt, err := readPrompt(cmd, nil)
	require.NoError(t, err)
	require.Equal(t, "hello", prompt)
}

func executeRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := executeCommand(context.Background(), root)
	return stdout.String(), stderr.String(), err
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func defaultTestConfig() string {
	return `{
  "providers": [
    {
      "name": "deepseek",
      "type": "openai_compatible",
      "base_url": "https://api.deepseek.com/v1",
      "api_key_env": "DEEPSEEK_API_KEY",
      "models": ["deepseek-chat"]
    }
  ],
  "models": [
    {
      "id": "deepseek-chat",
      "provider": "deepseek",
      "context_window": 64000,
      "input_price_per_mtok_usd": 0.27,
      "output_price_per_mtok_usd": 1.10,
      "supports_images": false,
      "supports_tools": true
    }
  ],
  "agents": [
    {
      "name": "coder",
      "model": "deepseek-chat",
      "system_prompt": "You are concise."
    }
  ],
  "ledger": {
    "currency": "INR",
    "usd_inr_rate": 83.5
  }
}`
}

type fakeKeyring struct {
	values map[string]string
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: make(map[string]string)}
}

func (k *fakeKeyring) Get(service, account string) (string, error) {
	value, ok := k.values[service+"/"+account]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func (k *fakeKeyring) Set(service, account, secret string) error {
	k.values[service+"/"+account] = secret
	return nil
}

func (k *fakeKeyring) Delete(service, account string) error {
	delete(k.values, service+"/"+account)
	return nil
}

type failingKeyring struct {
	err error
}

func (k failingKeyring) Get(service, account string) (string, error) {
	_ = service
	_ = account
	return "", k.err
}

func (k failingKeyring) Set(service, account, secret string) error {
	_ = service
	_ = account
	_ = secret
	return k.err
}

func (k failingKeyring) Delete(service, account string) error {
	_ = service
	_ = account
	return k.err
}

var _ = cobra.Command{}
