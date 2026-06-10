package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readDefaultModel reads the persisted config file and returns the model ID
// recorded on the "coder" agent, which is the convention the agent loop
// resolves as the default model.
func readDefaultModel(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg struct {
		Agents []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	for _, a := range cfg.Agents {
		if a.Name == "coder" {
			return a.Model
		}
	}
	t.Fatalf("no coder agent found in %s", configPath)
	return ""
}

// twoModelTestConfig has two models so the picker has a non-trivial choice
// and the chosen default differs from the starting default.
func twoModelTestConfig() string {
	return `{
  "providers": [
    {
      "name": "deepseek",
      "type": "openai_compatible",
      "base_url": "https://api.deepseek.com/v1",
      "api_key_env": "DEEPSEEK_API_KEY",
      "models": ["deepseek-chat", "deepseek-reasoner"]
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
    },
    {
      "id": "deepseek-reasoner",
      "provider": "deepseek",
      "context_window": 64000,
      "input_price_per_mtok_usd": 0.55,
      "output_price_per_mtok_usd": 2.19,
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

// TestModelsListShowsCompatContextOverride verifies the listing shows the
// EFFECTIVE context window — a compat.context_window override must replace the
// catalog value, mirroring the registry's resolution, so a user checking their
// override sees it applied.
func TestModelsListShowsCompatContextOverride(t *testing.T) {
	cfg := strings.Replace(twoModelTestConfig(),
		`      "id": "deepseek-chat",
      "provider": "deepseek",
      "context_window": 64000,`,
		`      "id": "deepseek-chat",
      "provider": "deepseek",
      "context_window": 64000,
      "compat": {"context_window": 99000},`,
		1)
	require.Contains(t, cfg, "99000", "test premise: compat block injected")
	configPath := writeConfig(t, cfg)

	stdout, stderr, err := executeRoot(t, "--config", configPath, "models")
	require.NoError(t, err)
	require.Empty(t, stderr)

	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "deepseek-chat") {
			require.Contains(t, line, "99000", "compat override must be the listed context window")
			require.NotContains(t, line, "64000")
		}
		if strings.Contains(line, "deepseek-reasoner") {
			require.Contains(t, line, "64000", "model without compat keeps its catalog window")
		}
	}
}

func TestModelsPickPersistsSelection(t *testing.T) {
	configPath := writeConfig(t, twoModelTestConfig())
	require.Equal(t, "deepseek-chat", readDefaultModel(t, configPath))

	oldSelect := selectModel
	defer func() { selectModel = oldSelect }()

	var offered []string
	selectModel = func(ids []string) (string, error) {
		offered = ids
		return "deepseek-reasoner", nil
	}

	stdout, stderr, err := executeRoot(t, "--config", configPath, "models", "--pick")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Default model set to deepseek-reasoner\n", stdout)

	// The selector was offered the sorted set of configured model IDs.
	require.Equal(t, []string{"deepseek-chat", "deepseek-reasoner"}, offered)

	// The chosen model is now the persisted default.
	require.Equal(t, "deepseek-reasoner", readDefaultModel(t, configPath))
}

func TestModelsModelFlagPersistsNonInteractive(t *testing.T) {
	configPath := writeConfig(t, twoModelTestConfig())

	// If --model bypassed the selector correctly, this stub must never run.
	oldSelect := selectModel
	defer func() { selectModel = oldSelect }()
	selectModel = func(ids []string) (string, error) {
		t.Fatalf("selector should not be called for --model; got %v", ids)
		return "", nil
	}

	stdout, stderr, err := executeRoot(t, "--config", configPath, "models", "--model", "deepseek-reasoner")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Default model set to deepseek-reasoner\n", stdout)

	require.Equal(t, "deepseek-reasoner", readDefaultModel(t, configPath))
}

func TestModelsModelFlagRejectsUnknownModel(t *testing.T) {
	configPath := writeConfig(t, twoModelTestConfig())

	stdout, stderr, err := executeRoot(t, "--config", configPath, "models", "--model", "gpt-nonexistent")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "Error: model gpt-nonexistent not found")

	// The original default is untouched.
	require.Equal(t, "deepseek-chat", readDefaultModel(t, configPath))
}

func TestModelsPickRejectsUnknownSelection(t *testing.T) {
	configPath := writeConfig(t, twoModelTestConfig())

	oldSelect := selectModel
	defer func() { selectModel = oldSelect }()
	selectModel = func(ids []string) (string, error) {
		return "ghost-model", nil
	}

	stdout, stderr, err := executeRoot(t, "--config", configPath, "models", "--pick")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "Error: model ghost-model not found")
	require.Equal(t, "deepseek-chat", readDefaultModel(t, configPath))
}

// TestDefaultModelPickerParsesNumberedChoice exercises the real, non-stubbed
// selector body to prove the default implementation maps a 1-based numeric
// line to the right model ID without needing a TTY.
func TestDefaultModelPickerParsesNumberedChoice(t *testing.T) {
	var out strings.Builder
	chosen, err := promptModelChoice(strings.NewReader("2\n"), &out, []string{"deepseek-chat", "deepseek-reasoner"})
	require.NoError(t, err)
	require.Equal(t, "deepseek-reasoner", chosen)
	require.Contains(t, out.String(), "1) deepseek-chat")
	require.Contains(t, out.String(), "2) deepseek-reasoner")
	require.Contains(t, out.String(), "Select model:")
}

func TestDefaultModelPickerRejectsOutOfRange(t *testing.T) {
	var out strings.Builder
	_, err := promptModelChoice(strings.NewReader("9\n"), &out, []string{"deepseek-chat", "deepseek-reasoner"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid selection "9"`)
}
