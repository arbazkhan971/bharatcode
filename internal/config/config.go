// Package config loads and validates BharatCode configuration from
// the global file (~/.config/bharatcode/config.json), the project
// file (.bharatcode.json), and the embedded defaults. Project
// settings override global; global overrides defaults.
package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Config is the root configuration. All fields are JSON-tagged with
// snake_case names. Slice fields preserve insertion order; merge
// semantics are documented per-field on the merge() method.
type Config struct {
	Providers   []Provider    `json:"providers"`
	Models      []Model       `json:"models"`
	Permissions PermConfig    `json:"permissions"`
	Agents      []Agent       `json:"agents"`
	Hooks       []Hook        `json:"hooks"`
	MCP         []MCPServer   `json:"mcp"`
	LSP         []LSPServer   `json:"lsp"`
	Ledger      LedgerConfig  `json:"ledger"`
	Options     Options       `json:"options"`
	Sandbox     SandboxConfig `json:"sandbox"`
}

// SandboxConfig selects the OS-level confinement applied around shell command
// execution. Mode is one of "off", "workspace-write" (the default: reads
// anywhere, writes restricted to the workspace and temp dir, no network),
// "read-only" (no writes, no network), or "full" (no sandbox). The string is
// mapped to a shell.SandboxMode at wiring time; config does not import shell,
// so unknown values are tolerated here and resolved to the safe default by the
// shell layer. When the platform or its sandbox launcher is unavailable the
// mode degrades to off with a logged warning rather than failing.
type SandboxConfig struct {
	Mode string `json:"mode,omitempty"`
}

// ProviderType identifies the API dialect a Provider speaks. A
// single provider value drives both the wire format chosen by the
// LLM client and the env-var lookup used for the API key.
type ProviderType string

const (
	// ProviderAnthropic is for Anthropic API.
	ProviderAnthropic ProviderType = "anthropic"
	// ProviderOpenAI is for OpenAI API.
	ProviderOpenAI ProviderType = "openai"
	// ProviderOpenAICompatible is for OpenAI compatible APIs (DeepSeek, Groq, etc.).
	ProviderOpenAICompatible ProviderType = "openai_compatible"
	// ProviderOllama is for Ollama local API.
	ProviderOllama ProviderType = "ollama"
	// ProviderLMStudio is for LM Studio local API.
	ProviderLMStudio ProviderType = "lmstudio"
)

// Provider describes one LLM endpoint. APIKeyEnv names an
// environment variable that supplies the secret at runtime; the
// secret itself never lives in the config file. BaseURL is required
// for openai_compatible, ollama, and lmstudio types; it is ignored
// for anthropic and openai (which use the SDK's built-in URLs).
type Provider struct {
	Name      string       `json:"name"`
	Type      ProviderType `json:"type"`
	BaseURL   string       `json:"base_url,omitempty"`
	APIKeyEnv string       `json:"api_key_env,omitempty"`
	Models    []string     `json:"models"`
	Disabled  bool         `json:"disabled,omitempty"`
}

// Model is one entry in a model pack. Prices are quoted per
// million tokens in USD; the ledger converts to INR using
// LedgerConfig.UsdInrRate. ContextWindow is the model's maximum
// total context (input + output) in tokens.
type Model struct {
	ID                    string  `json:"id"`
	Provider              string  `json:"provider"`
	ContextWindow         int     `json:"context_window"`
	InputPricePerMTokUSD  float64 `json:"input_price_per_mtok_usd"`
	OutputPricePerMTokUSD float64 `json:"output_price_per_mtok_usd"`
	SupportsImages        bool    `json:"supports_images"`
	SupportsTools         bool    `json:"supports_tools"`
}

// PermConfig declares default permission behaviour for tool calls.
// The agent gate (internal/permission) consults this before
// prompting the user; --yolo at the CLI flips AllowAll.
type PermConfig struct {
	AllowAll     bool              `json:"allow_all,omitempty"`
	AutoApprove  []string          `json:"auto_approve,omitempty"`
	AlwaysPrompt []string          `json:"always_prompt,omitempty"`
	Deny         []string          `json:"deny,omitempty"`
	Remembered   map[string]string `json:"remembered,omitempty"`
}

