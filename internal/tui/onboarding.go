package tui

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// onboardingDialogID is the stack id of the first-run onboarding dialog. It is
// matched in handleKey so the onboarding key handler intercepts navigation and
// entry keys before the generic dialog handler, mirroring how the model picker
// claims "model_picker".
const onboardingDialogID = "onboarding"

// onboardingStep is the phase of the first-run onboarding flow: the provider
// menu, or the masked key-entry prompt for a chosen provider.
type onboardingStep int

const (
	// onboardingMenu lists the setup choices: each key-requiring provider, the
	// local-Ollama shortcut, and the experimental ChatGPT sign-in.
	onboardingMenu onboardingStep = iota
	// onboardingKeyEntry collects a masked API key for the chosen provider.
	onboardingKeyEntry
)

// onboardingOptionKind distinguishes the three kinds of menu rows so selection
// dispatches to the right handler regardless of where a row lands in the list.
type onboardingOptionKind int

const (
	// optProviderKey pastes an API key for a remote provider.
	optProviderKey onboardingOptionKind = iota
	// optLocalOllama wires the local Ollama provider, which needs no key.
	optLocalOllama
	// optChatGPT is the experimental ChatGPT sign-in entry point the OAuth work
	// hooks into. Selecting it dispatches startChatGPTLoginMsg.
	optChatGPT
)

// onboardingOption is one selectable row in the onboarding menu.
type onboardingOption struct {
	kind onboardingOptionKind
	// label is the row text shown to the user.
	label string
	// provider is the configured provider this row sets up (its lowercase name
	// is the keyring account). Empty for the ChatGPT row.
	provider config.Provider
	// model is the default model activated once the provider is set up — the
	// first configured model whose Provider matches provider.Name. Empty when no
	// model references the provider, in which case selection still stores the key
	// but leaves the active model unchanged.
	model config.Model
}

// onboardingState holds the in-progress first-run onboarding. Its zero value is
// inert (no options, menu step), so a model that never onboards renders exactly
// as before. It mirrors the model-picker pattern of carrying picker state on the
// model rather than inside the dialog.
type onboardingState struct {
	step    onboardingStep
	options []onboardingOption
	cursor  int
	// keyInput accumulates the masked API key while step is onboardingKeyEntry.
	keyInput strings.Builder
	// selected is the option whose key is being entered.
	selected onboardingOption
	// errMsg is a transient validation message shown on the key-entry screen
	// (e.g. an empty submission or a failed keyring write).
	errMsg string
}

// startChatGPTLoginMsg is the message dispatched when the user picks
// "Sign in with ChatGPT (experimental)" in onboarding. It is the clear entry
// point the OAuth work wires up: the onboarding here closes its dialog and emits
// this message, and the OAuth handler can begin the browser/device flow from a
// single Update case (see handleStartChatGPTLogin). Keeping it a message rather
// than a direct call keeps the OAuth flow decoupled from onboarding internals.
type startChatGPTLoginMsg struct{}

// needsOnboarding reports whether the TUI should open first-run onboarding: the
// user has no model configured (initialIdentity returned "unknown"), or the
// active model's provider has no resolvable API key (neither env nor keyring).
// It is the strict "no usable setup" gate from the task — a session that already
// has a model AND a resolvable key returns false and goes straight to chat, so a
// returning user never sees onboarding.
func (m *model) needsOnboarding() bool {
	if m.status.Model == "" || m.status.Model == "unknown" {
		return true
	}
	prov, ok := m.providerForModel(m.status.Model)
	if !ok {
		// A model is set but no configured provider backs it; treat as set up so
		// onboarding does not fight an unusual hand-rolled config. The provider
		// layer surfaces a precise error if a turn is actually attempted.
		return false
	}
	// chatgpt uses an OAuth session file rather than an API key env var, so
	// HasAPIKey would always return true (empty envVar → no key needed). Check
	// the session file directly instead and trigger onboarding when it is absent.
	if prov.Type == config.ProviderChatGPT {
		_, err := llm.ChatGPTStatus()
		return err != nil
	}
	return !llm.HasAPIKey(prov.APIKeyEnv, strings.ToLower(prov.Name))
}

