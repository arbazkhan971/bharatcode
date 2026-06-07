package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// memKeyring is an in-memory keyring double satisfying both the reader and
// writer seams in internal/llm, so onboarding tests can store and resolve keys
// without touching the OS keyring.
type memKeyring struct {
	values map[string]string // account -> secret
}

func (k *memKeyring) Get(_, account string) (string, error) {
	if k.values == nil {
		return "", nil
	}
	return k.values[account], nil
}

func (k *memKeyring) Set(_, account, secret string) error {
	if k.values == nil {
		k.values = map[string]string{}
	}
	k.values[account] = secret
	return nil
}

// withMemKeyring installs an empty in-memory keyring for the test and restores
// the previous reader/writer afterward. It returns the keyring so a test can
// pre-seed or assert stored values.
func withMemKeyring(t *testing.T) *memKeyring {
	t.Helper()
	k := &memKeyring{values: map[string]string{}}
	// The llm package keeps unexported package-level seams; SetKeyringReader and
	// SetKeyringWriter are the supported way to swap them. There is no getter, so
	// restore a no-op double on cleanup (the production wiring re-installs the OS
	// keyring at startup; tests never rely on the prior value).
	llm.SetKeyringReader(k)
	llm.SetKeyringWriter(k)
	t.Cleanup(func() {
		llm.SetKeyringReader(&memKeyring{})
		llm.SetKeyringWriter(&memKeyring{})
	})
	return k
}

// onboardingDeps builds TUI dependencies whose config has one keyed provider
// (deepseek, api_key_env DEEPSEEK_API_KEY) backing one model, plus a coder agent
// pinned to that model — the shape that triggers the "no key" first-run case.
func onboardingDeps() Dependencies {
	deps := testDeps()
	deps.Cfg = &config.Config{
		Providers: []config.Provider{
			{Name: "deepseek", Type: config.ProviderOpenAICompatible, APIKeyEnv: "DEEPSEEK_API_KEY"},
			{Name: "ollama", Type: config.ProviderOllama},
		},
		Models: []config.Model{
			{ID: "deepseek-chat", Provider: "deepseek"},
			{ID: "llama3", Provider: "ollama"},
		},
		Agents: []config.Agent{{Name: "coder", Model: "deepseek-chat"}},
		Ledger: config.LedgerConfig{MaxInrPerMonth: 100},
	}
	return deps
}

func sizedOnboardingModel(t *testing.T, deps Dependencies) *model {
	t.Helper()
	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

// --- needsOnboarding ------------------------------------------------------

func TestNeedsOnboarding_UnknownModel(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	// A config with no models/agents makes initialIdentity return "unknown".
	deps := testDeps()
	deps.Cfg = &config.Config{Ledger: config.LedgerConfig{MaxInrPerMonth: 100}}
	m := newModel(context.Background(), deps)
	require.Equal(t, "unknown", m.status.Model)
	require.True(t, m.needsOnboarding(), "unknown model must require onboarding")
}

func TestNeedsOnboarding_NoKeyForActiveProvider(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := newModel(context.Background(), onboardingDeps())
	require.Equal(t, "deepseek-chat", m.status.Model)
	require.True(t, m.needsOnboarding(), "configured model with no resolvable key must require onboarding")
}

func TestNeedsOnboarding_KeyFromEnv(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "env-token")
	m := newModel(context.Background(), onboardingDeps())
	require.False(t, m.needsOnboarding(), "a model with a key in the env must skip onboarding")
}

func TestNeedsOnboarding_KeyFromKeyring(t *testing.T) {
	k := withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	k.values["deepseek"] = "stored-token"
	m := newModel(context.Background(), onboardingDeps())
	require.False(t, m.needsOnboarding(), "a model with a key in the keyring must skip onboarding")
}

