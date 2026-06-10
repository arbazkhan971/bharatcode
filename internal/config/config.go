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
	Providers    []Provider         `json:"providers"`
	Models       []Model            `json:"models"`
	Permissions  PermConfig         `json:"permissions"`
	Agents       []Agent            `json:"agents"`
	Hooks        []Hook             `json:"hooks"`
	MCP          []MCPServer        `json:"mcp"`
	LSP          []LSPServer        `json:"lsp"`
	Ledger       LedgerConfig       `json:"ledger"`
	Options      Options            `json:"options"`
	Sandbox      SandboxConfig      `json:"sandbox"`
	Cache        CacheConfig        `json:"cache"`
	Routing      RoutingConfig      `json:"routing"`
	Verification VerificationConfig `json:"verification"`
}

// VerificationTrigger names a class of change that, when produced during a
// turn, makes verification REQUIRED before the agent may report the work done.
// The values are the policy's stable vocabulary: they are encoded into the
// agent system prompt and surface in the final response when verification is
// claimed, so they double as documentation and as the testable contract.
type VerificationTrigger string

const (
	// VerifyTriggerSourceEdit fires when a write-class tool (write, edit,
	// multiedit, patch, rename) changes a source file.
	VerifyTriggerSourceEdit VerificationTrigger = "source_edit"
	// VerifyTriggerGeneratedArtifact fires when a generated frontend artifact
	// (a build output, a bundled asset, a compiled stylesheet) is produced or
	// changed.
	VerifyTriggerGeneratedArtifact VerificationTrigger = "generated_artifact"
	// VerifyTriggerPackageManifest fires when a package manifest (go.mod,
	// package.json, pyproject.toml, Cargo.toml, and the like) is touched.
	VerifyTriggerPackageManifest VerificationTrigger = "package_manifest"
	// VerifyTriggerTestOrBuildFile fires when a test file or a build/CI file
	// (Makefile, Dockerfile, a *_test.go, a workflow YAML) is touched.
	VerifyTriggerTestOrBuildFile VerificationTrigger = "test_or_build_file"
)

// VerificationSkipReason enumerates the ONLY reasons the agent may skip
// verification on a turn that would otherwise require it. Any other excuse is
// not a sanctioned skip and the work must not be reported as done. The reasons
// are part of the prompt contract and are echoed verbatim into the final
// response as "skipped (<reason>)".
type VerificationSkipReason string

const (
	// SkipNoTestCommand is allowed when the project exposes no test, build, or
	// lint command to run (no manifest target, no recognizable toolchain).
	SkipNoTestCommand VerificationSkipReason = "no_test_command"
	// SkipDependencyUnavailable is allowed when an external dependency required
	// to verify is unavailable (toolchain not installed, network or service
	// down, credentials absent).
	SkipDependencyUnavailable VerificationSkipReason = "dependency_unavailable"
	// SkipUserOptedOut is allowed when the user explicitly asked not to run
	// tests, the build, or the linter for this change.
	SkipUserOptedOut VerificationSkipReason = "user_opted_out"
)

// VerificationConfig encodes BharatCode's verification policy: when verifying a
// change is REQUIRED, and which reasons may justify skipping it. The policy is
// data, not prose, so it is explicit and testable; the agent system prompt
// renders the same rules so the model and the config never drift.
//
// The policy is ON by default: a zero VerificationConfig (the value an omitted
// "verification" block produces) selects the strict default set of triggers and
// the standard skip reasons. Set Disabled to make verification advisory only —
// the agent is still asked to verify but nothing depends on the trigger/skip
// vocabulary.
type VerificationConfig struct {
	// Disabled turns the policy off. It defaults to false, so verification is
	// required by default; set true to make verification advisory rather than a
	// reported contract.
	Disabled bool `json:"disabled,omitempty"`
	// RequiredTriggers lists the change classes that make verification
	// required. Empty selects the built-in default set (every trigger), so a
	// config that omits the field gets the strict policy.
	RequiredTriggers []VerificationTrigger `json:"required_triggers,omitempty"`
	// AllowedSkipReasons lists the skip reasons the policy sanctions. Empty
	// selects the built-in default set (every reason), so a config that omits
	// the field gets the standard escape hatches.
	AllowedSkipReasons []VerificationSkipReason `json:"allowed_skip_reasons,omitempty"`
}