// providerForModel returns the configured provider backing the model identified
// by modelID (either its bare ID or a "provider/ID" label). It maps the model to
// its Provider name and then finds the matching config.Provider, so the caller
// can read the api_key_env and decide whether a key resolves.
func (m *model) providerForModel(modelID string) (config.Provider, bool) {
	if m.deps.Cfg == nil {
		return config.Provider{}, false
	}
	provName := ""
	for _, mod := range m.deps.Cfg.Models {
		if mod.ID == modelID || mod.Provider+"/"+mod.ID == modelID {
			provName = mod.Provider
			break
		}
	}
	if provName == "" {
		return config.Provider{}, false
	}
	for _, p := range m.deps.Cfg.Providers {
		if strings.EqualFold(p.Name, provName) {
			return p, true
		}
	}
	return config.Provider{}, false
}

// maybeStartOnboarding opens the onboarding dialog when first-run setup is
// needed and onboarding is not already open. It is called once, from the first
// WindowSizeMsg, so the dialog renders with a known width (a dialog pushed in
// newModel would render before any size arrives). It is a no-op when the session
// is already usable, so a configured user proceeds straight to chat.
func (m *model) maybeStartOnboarding() {
	if m.dialogs.Contains(onboardingDialogID) {
		return
	}
	if !m.needsOnboarding() {
		return
	}
	m.openOnboarding()
}

// openOnboarding builds the menu options and pushes the onboarding dialog. The
// menu lists every enabled provider that requires an API key, then a local
// Ollama shortcut when that provider is configured, then the experimental
// ChatGPT sign-in. The provider backing the currently-configured model (if any)
// is floated to the top so the most likely choice — finishing setup for the
// model the config already selected, which then chats immediately — is the
// default selection.
func (m *model) openOnboarding() {
	m.onboarding = onboardingState{step: onboardingMenu}
	m.onboarding.options = m.buildOnboardingOptions()
	m.dialogs.Push(&dialog.Text{
		DialogID: onboardingDialogID,
		Title:    "Welcome to BharatCode",
		Body:     m.onboardingBody(),
		Theme:    m.theme,
	})
}

// buildOnboardingOptions assembles the menu rows. Providers are taken from the
// config in order, skipping disabled ones, the local-only types (which appear as
// the dedicated Ollama shortcut), and the experimental codex_oauth provider
// (surfaced as the ChatGPT row instead). The provider backing the active model
// is moved to the front.
func (m *model) buildOnboardingOptions() []onboardingOption {
	var opts []onboardingOption
	hasOllama := false
	if m.deps.Cfg != nil {
		for _, p := range m.deps.Cfg.Providers {
			if p.Disabled {
				continue
			}
			switch p.Type {
			case config.ProviderOllama:
				hasOllama = true
				continue
			case config.ProviderLMStudio:
				// Other local, key-less endpoints are not part of the guided
				// first-run choices; a user with one can pick it via /model.
				continue
			case config.ProviderCodexOAuth:
				// Represented by the dedicated ChatGPT sign-in row below.
				continue
			case config.ProviderChatGPT:
				// The chatgpt provider has no API key env var — it uses its own
				// OAuth session file. It is represented by the dedicated
				// "Sign in with ChatGPT" row at the bottom of the menu.
				continue
			}
			opts = append(opts, onboardingOption{
				kind:     optProviderKey,
				label:    "Paste an API key for " + p.Name,
				provider: p,
				model:    m.defaultModelForProvider(p.Name),
			})
		}
	}
	// Float the provider backing the active model to the front so the default
	// selection finishes setup for the already-configured model.
	if prov, ok := m.providerForModel(m.status.Model); ok {
		for i := range opts {
			if strings.EqualFold(opts[i].provider.Name, prov.Name) {
				sel := opts[i]
				opts = append(opts[:i], opts[i+1:]...)
				opts = append([]onboardingOption{sel}, opts...)
				break
			}
		}
	}
	if hasOllama {
		opts = append(opts, onboardingOption{kind: optLocalOllama, label: "Use local Ollama (no API key needed)"})
	}
	opts = append(opts, onboardingOption{kind: optChatGPT, label: "Sign in with ChatGPT (experimental)"})
	return opts
}