func TestNeedsOnboarding_LocalProviderNeedsNoKey(t *testing.T) {
	withMemKeyring(t)
	// Active model is served by a key-less local provider.
	deps := testDeps()
	deps.Cfg = &config.Config{
		Providers: []config.Provider{{Name: "ollama", Type: config.ProviderOllama}},
		Models:    []config.Model{{ID: "llama3", Provider: "ollama"}},
		Agents:    []config.Agent{{Name: "coder", Model: "llama3"}},
		Ledger:    config.LedgerConfig{MaxInrPerMonth: 100},
	}
	m := newModel(context.Background(), deps)
	require.Equal(t, "llama3", m.status.Model)
	require.False(t, m.needsOnboarding(), "a key-less local provider must not require onboarding")
}

// --- auto-open on first WindowSizeMsg -------------------------------------

func TestOnboarding_OpensOnFirstResizeWhenNoSetup(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	require.True(t, m.dialogs.Contains(onboardingDialogID), "onboarding must open on first resize when unset")
	body := plainText(m.dialogs.Render(200))
	require.Contains(t, body, "Welcome to BharatCode")
	require.Contains(t, body, "deepseek", "the keyed provider must be offered")
	require.Contains(t, body, "Use local Ollama", "the Ollama shortcut must be offered")
	require.Contains(t, body, "ChatGPT", "the experimental ChatGPT entry must be offered")
}

// TestOnboarding_DoesNotOpenWhenSetUp is the critical user requirement: a
// session that already has a model AND a resolvable key goes straight to chat.
func TestOnboarding_DoesNotOpenWhenSetUp(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "env-token")
	m := sizedOnboardingModel(t, onboardingDeps())
	require.False(t, m.dialogs.Contains(onboardingDialogID), "configured+keyed session must skip onboarding")
	require.Equal(t, 0, m.dialogs.Len(), "no dialog should be open when already set up")
}

// TestOnboarding_DoesNotReopenAfterSkip asserts the one-shot guard: dismissing
// onboarding and resizing again does not reopen it.
func TestOnboarding_DoesNotReopenAfterSkip(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	require.True(t, m.dialogs.Contains(onboardingDialogID))

	// Esc on the menu falls through to the generic handler, popping the dialog.
	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.False(t, m.dialogs.Contains(onboardingDialogID), "esc must dismiss onboarding")

	// A second resize must not reopen it.
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.False(t, m.dialogs.Contains(onboardingDialogID), "onboarding must not reopen after being skipped")
}

// --- provider key flow ----------------------------------------------------

func TestOnboarding_StoreKeyAppliesModelAndAllowsChat(t *testing.T) {
	k := withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	require.True(t, m.dialogs.Contains(onboardingDialogID))

	// The deepseek provider (backing the active model) is floated to the top, so
	// it is the default selection. Press enter to go to key entry.
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, onboardingKeyEntry, m.onboarding.step, "selecting a provider must move to key entry")
	require.Equal(t, "deepseek", strings.ToLower(m.onboarding.selected.provider.Name))

	// Type a key; it must render masked (bullets), never the raw secret.
	for _, ch := range "sk-secret" {
		_, _ = m.Update(keyText(string(ch)))
	}
	body := plainText(m.dialogs.Render(200))
	require.NotContains(t, body, "sk-secret", "the raw key must never be rendered")
	require.Contains(t, body, "•", "the key must be masked with bullets")

	// Submit: the key is stored in the keyring and the model is applied.
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, "sk-secret", k.values["deepseek"], "the key must be stored under the lowercase provider name")
	require.Equal(t, "deepseek-chat", m.status.Model, "the provider's default model must be active")
	require.True(t, m.dialogs.Contains("onboarding_done"), "a completion dialog must confirm setup")

	// Dismiss the confirmation; the session is now usable (resolves the key).
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.False(t, m.needsOnboarding(), "after storing a key the session must be set up")
}

func TestOnboarding_EmptyKeyIsRejected(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter)) // pick provider
	require.Equal(t, onboardingKeyEntry, m.onboarding.step)

	_, _ = m.Update(keySpecial("enter", tea.KeyEnter)) // submit empty
	require.Equal(t, onboardingKeyEntry, m.onboarding.step, "an empty key must keep the user on entry")
	require.True(t, m.dialogs.Contains(onboardingDialogID))
	require.Contains(t, plainText(m.dialogs.Render(200)), "empty")
}