// defaultVerificationTriggers is the strict default: every change class
// requires verification.
var defaultVerificationTriggers = []VerificationTrigger{
	VerifyTriggerSourceEdit,
	VerifyTriggerGeneratedArtifact,
	VerifyTriggerPackageManifest,
	VerifyTriggerTestOrBuildFile,
}

// defaultVerificationSkipReasons is the default allow-list of skip reasons.
var defaultVerificationSkipReasons = []VerificationSkipReason{
	SkipNoTestCommand,
	SkipDependencyUnavailable,
	SkipUserOptedOut,
}

// Triggers returns the effective set of change classes that require
// verification: the configured RequiredTriggers, or the strict default set
// when none are configured.
func (v VerificationConfig) Triggers() []VerificationTrigger {
	if len(v.RequiredTriggers) == 0 {
		return append([]VerificationTrigger(nil), defaultVerificationTriggers...)
	}
	return append([]VerificationTrigger(nil), v.RequiredTriggers...)
}

// SkipReasons returns the effective allow-list of skip reasons: the configured
// AllowedSkipReasons, or the default set when none are configured.
func (v VerificationConfig) SkipReasons() []VerificationSkipReason {
	if len(v.AllowedSkipReasons) == 0 {
		return append([]VerificationSkipReason(nil), defaultVerificationSkipReasons...)
	}
	return append([]VerificationSkipReason(nil), v.AllowedSkipReasons...)
}

// RequiresVerification reports whether a change of class t obliges the agent to
// verify before reporting the work done. When the policy is disabled it always
// returns false.
func (v VerificationConfig) RequiresVerification(t VerificationTrigger) bool {
	if v.Disabled {
		return false
	}
	for _, want := range v.Triggers() {
		if want == t {
			return true
		}
	}
	return false
}

// SkipAllowed reports whether r is a sanctioned reason to skip verification
// under this policy.
func (v VerificationConfig) SkipAllowed(r VerificationSkipReason) bool {
	for _, want := range v.SkipReasons() {
		if want == r {
			return true
		}
	}
	return false
}

// Validate reports the first inconsistency in the verification policy, or nil
// when it is internally consistent. It rejects unknown triggers and unknown
// skip reasons so a typo in config surfaces explicitly rather than silently
// weakening the policy. It is a self-contained validator, suitable for the
// package-level Validate to call and for tests to exercise directly.
func (v VerificationConfig) Validate() error {
	known := map[VerificationTrigger]bool{
		VerifyTriggerSourceEdit:        true,
		VerifyTriggerGeneratedArtifact: true,
		VerifyTriggerPackageManifest:   true,
		VerifyTriggerTestOrBuildFile:   true,
	}
	for i, t := range v.RequiredTriggers {
		if !known[t] {
			return fmt.Errorf("invalid verification trigger %q at /verification/required_triggers/%d", t, i)
		}
	}
	knownReasons := map[VerificationSkipReason]bool{
		SkipNoTestCommand:         true,
		SkipDependencyUnavailable: true,
		SkipUserOptedOut:          true,
	}
	for i, r := range v.AllowedSkipReasons {
		if !knownReasons[r] {
			return fmt.Errorf("invalid verification skip reason %q at /verification/allowed_skip_reasons/%d", r, i)
		}
	}
	return nil
}

// CacheConfig toggles the LLM response cache that serves repeated,
// deterministic (temperature-0) requests from memory instead of re-calling the
// provider. It is off by default, preserving current behavior. When Enabled is
// true each configured provider is wrapped in a caching layer backed by an LRU
// of at most MaxEntries entries; a non-positive MaxEntries selects the package
// default.
type CacheConfig struct {
	Enabled    bool `json:"enabled,omitempty"`
	MaxEntries int  `json:"max_entries,omitempty"`
}