// defaultModelForProvider returns the first configured model whose Provider
// matches name, or the zero Model when none reference the provider. It picks the
// model activated after the provider's key is stored.
func (m *model) defaultModelForProvider(name string) config.Model {
	if m.deps.Cfg == nil {
		return config.Model{}
	}
	for _, mod := range m.deps.Cfg.Models {
		if strings.EqualFold(mod.Provider, name) {
			return mod
		}
	}
	return config.Model{}
}

// onboardingBody renders the current onboarding screen: the provider menu with a
// cursor marker, or the masked key-entry prompt. It mirrors the model picker's
// body layout (cursor "> ", a hint footer) so the two feel like one pattern.
func (m *model) onboardingBody() string {
	if m.onboarding.step == onboardingKeyEntry {
		return m.onboardingKeyEntryBody()
	}
	lines := []string{
		"No model is set up yet. Choose how to get started:",
		"",
	}
	if len(m.onboarding.options) == 0 {
		lines = append(lines, "(no providers configured)")
	}
	for i, opt := range m.onboarding.options {
		marker := "  "
		if i == m.onboarding.cursor {
			marker = "> "
		}
		lines = append(lines, marker+opt.label)
	}
	lines = append(lines, "", "↑/↓ to move · enter to select · esc to skip")
	return strings.Join(lines, "\n")
}

// onboardingKeyEntryBody renders the masked API-key prompt for the selected
// provider. The key is shown as bullets so a shoulder-surfer cannot read it,
// matching the masked entry 'bharatcode login' uses. The env-var name is shown
// so a user who would rather set an environment variable knows which one.
func (m *model) onboardingKeyEntryBody() string {
	opt := m.onboarding.selected
	masked := strings.Repeat("•", len([]rune(m.onboarding.keyInput.String())))
	lines := []string{
		"Paste your API key for " + opt.provider.Name + ".",
	}
	if opt.provider.APIKeyEnv != "" {
		lines = append(lines, m.theme.Muted.Render("(or set "+opt.provider.APIKeyEnv+" in your environment)"))
	}
	lines = append(lines,
		"",
		"Key: "+masked+"▌",
	)
	if m.onboarding.errMsg != "" {
		lines = append(lines, "", m.theme.Muted.Render(m.onboarding.errMsg))
	}
	lines = append(lines, "", "enter to save · esc to go back")
	return strings.Join(lines, "\n")
}

// refreshOnboarding re-renders the open onboarding dialog in place so a moved
// cursor, a typed key character, or a step change is reflected. It mirrors
// refreshModelPicker.
func (m *model) refreshOnboarding() {
	m.dialogs.Pop()
	m.dialogs.Push(&dialog.Text{
		DialogID: onboardingDialogID,
		Title:    "Welcome to BharatCode",
		Body:     m.onboardingBody(),
		Theme:    m.theme,
	})
}

// handleOnboardingKey processes keys while the onboarding dialog is open,
// returning whether the key was consumed (an unconsumed key falls through to the
// dialog's own handler, which dismisses on esc/enter). The menu step navigates
// and selects; the key-entry step accumulates a masked key, submits it on enter,
// and steps back to the menu on esc — so esc never strands the user mid-entry.
func (m *model) handleOnboardingKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	if m.onboarding.step == onboardingKeyEntry {
		return m.handleOnboardingKeyEntry(msg)
	}
	switch msg.String() {
	case "up":
		if m.onboarding.cursor > 0 {
			m.onboarding.cursor--
			m.refreshOnboarding()
		}
		return true, nil
	case "down":
		if m.onboarding.cursor < len(m.onboarding.options)-1 {
			m.onboarding.cursor++
			m.refreshOnboarding()
		}
		return true, nil
	case "home":
		if m.onboarding.cursor != 0 {
			m.onboarding.cursor = 0
			m.refreshOnboarding()
		}
		return true, nil
	case "end":
		if last := len(m.onboarding.options) - 1; last >= 0 && m.onboarding.cursor != last {
			m.onboarding.cursor = last
			m.refreshOnboarding()
		}
		return true, nil
	case "enter":
		return m.selectOnboardingOption()
	case "esc":
		// Esc skips onboarding: close it and let the user explore. They can run
		// /model or 'bharatcode login' later; a turn without a key surfaces a
		// precise error rather than crashing. Falling through (consumed=false)
		// lets the generic handler pop the dialog.
		return false, nil
	default:
		return false, nil
	}
}