// Agent is one named agent definition (e.g. "coder", "task").
type Agent struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"` // ref to a Model.ID
	SystemPrompt string   `json:"system_prompt"`
	Tools        []string `json:"tools,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// HookEvent enumerates the points in the agent lifecycle where a
// hook may fire.
type HookEvent string

const (
	// HookPreToolUse fires before a tool is executed.
	HookPreToolUse HookEvent = "PreToolUse"
	// HookPostToolUse fires after a tool executes.
	HookPostToolUse HookEvent = "PostToolUse"
	// HookOnError fires when an error occurs in the agent loop.
	HookOnError HookEvent = "OnError"
	// HookOnSession fires when a session is created/started.
	HookOnSession HookEvent = "OnSession"
	// HookSessionStart fires when a session starts.
	HookSessionStart HookEvent = "SessionStart"
	// HookSessionEnd fires when a session ends.
	HookSessionEnd HookEvent = "SessionEnd"
	// HookFileEdit fires after a file is edited.
	HookFileEdit HookEvent = "FileEdit"
)

// Hook is a user-defined shell command that fires on a HookEvent.
// Command runs through /bin/sh -c on POSIX, cmd.exe /c on Windows.
type Hook struct {
	Event   HookEvent `json:"event"`
	Match   string    `json:"match,omitempty"` // glob over tool name
	Command string    `json:"command"`
	Timeout int       `json:"timeout_seconds,omitempty"`
}

// MCPServer is one MCP endpoint definition. Transport is "stdio",
// "http", or "sse".
type MCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Disabled  bool              `json:"disabled,omitempty"`
}

// LSPServer is one LSP language-server definition.
type LSPServer struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	Languages []string `json:"languages"`
	RootFiles []string `json:"root_files,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`
}

// ModelPricing defines pricing per million tokens in USD.
type ModelPricing struct {
	InputPricePerMTokUSD  float64 `json:"input_price_per_mtok_usd"`
	OutputPricePerMTokUSD float64 `json:"output_price_per_mtok_usd"`
}

// LedgerConfig declares cost-accounting policy. Currency is the
// display currency for the TUI footer ("INR" or "USD"). UsdInrRate
// is multiplied by every USD cost to derive an INR cost; the rate
// is user-editable and refreshed manually via `bharatcode update-fx`
// (or left at the embedded default). MaxInr* fields cap spend at
// each window; a request that would exceed the cap triggers a
// confirmation dialog before proceeding.
type LedgerConfig struct {
	Currency         string                  `json:"currency"` // "INR" or "USD"
	UsdInrRate       float64                 `json:"usd_inr_rate"`
	MaxInrPerSession float64                 `json:"max_inr_per_session,omitempty"`
	MaxInrPerDay     float64                 `json:"max_inr_per_day,omitempty"`
	MaxInrPerMonth   float64                 `json:"max_inr_per_month,omitempty"`
	Models           map[string]ModelPricing `json:"models,omitempty"`
}

// Options is a free-form bag of feature toggles that do not
// warrant their own struct (yet).
type Options struct {
	DisableProviderAutoUpdate bool          `json:"disable_provider_auto_update,omitempty"`
	DataDir                   string        `json:"data_dir,omitempty"`
	LogLevel                  string        `json:"log_level,omitempty"` // "debug","info","warn","error"
	RequestTimeout            time.Duration `json:"request_timeout,omitempty"`
}

// UnmarshalJSON customizes unmarshaling of Options.
func (o *Options) UnmarshalJSON(data []byte) error {
	type Alias Options
	aux := &struct {
		RequestTimeout any `json:"request_timeout,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(o),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshaling options alias: %w", err)
	}
	if aux.RequestTimeout != nil {
		switch v := aux.RequestTimeout.(type) {
		case string:
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("parsing request_timeout duration: %w", err)
			}
			o.RequestTimeout = d
		case float64:
			o.RequestTimeout = time.Duration(v)
		default:
			return fmt.Errorf("invalid type for request_timeout: expected string or number")
		}
	}
	return nil
}

// MarshalJSON customizes marshaling of Options.
func (o Options) MarshalJSON() ([]byte, error) {
	type Alias Options
	if o.RequestTimeout == 0 {
		return json.Marshal(struct {
			Alias
		}{
			Alias: Alias(o),
		})
	}
	return json.Marshal(struct {
		DisableProviderAutoUpdate bool   `json:"disable_provider_auto_update,omitempty"`
		DataDir                   string `json:"data_dir,omitempty"`
		LogLevel                  string `json:"log_level,omitempty"`
		RequestTimeout            string `json:"request_timeout,omitempty"`
	}{
		DisableProviderAutoUpdate: o.DisableProviderAutoUpdate,
		DataDir:                   o.DataDir,
		LogLevel:                  o.LogLevel,
		RequestTimeout:            o.RequestTimeout.String(),
	})
}

// Scope identifies which on-disk file a Save() targets.
type Scope int

const (
	// ScopeGlobal points to ~/.config/bharatcode/config.json.
	ScopeGlobal Scope = iota
	// ScopeProject points to .bharatcode.json in the project root.
	ScopeProject
)