// RoutingConfig toggles cost-aware model routing, which sends short, tool-free
// turns to the cheapest configured model and long or tool-driven turns to the
// strongest one. It is off by default, leaving each agent pinned to its
// configured model. When Enabled is true a CostAwareRouter is installed on
// every agent loop. PromptLenThreshold is the user-prompt character count at or
// above which a turn is treated as complex (a non-positive value selects the
// router default); ToolsImplyComplex, when true, treats any tool-enabled turn
// as complex.
type RoutingConfig struct {
	Enabled            bool `json:"enabled,omitempty"`
	PromptLenThreshold int  `json:"prompt_len_threshold,omitempty"`
	ToolsImplyComplex  bool `json:"tools_imply_complex,omitempty"`
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
	// ProviderOpenAIResponses is for the OpenAI Responses API shape.
	ProviderOpenAIResponses ProviderType = "openai_responses"
	// ProviderCodexOAuth is the experimental provider that reuses the Codex
	// CLI's stored ChatGPT subscription token. It talks to OpenAI's private
	// Codex backend; unsupported and outside OpenAI's third-party terms.
	ProviderCodexOAuth ProviderType = "codex_oauth"
	// ProviderChatGPT is the experimental "Sign in with ChatGPT" provider. Like
	// ProviderCodexOAuth it talks to OpenAI's private ChatGPT Codex backend, but
	// BharatCode performs the OAuth (PKCE) login itself via 'bharatcode auth
	// chatgpt' and owns the token lifecycle (refresh included) rather than
	// borrowing the Codex CLI's on-disk token. Unsupported, outside OpenAI's
	// third-party terms, and for personal single-account use only.
	ProviderChatGPT ProviderType = "chatgpt"
	// ProviderGemini is for Google's native Generative Language API
	// (generateContent / streamGenerateContent) used by Gemini models.
	ProviderGemini ProviderType = "gemini"
	// ProviderAzure is for Azure OpenAI Service endpoints. It speaks the
	// OpenAI chat-completions wire format but authenticates with the
	// "api-key" request header instead of "Authorization: Bearer", and
	// requires a deployment-scoped base_url of the form
	// "https://<resource>.openai.azure.com/openai/deployments/<deploy>?api-version=<ver>".
	ProviderAzure ProviderType = "azure"
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
	// Headers are extra HTTP headers attached to every request this provider
	// sends, on top of the auth and content-type headers the client sets itself.
	// They enable provider-specific attribution and routing — e.g. OpenRouter's
	// HTTP-Referer / X-Title headers, an Azure deployment's api-key, or a
	// corporate proxy token. A custom header never overrides one the provider
	// already set (auth, content type), so it cannot break authentication. Values
	// support ${ENV} interpolation, mirroring base_url and MCP env. Empty by
	// default, in which case the provider behaves exactly as before.
	Headers map[string]string `json:"headers,omitempty"`
	// Fallbacks names other configured providers to try, in order, when this
	// provider fails with a retryable availability error (rate limit, server
	// error, transport failure). It is empty by default, so a provider with no
	// fallbacks behaves exactly as before. Names are matched case-insensitively
	// against other Provider.Name values; an unknown or disabled fallback is
	// skipped at wiring time.
	Fallbacks []string `json:"fallbacks,omitempty"`
}

// ThinkingFormat selects the wire encoding for a model's extended-thinking
// (reasoning) output. Each value corresponds to the on-the-wire field name or
// structure used by a specific provider family. "openai" and the empty string
// (the zero value) are equivalent: the request uses the OpenAI
// reasoning_effort / reasoning_content fields and no special thinking block is
// injected. The other values are:
//
//   - "openrouter": use OpenRouter's unified `reasoning` object (enabled/effort/
//     max_tokens). This is set automatically when the provider's base_url
//     contains "openrouter.ai" and may be overridden here.
//   - "deepseek": treat "reasoning_content" as the thinking field name in both
//     request and response, matching the DeepSeek-R1 wire format.
//   - "qwen": use the Qwen extended-thinking envelope
//     (enable_thinking / thinking_budget in the request body; thinking_content
//     in the delta).
//   - "string-thinking": emit thinking as plain text prepended to the first
//     content chunk — for providers that do not surface thinking in a dedicated
//     field but include it inline (e.g. some local servers).
//   - "none": suppress all thinking fields even when a budget or effort is
//     configured. Use this for providers that 400 on unknown fields.
type ThinkingFormat string