// handleOnboardingKeyEntry processes keys on the masked key-entry screen.
func (m *model) handleOnboardingKeyEntry(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.submitOnboardingKey()
	case "esc":
		// Step back to the menu rather than abandoning onboarding entirely, so a
		// mistaken provider choice is one keystroke to correct.
		m.onboarding.step = onboardingMenu
		m.onboarding.keyInput.Reset()
		m.onboarding.errMsg = ""
		m.refreshOnboarding()
		return true, nil
	case "backspace":
		s := m.onboarding.keyInput.String()
		if s != "" {
			r := []rune(s)
			m.onboarding.keyInput.Reset()
			m.onboarding.keyInput.WriteString(string(r[:len(r)-1]))
			m.refreshOnboarding()
		}
		return true, nil
	case "ctrl+u":
		if m.onboarding.keyInput.Len() > 0 {
			m.onboarding.keyInput.Reset()
			m.refreshOnboarding()
		}
		return true, nil
	default:
		// Accept printable characters into the key buffer. A pasted key arrives
		// as a PasteMsg handled in Update, not here.
		if text := msg.Key().Text; text != "" {
			m.onboarding.keyInput.WriteString(text)
			m.onboarding.errMsg = ""
			m.refreshOnboarding()
			return true, nil
		}
		return true, nil
	}
}

// selectOnboardingOption acts on the highlighted menu row: a provider row moves
// to masked key entry; the Ollama row wires the local provider immediately; the
// ChatGPT row closes onboarding and dispatches the experimental sign-in message.
func (m *model) selectOnboardingOption() (consumed bool, cmd tea.Cmd) {
	if len(m.onboarding.options) == 0 {
		return true, nil
	}
	opt := m.onboarding.options[m.onboarding.cursor]
	switch opt.kind {
	case optProviderKey:
		m.onboarding.selected = opt
		m.onboarding.step = onboardingKeyEntry
		m.onboarding.keyInput.Reset()
		m.onboarding.errMsg = ""
		m.refreshOnboarding()
		return true, nil
	case optLocalOllama:
		return m.finishLocalOllama()
	case optChatGPT:
		// Close onboarding and hand off to the OAuth flow via a message. This is
		// the documented entry point the ChatGPT/OAuth work wires up.
		m.dialogs.Pop()
		m.onboarding = onboardingState{}
		return true, func() tea.Msg { return startChatGPTLoginMsg{} }
	default:
		return true, nil
	}
}

// submitOnboardingKey validates and stores the entered API key, then activates
// the provider's default model. A token stored in the keyring resolves on the
// next turn (key lookup is lazy), so chat works immediately without a restart.
// An empty key or a failed keyring write is reported in place and keeps the user
// on the entry screen.
func (m *model) submitOnboardingKey() (consumed bool, cmd tea.Cmd) {
	token := strings.TrimSpace(m.onboarding.keyInput.String())
	if token == "" {
		m.onboarding.errMsg = "Key cannot be empty."
		m.refreshOnboarding()
		return true, nil
	}
	opt := m.onboarding.selected
	if err := llm.StoreAPIKey(strings.ToLower(opt.provider.Name), token); err != nil {
		m.onboarding.errMsg = "Could not save key: " + err.Error()
		m.refreshOnboarding()
		return true, nil
	}
	m.dialogs.Pop()
	m.onboarding = onboardingState{}
	m.completeOnboarding(opt.model, opt.provider.Name)
	return true, nil
}

// finishLocalOllama wires the local Ollama provider. Ollama needs no API key, so
// there is nothing to store — onboarding just activates its default model so the
// next turn targets the local endpoint. When the config declares no Ollama model
// the active model is left unchanged and a note explains the model must be added.
func (m *model) finishLocalOllama() (consumed bool, cmd tea.Cmd) {
	mod := m.defaultOllamaModel()
	m.dialogs.Pop()
	m.onboarding = onboardingState{}
	if mod.ID == "" {
		m.dialogs.Push(&dialog.Text{
			DialogID: "onboarding_done",
			Title:    "Local Ollama",
			Body:     "No Ollama model is configured. Add one to the models list, then pick it with /model.",
			Theme:    m.theme,
		})
		return true, nil
	}
	m.completeOnboarding(mod, "ollama")
	return true, nil
}