func TestOnboarding_KeyEntryEscReturnsToMenu(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter)) // pick provider
	require.Equal(t, onboardingKeyEntry, m.onboarding.step)

	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))
	require.Equal(t, onboardingMenu, m.onboarding.step, "esc on key entry must step back to the menu")
	require.True(t, m.dialogs.Contains(onboardingDialogID), "onboarding must stay open after stepping back")
}

func TestOnboarding_KeyEntryAcceptsPaste(t *testing.T) {
	k := withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter)) // pick provider

	// A bracketed paste (with stray whitespace) must land trimmed in the buffer.
	_, _ = m.Update(tea.PasteMsg("  sk-pasted-key\n"))
	require.Equal(t, "sk-pasted-key", m.onboarding.keyInput.String())

	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, "sk-pasted-key", k.values["deepseek"])
}

func TestOnboarding_BackspaceEditsMaskedKey(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter)) // pick provider
	for _, ch := range "abc" {
		_, _ = m.Update(keyText(string(ch)))
	}
	_, _ = m.Update(keySpecial("backspace", tea.KeyBackspace))
	require.Equal(t, "ab", m.onboarding.keyInput.String(), "backspace must remove the last key character")
}

// --- local Ollama ---------------------------------------------------------

func TestOnboarding_LocalOllamaActivatesModelNoKey(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())

	// Move the cursor to the "Use local Ollama" row, then select it.
	idx := -1
	for i, opt := range m.onboarding.options {
		if opt.kind == optLocalOllama {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0, "the Ollama option must be present")
	for m.onboarding.cursor < idx {
		_, _ = m.Update(keySpecial("down", tea.KeyDown))
	}
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	require.Equal(t, "llama3", m.status.Model, "Ollama selection must activate its local model")
	require.False(t, m.dialogs.Contains(onboardingDialogID), "onboarding must close after Ollama setup")
	require.False(t, m.needsOnboarding(), "a key-less local model must satisfy setup")
}

// --- ChatGPT seam ---------------------------------------------------------

func TestOnboarding_ChatGPTDispatchesStartLoginMsg(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())

	idx := -1
	for i, opt := range m.onboarding.options {
		if opt.kind == optChatGPT {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0, "the ChatGPT option must be present")
	for m.onboarding.cursor < idx {
		_, _ = m.Update(keySpecial("down", tea.KeyDown))
	}
	_, cmd := m.Update(keySpecial("enter", tea.KeyEnter))
	require.False(t, m.dialogs.Contains(onboardingDialogID), "ChatGPT selection must close onboarding")
	require.NotNil(t, cmd, "ChatGPT selection must emit a command")

	// The emitted command must produce startChatGPTLoginMsg — the OAuth seam.
	msg := cmd()
	_, isStart := msg.(startChatGPTLoginMsg)
	require.True(t, isStart, "ChatGPT selection must dispatch startChatGPTLoginMsg")

	// Feeding that message back launches the OAuth flow via tea.Exec: a non-nil
	// command is returned (the exec that will pause the TUI and open the
	// browser), and no dialog is pushed synchronously when the user is not yet
	// signed in (dialogs are shown asynchronously after the flow completes via
	// chatgptLoginDoneMsg).
	_, oauthCmd := m.Update(startChatGPTLoginMsg{})
	require.NotNil(t, oauthCmd, "the ChatGPT login handler must return an exec command")
}

// --- navigation -----------------------------------------------------------

func TestOnboarding_ArrowKeysMoveCursor(t *testing.T) {
	withMemKeyring(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := sizedOnboardingModel(t, onboardingDeps())
	require.Equal(t, 0, m.onboarding.cursor)

	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 1, m.onboarding.cursor, "down must advance the cursor")

	_, _ = m.Update(keySpecial("up", tea.KeyUp))
	require.Equal(t, 0, m.onboarding.cursor, "up must move the cursor back")
}