const (
	// ThinkingFormatDefault is the zero-value alias for "openai". When unset,
	// the provider falls back to its own heuristic (e.g. isOpenRouter check).
	ThinkingFormatDefault ThinkingFormat = ""
	// ThinkingFormatOpenAI uses reasoning_effort / reasoning_content (OpenAI).
	ThinkingFormatOpenAI ThinkingFormat = "openai"
	// ThinkingFormatOpenRouter uses OpenRouter's `reasoning` object.
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
	// ThinkingFormatDeepSeek uses deepseek-style reasoning_content field.
	ThinkingFormatDeepSeek ThinkingFormat = "deepseek"
	// ThinkingFormatQwen uses Qwen's enable_thinking / thinking_budget fields.
	ThinkingFormatQwen ThinkingFormat = "qwen"
	// ThinkingFormatStringThinking prepends thinking as plain text to content.
	ThinkingFormatStringThinking ThinkingFormat = "string-thinking"
	// ThinkingFormatNone suppresses all thinking fields.
	ThinkingFormatNone ThinkingFormat = "none"
)

// CacheControlFormat selects how prompt-caching control hints are serialized
// for the provider. "anthropic" sends Anthropic-style cache_control blocks in
// the message content. "none" (the default for non-Anthropic dialects) omits
// them, preventing endpoints that do not understand the field from returning a
// 400 on unexpected structure.
type CacheControlFormat string

const (
	// CacheControlFormatNone omits cache_control from the wire request.
	CacheControlFormatNone CacheControlFormat = "none"
	// CacheControlFormatAnthropic emits Anthropic-style cache_control blocks.
	CacheControlFormatAnthropic CacheControlFormat = "anthropic"
)

// ToolResultQuirk names provider-specific quirks in how tool results must be
// formatted. The zero value ("") means standard OpenAI-compatible formatting.
//
//   - "": standard tool result as a {role:"tool", tool_call_id, content} message.
//   - "user-content": some local servers reject the "tool" role and require
//     tool results as a user-role message with a text description instead.
type ToolResultQuirk string

const (
	// ToolResultQuirkNone is the standard OpenAI tool-result message format.
	ToolResultQuirkNone ToolResultQuirk = ""
	// ToolResultQuirkUserContent wraps tool results in a user-role text message.
	ToolResultQuirkUserContent ToolResultQuirk = "user-content"
)