// defaultOllamaModel returns the first configured model served by an Ollama
// provider, or the zero Model when none exist. It distinguishes Ollama from
// other providers by the provider Type so a renamed provider still matches.
func (m *model) defaultOllamaModel() config.Model {
	if m.deps.Cfg == nil {
		return config.Model{}
	}
	ollamaNames := map[string]bool{}
	for _, p := range m.deps.Cfg.Providers {
		if p.Type == config.ProviderOllama {
			ollamaNames[strings.ToLower(p.Name)] = true
		}
	}
	for _, mod := range m.deps.Cfg.Models {
		if ollamaNames[strings.ToLower(mod.Provider)] {
			return mod
		}
	}
	return config.Model{}
}

// persistActiveModel writes modelID into the "coder" agent entry of the user's
// global config file so the choice survives a restart. It reads the existing
// global file (or starts from an empty Config when none exists), updates the
// first "coder" agent's model field, and saves it back. This preserves all
// other customisations the user has in their global file (providers, models,
// hooks, etc.).
//
// Errors are non-fatal: the in-session routing is already correct via applyModel;
// the worst outcome of a failed write is that the user needs to sign in again
// after a restart.
func (m *model) persistActiveModel(ctx context.Context, modelID string) {
	cfg, err := config.LoadFrom(ctx, config.GlobalPath(), "")
	if err != nil {
		// No existing file or parse error: start from an empty config so we
		// don't accidentally inherit defaults that belong only in the binary.
		cfg = &config.Config{}
	}
	// Update the "coder" agent or, if absent, append one.
	found := false
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "coder" {
			cfg.Agents[i].Model = modelID
			found = true
			break
		}
	}
	if !found {
		cfg.Agents = append(cfg.Agents, config.Agent{Name: "coder", Model: modelID})
	}
	if err := config.Save(ctx, cfg, config.ScopeGlobal); err != nil {
		slog.Warn("Persisting active model to global config", "model", modelID, "error", err)
	}
}

// completeOnboarding finalizes a successful setup: it activates the chosen model
// (reusing the model picker's applyModel so the status bar and live state move
// together) and shows a brief confirmation. The user can chat right away — the
// stored key resolves lazily on the next provider call, and the agent loop reads
// the active model at the top of each turn.
func (m *model) completeOnboarding(mod config.Model, providerName string) {
	if mod.ID != "" {
		m.applyModel(mod)
	}
	body := "You're all set."
	if mod.ID != "" {
		body = fmt.Sprintf("Using %s/%s. Type a message to begin.", providerName, mod.ID)
	}
	m.dialogs.Push(&dialog.Text{
		DialogID: "onboarding_done",
		Title:    "Setup complete",
		Body:     body,
		Theme:    m.theme,
	})
}

// chatgptLoginDoneMsg is delivered to Update when the ChatGPT OAuth flow
// launched by handleStartChatGPTLogin completes (successfully or with an error).
// It carries the signed-in identity (on success) or the error so the TUI can
// activate the chatgpt model or surface a precise failure message.
type chatgptLoginDoneMsg struct {
	id  llm.ChatGPTIdentity
	err error
}

// chatgptFuncExec is a tea.ExecCommand that wraps a plain Go function.
// tea.Exec (bubbletea v2) pauses the alt-screen TUI, releases the terminal to
// the function so it can write progress lines and interact with the user's
// browser, then restores the TUI when the function returns. The three Set*
// methods satisfy the ExecCommand interface; the function uses the writers
// wired in by Set* for its output so the terminal state is correct.
type chatgptFuncExec struct {
	run    func(stdout io.Writer) (llm.ChatGPTIdentity, error)
	stdout io.Writer
	result chatgptLoginDoneMsg
}

func (c *chatgptFuncExec) SetStdin(_ io.Reader)  {}
func (c *chatgptFuncExec) SetStderr(_ io.Writer) {}
func (c *chatgptFuncExec) SetStdout(w io.Writer) { c.stdout = w }

