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

// scriptedPrompter returns a Prompter that hands back the queued
// answers in order, so init can run with no TTY. It records every
// question for assertions and fails the test if asked more questions
// than answers were scripted.
func scriptedPrompter(t *testing.T, answers ...string) (Prompter, *[]string) {
	t.Helper()
	var asked []string
	i := 0
	return func(question string) (string, error) {
		asked = append(asked, question)
		if i >= len(answers) {
			t.Fatalf("unexpected prompt %q: no scripted answer left", question)
		}
		a := answers[i]
		i++
		return a, nil
	}, &asked
}

// runInitCmd drives runInit directly with an injected prompter, mirroring
// how RunE wires it up but without needing a real stdin.
func runInitCmd(t *testing.T, opts *rootOptions, project bool, prompt Prompter) (string, error) {
	t.Helper()
	cmd := newInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	err := runInit(cmd, opts, project, prompt)
	return out.String(), err
}

// TestInitWritesValidConfigWithChosenDefault is the core behavior test:
// running init in a clean temp dir writes a parseable, valid config whose
// default agent points at a model belonging to the chosen provider.
func TestInitWritesValidConfigWithChosenDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	// Pick "openai" by name; its first model is gpt-4o.
	prompt, asked := scriptedPrompter(t, "openai")
	out, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	require.FileExists(t, path)

	// The file parses and validates through the real loader.
	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))

	// The default agent was pointed at the chosen provider's model.
	require.NotEmpty(t, cfg.Agents)
	require.Equal(t, "gpt-4o", cfg.Agents[0].Model)

	// The chosen provider exists in the scaffolded provider list.
	var found bool
	for _, p := range cfg.Providers {
		if p.Name == "openai" {
			found = true
		}
	}
	require.True(t, found, "scaffolded config must keep the full provider list")

	// The user was prompted to choose a provider, and reminded of the key.
	require.Len(t, *asked, 1)
	require.Contains(t, (*asked)[0], "Choose a default provider")
	require.Contains(t, out, "OPENAI_API_KEY")
	require.Contains(t, out, path)
}

// TestInitEmbedsCommentedGuidanceAndStaysLoadable proves the scaffolded
// file carries human-readable commented guidance (as _comment keys, since
// JSON has no comment syntax) AND still parses and validates through the
// real loader. The round-trip is the discriminating check: if the
// guidance keys broke decoding, LoadFrom would fail here.
func TestInitEmbedsCommentedGuidanceAndStaysLoadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	prompt, _ := scriptedPrompter(t, "openai")
	_, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(raw)
	require.Contains(t, text, "_comment", "starter config must carry commented guidance")
	require.Contains(t, text, "OPENAI_API_KEY", "guidance must name the API-key env var")
	require.Contains(t, text, "agents[0].model", "guidance must explain how to switch the default")

	// The guidance keys do not break the loader: the file still loads
	// and validates.
	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))
	require.Equal(t, "gpt-4o", cfg.Agents[0].Model)
}

// TestInitDefaultProviderOnBlankAnswer proves a bare Enter selects the
// first provider rather than erroring.
func TestInitDefaultProviderOnBlankAnswer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	prompt, _ := scriptedPrompter(t, "")
	_, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	// First provider in defaults is anthropic -> claude-sonnet-4-5.
	require.Equal(t, "claude-sonnet-4-5", cfg.Agents[0].Model)
}

// TestInitSelectsProviderByIndex proves a numeric answer selects by
// 1-based position.
func TestInitSelectsProviderByIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	// Position 2 is openai in the embedded defaults.
	prompt, _ := scriptedPrompter(t, "2")
	_, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", cfg.Agents[0].Model)
}

// TestInitDoesNotClobberWithoutConfirmation proves an existing config is
// preserved byte-for-byte when the user declines, and the starter is
// diverted to a sibling .example file instead.
func TestInitDoesNotClobberWithoutConfirmation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	original := []byte(defaultTestConfig())
	require.NoError(t, os.WriteFile(path, original, 0o600))

	// Provider answer, then decline the overwrite confirmation.
	prompt, asked := scriptedPrompter(t, "deepseek", "n")
	out, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	// The original config is untouched.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after, "existing config must not be clobbered")

	// The starter landed in the .example sibling and is valid.
	examplePath := path + ".example"
	require.FileExists(t, examplePath)
	cfg, err := config.LoadFrom(context.Background(), examplePath, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))

	// The user was asked to confirm the overwrite.
	require.Len(t, *asked, 2)
	require.Contains(t, (*asked)[1], "Overwrite?")
	require.Contains(t, out, examplePath)
}

// TestInitOverwritesOnConfirmation proves an explicit yes replaces the
// existing config in place (no .example sibling).
func TestInitOverwritesOnConfirmation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	require.NoError(t, os.WriteFile(path, []byte(defaultTestConfig()), 0o600))

	prompt, _ := scriptedPrompter(t, "openai", "y")
	_, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	// No .example sibling: the overwrite happened in place.
	require.NoFileExists(t, path+".example")

	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))
	require.Equal(t, "gpt-4o", cfg.Agents[0].Model)
}

// TestInitProjectFlagWritesProjectFile proves --project targets a
// .bharatcode.json under the project dir rather than the global config.
func TestInitProjectFlagWritesProjectFile(t *testing.T) {
	dir := t.TempDir()
	opts := &rootOptions{projectDir: dir}

	prompt, _ := scriptedPrompter(t, "openai")
	_, err := runInitCmd(t, opts, true, prompt)
	require.NoError(t, err)

	projPath := filepath.Join(dir, ".bharatcode.json")
	require.FileExists(t, projPath)

	cfg, err := config.LoadFrom(context.Background(), "", projPath)
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))
	require.Equal(t, "gpt-4o", cfg.Agents[0].Model)
}

// TestInitWritesGlobalPathByDefault proves the true first-run path: with
// no --config and no --project, init targets the global config under
// XDG_CONFIG_HOME. Pointing XDG_CONFIG_HOME at a temp dir keeps the test
// hermetic and never touches the developer's real config.
func TestInitWritesGlobalPathByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("global path is derived from APPDATA on Windows, not XDG_CONFIG_HOME")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	opts := &rootOptions{} // no configPath, no projectDir
	prompt, _ := scriptedPrompter(t, "openai")
	_, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)

	globalPath := filepath.Join(dir, "bharatcode", "config.json")
	require.FileExists(t, globalPath)
	require.Equal(t, config.GlobalPath(), globalPath,
		"init must write exactly the resolved global config path")

	cfg, err := config.LoadFrom(context.Background(), globalPath, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))
}

// TestInitRejectsUnknownProvider proves an out-of-range or unknown
// provider answer is an error rather than silently writing a broken
// config.
func TestInitRejectsUnknownProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	prompt, _ := scriptedPrompter(t, "no-such-provider")
	_, err := runInitCmd(t, opts, false, prompt)
	require.Error(t, err)
	require.NoFileExists(t, path, "no config should be written on a bad provider choice")
}

// TestInitLocalProviderNeedsNoKey proves a local provider (ollama)
// produces guidance that it needs no API key.
func TestInitLocalProviderNeedsNoKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	opts := &rootOptions{configPath: path}

	prompt, _ := scriptedPrompter(t, "ollama")
	out, err := runInitCmd(t, opts, false, prompt)
	require.NoError(t, err)
	require.Contains(t, out, "needs no API key")

	cfg, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err)
	require.NoError(t, config.Validate(cfg))
	require.Equal(t, "qwen2.5-coder:32b", cfg.Agents[0].Model)
}