// ModelCompat carries declarative per-model provider-compatibility flags for
// OpenAI-compatible (and quirky) endpoints that deviate from the OpenAI
// baseline. All fields are optional (pointer or zero-value = "use the
// heuristic / current default"), so a Model with no Compat block behaves
// exactly as before.
//
// Example config:
//
//	{
//	  "id": "deepseek-r1",
//	  "provider": "deepseek",
//	  "compat": {
//	    "thinking_format": "deepseek",
//	    "context_window": 131072
//	  }
//	}
type ModelCompat struct {
	// ThinkingFormat selects how extended-thinking (reasoning) output is encoded
	// on the wire. Empty (the default) falls back to heuristic detection.
	ThinkingFormat ThinkingFormat `json:"thinking_format,omitempty"`

	// CacheControlFormat selects how prompt-caching hints are serialized.
	// Empty (the default) uses "none" for OpenAI-compatible providers.
	CacheControlFormat CacheControlFormat `json:"cache_control_format,omitempty"`

	// StrictTools, when true, adds "strict": true to every function definition
	// in the tools array. Some providers (Azure OpenAI, certain OpenRouter
	// upstreams) require this for reliable JSON schema adherence; the OpenAI
	// baseline omits it by default. Has no effect when no tools are configured.
	StrictTools bool `json:"strict_tools,omitempty"`

	// ToolResultQuirk selects an alternate tool-result message format for
	// providers that reject the standard OpenAI tool-result structure.
	// Empty (the default) uses standard formatting.
	ToolResultQuirk ToolResultQuirk `json:"tool_result_quirk,omitempty"`

	// ContextWindow overrides the model's context window (in tokens) when set to
	// a positive value, taking precedence over the catalog value and the
	// model-id heuristic in inferContextWindow. Use this for private or
	// aggregator-specific variants whose window differs from the public model.
	ContextWindow *int `json:"context_window,omitempty"`

	// MaxTokens overrides the maximum output tokens the provider will generate.
	// A positive value replaces the model-id heuristic; zero (the default) falls
	// back to the heuristic or the caller-supplied value.
	MaxTokens *int `json:"max_tokens,omitempty"`

	// SupportsImages overrides the model-level supports_images flag. Use a
	// pointer so absent = keep the model's configured value.
	SupportsImages *bool `json:"supports_images,omitempty"`

	// Reasoning overrides the per-model reasoning capability detection. When
	// true, the model is treated as a reasoning model (omit temperature, use
	// max_completion_tokens). When false, it is treated as a non-reasoning chat
	// model even if the id-heuristic would classify it as reasoning. Nil (the
	// default) defers to the heuristic.
	Reasoning *bool `json:"reasoning,omitempty"`
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
	// ReasoningEffort, when non-empty, requests a fixed hidden-reasoning budget
	// from an OpenAI reasoning model (o-series, gpt-5 reasoning) on every turn.
	// Valid values are provider-defined ("low", "medium", "high"). It is ignored
	// by non-reasoning models, which the provider gates by model id, so setting
	// it on a classic model is harmless. Empty (the default) sends no field.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ThinkingBudget, when positive, opts an Anthropic extended-thinking model
	// (Claude 3.7 Sonnet, Claude 4 families) into visible reasoning, capping the
	// tokens it may spend per turn. It is ignored by models that do not support
	// thinking, which the provider gates by model id. Zero (the default) leaves
	// thinking off.
	ThinkingBudget int `json:"thinking_budget,omitempty"`
	// Compat, when non-nil, carries declarative per-model compatibility flags for
	// OpenAI-compatible endpoints that deviate from the baseline. A nil Compat
	// block (the default) preserves existing heuristic behavior unchanged.
	Compat *ModelCompat `json:"compat,omitempty"`
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
	// HookUserPromptSubmit fires when the user submits a prompt, before the
	// turn runs. The hook may block the prompt or inject additional context.
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
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
//
// VerifyCommand, when non-empty, is run after a successful write-class tool
// execution that matches the hook's Match pattern. It is opt-in: an empty
// VerifyCommand disables verification entirely, preserving the prior behaviour.
// VerifyTimeoutSeconds caps how long the verify command may run; a non-positive
// value selects a sensible default (30 s).
type Hook struct {
	Event                HookEvent `json:"event"`
	Match                string    `json:"match,omitempty"` // glob over tool name
	Command              string    `json:"command"`
	Timeout              int       `json:"timeout_seconds,omitempty"`
	VerifyCommand        string    `json:"verify_command,omitempty"`
	VerifyTimeoutSeconds int       `json:"verify_timeout_seconds,omitempty"`
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
	DisableProviderAutoUpdate bool `json:"disable_provider_auto_update,omitempty"`
	// AutoUpdate opts in to self-applying updates on startup: when true (and not
	// in offline mode, and the binary carries a real stamped version/commit),
	// BharatCode does a best-effort, time-bounded check at launch and, if a newer
	// release exists, downloads and installs it in place. It is off by default,
	// so the binary never mutates itself unless the user asks. The on-disk swap
	// takes effect on the next start; the running process is never re-executed.
	AutoUpdate     bool          `json:"auto_update,omitempty"`
	DataDir        string        `json:"data_dir,omitempty"`
	LogLevel       string        `json:"log_level,omitempty"` // "debug","info","warn","error"
	RequestTimeout time.Duration `json:"request_timeout,omitempty"`
	// AutoCompactThreshold, when positive, enables automatic context compaction.
	// When the provider reports that the input-token count divided by the model's
	// context window meets or exceeds this fraction the loop proactively compacts
	// the in-memory history so the next turn does not overflow. A value of 0
	// disables auto-compaction (the default). Typical values are 0.85–0.95.
	AutoCompactThreshold float64 `json:"auto_compact_threshold,omitempty"`
	// BashDefaultTimeoutSec is the per-call timeout applied to foreground bash
	// commands that do not supply their own timeout. A positive value caps how
	// long a single bash call may run; zero or negative disables the default (no
	// cap — the command runs until it finishes or the agent turn is cancelled).
	// This prevents hung commands (sleep infinity, ping, blocking I/O) from
	// stalling the agent indefinitely. Typical values: 120 (matches Claude Code).
	BashDefaultTimeoutSec int `json:"bash_default_timeout_sec,omitempty"`
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
		AutoUpdate                bool   `json:"auto_update,omitempty"`
		DataDir                   string `json:"data_dir,omitempty"`
		LogLevel                  string `json:"log_level,omitempty"`
		RequestTimeout            string `json:"request_timeout,omitempty"`
	}{
		DisableProviderAutoUpdate: o.DisableProviderAutoUpdate,
		AutoUpdate:                o.AutoUpdate,
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