// Run executes the OAuth flow with the terminal's stdout wired in by
// bubbletea before the call. The result is stored on the struct so the
// ExecCallback closure (in handleStartChatGPTLogin) can read it after Run
// returns.
func (c *chatgptFuncExec) Run() error {
	id, err := c.run(c.stdout)
	c.result = chatgptLoginDoneMsg{id: id, err: err}
	// Always return nil: a login error is a domain error surfaced via
	// chatgptLoginDoneMsg, not an exec-level failure. Returning a non-nil
	// error here would cause bubbletea to skip RestoreTerminal on some paths.
	return nil
}

// handleStartChatGPTLogin handles the experimental ChatGPT sign-in chosen in
// onboarding. If the user is already signed in it activates the chatgpt model
// and shows a confirmation. Otherwise it uses tea.Exec to pause the alt-screen
// TUI, release the terminal, and run the browser-based OAuth (PKCE) flow
// interactively; when the flow completes the TUI resumes and
// chatgptLoginDoneMsg is dispatched to finalize the session.
func (m *model) handleStartChatGPTLogin() (tea.Model, tea.Cmd) {
	// Fast-path: already signed in — activate the model and confirm.
	if id, err := llm.ChatGPTStatus(); err == nil {
		who := id.Email
		if who == "" {
			who = "your ChatGPT account"
		}
		mod := m.defaultModelForProvider("chatgpt")
		if mod.ID != "" {
			m.applyModel(mod)
			m.persistActiveModel(m.ctx, mod.ID)
		}
		body := fmt.Sprintf("Already signed in as %s.\n\nType a message to begin.", who)
		if mod.ID == "" {
			body = fmt.Sprintf("Already signed in as %s.\n\nPick the ChatGPT model with /model to use it.", who)
		}
		m.dialogs.Push(&dialog.Text{
			DialogID: "chatgpt_login",
			Title:    "Sign in with ChatGPT",
			Body:     body,
			Theme:    m.theme,
		})
		return m, nil
	}

	// Run the OAuth flow by pausing the TUI so the browser interaction and
	// progress lines can use the terminal directly. bubbletea v2's tea.Exec
	// releases the alt-screen, calls exec.Run() (which calls our closure),
	// then restores the TUI. The callback delivers chatgptLoginDoneMsg so
	// Update can activate the model on success.
	exec := &chatgptFuncExec{
		run: func(stdout io.Writer) (llm.ChatGPTIdentity, error) {
			return llm.LoginChatGPT(m.ctx, stdout)
		},
	}
	return m, tea.Exec(exec, func(err error) tea.Msg {
		// err here is exec.Run()'s return value, which we always set to nil
		// (domain errors are stored in exec.result). Read the real outcome.
		return exec.result
	})
}

// handleChatGPTLoginDone processes the result of the OAuth flow launched by
// handleStartChatGPTLogin. On success it activates the chatgpt model and shows
// a confirmation so the user can chat immediately. On failure it surfaces the
// error and points to the CLI fallback so the session is not stranded.
func (m *model) handleChatGPTLoginDone(msg chatgptLoginDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		body := "ChatGPT sign-in failed: " + msg.err.Error() +
			"\n\nRun 'bharatcode auth chatgpt' in a terminal and restart, or pick a\n" +
			"provider and paste an API key (Ctrl+P)."
		m.dialogs.Push(&dialog.Text{
			DialogID: "chatgpt_login_error",
			Title:    "Sign in with ChatGPT",
			Body:     body,
			Theme:    m.theme,
		})
		return m, nil
	}
	// OAuth succeeded — activate the first chatgpt model so the next turn
	// goes to the right provider without requiring a /model selection.
	// Also persist the choice to the global config so a restart picks it up.
	mod := m.defaultModelForProvider("chatgpt")
	if mod.ID != "" {
		m.applyModel(mod)
		m.persistActiveModel(m.ctx, mod.ID)
	}
	who := msg.id.Email
	if who == "" {
		who = "your ChatGPT account"
	}
	body := fmt.Sprintf("Signed in as %s.", who)
	if mod.ID != "" {
		body = fmt.Sprintf("Signed in as %s. Using chatgpt/%s.\n\nType a message to begin.", who, mod.ID)
	} else {
		body += "\n\nPick the ChatGPT model with /model to use it."
	}
	m.dialogs.Push(&dialog.Text{
		DialogID: "chatgpt_login_done",
		Title:    "Sign in with ChatGPT",
		Body:     body,
		Theme:    m.theme,
	})
	return m, nil
}
