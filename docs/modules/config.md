# config

**Path:** `internal/config/`
**Status:** Completed

## Purpose

The `config` package loads, validates, and serialises BharatCode's user-facing configuration. Configuration lives in two JSON files: a **global** one at `~/.config/bharatcode/config.json` (or `$XDG_CONFIG_HOME/bharatcode/config.json`) shared across all projects, and a **project-local** one at `.bharatcode.json` in the project root. The project file is merged over the global file at load time; the project file wins on any conflict. A built-in default config is embedded in the binary and used as the base layer beneath both files, so BharatCode produces a usable state with zero on-disk configuration.

This module owns the full schema: provider configurations (Anthropic, OpenAI, DeepSeek, Moonshot, Groq, Together, Fireworks, OpenRouter, Ollama, LM Studio), model packs with USD pricing, agent definitions, hook definitions, MCP server definitions, LSP server definitions, permission defaults, and the INR-aware ledger budget block. Anything that needs to be persisted across runs and remain user-editable lives here; ephemeral runtime overrides flow through `db.config_kv` instead.

## Public interface

```go
// Package config loads and validates BharatCode configuration from
// the global file (~/.config/bharatcode/config.json), the project
// file (.bharatcode.json), and the embedded defaults. Project
// settings override global; global overrides defaults.
package config

import (
    "context"
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
}

// ProviderType identifies the API dialect a Provider speaks. A
// single provider value drives both the wire format chosen by the
// LLM client and the env-var lookup used for the API key.
type ProviderType string

const (
    ProviderAnthropic        ProviderType = "anthropic"
    ProviderOpenAI           ProviderType = "openai"
    ProviderOpenAICompatible ProviderType = "openai_compatible"
    ProviderOllama           ProviderType = "ollama"
    ProviderLMStudio         ProviderType = "lmstudio"
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
    AllowAll        bool     `json:"allow_all,omitempty"`
    AutoApprove     []string `json:"auto_approve,omitempty"`
    AlwaysPrompt    []string `json:"always_prompt,omitempty"`
    Deny            []string `json:"deny,omitempty"`
}

// Agent is one named agent definition (e.g. "coder", "task").
type Agent struct {
    Name         string   `json:"name"`
    Model        string   `json:"model"`         // ref to a Model.ID
    SystemPrompt string   `json:"system_prompt"`
    Tools        []string `json:"tools,omitempty"`
    Description  string   `json:"description,omitempty"`
}

// HookEvent enumerates the points in the agent lifecycle where a
// hook may fire.
type HookEvent string

const (
    HookPreToolUse  HookEvent = "PreToolUse"
    HookPostToolUse HookEvent = "PostToolUse"
    HookOnError     HookEvent = "OnError"
    HookOnSession   HookEvent = "OnSession"
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

// LedgerConfig declares cost-accounting policy. Currency is the
// display currency for the TUI footer ("INR" or "USD"). UsdInrRate
// is multiplied by every USD cost to derive an INR cost; the rate
// is user-editable and refreshed manually via `bharatcode update-fx`
// (or left at the embedded default). MaxInr* fields cap spend at
// each window; a request that would exceed the cap triggers a
// confirmation dialog before proceeding.
type LedgerConfig struct {
    Currency          string  `json:"currency"`                  // "INR" or "USD"
    UsdInrRate        float64 `json:"usd_inr_rate"`              // e.g. 83.50
    MaxInrPerSession  float64 `json:"max_inr_per_session,omitempty"`
    MaxInrPerDay      float64 `json:"max_inr_per_day,omitempty"`
    MaxInrPerMonth    float64 `json:"max_inr_per_month,omitempty"`
}

// Options is a free-form bag of feature toggles that do not
// warrant their own struct (yet).
type Options struct {
    DisableProviderAutoUpdate bool          `json:"disable_provider_auto_update,omitempty"`
    DataDir                   string        `json:"data_dir,omitempty"`
    LogLevel                  string        `json:"log_level,omitempty"`   // "debug","info","warn","error"
    RequestTimeout            time.Duration `json:"request_timeout,omitempty"`
}

// Scope identifies which on-disk file a Save() targets.
type Scope int

const (
    ScopeGlobal  Scope = iota // ~/.config/bharatcode/config.json
    ScopeProject              // .bharatcode.json in the project root
)

// Load reads the embedded defaults, overlays the global file (if
// any), overlays the project file (if any, discovered by walking up
// from the current working directory looking for .bharatcode.json),
// resolves env-var interpolation in API keys and command args, and
// validates the result. Load never panics; configuration errors
// return a wrapped error with the offending file path and JSON
// pointer to the failing field.
func Load(ctx context.Context) (*Config, error)

// LoadFrom is like Load but accepts explicit file paths. Either
// path may be empty to skip that layer. Used by tests.
func LoadFrom(ctx context.Context, globalPath, projectPath string) (*Config, error)

// Save serialises cfg as indented JSON and writes it atomically to
// the file at scope. Sensitive fields (APIKeyEnv values, never the
// keys themselves) are preserved as `${ENV_NAME}` placeholders.
// Save creates the parent directory with 0o755 if missing. The
// file is written with 0o600 since it may contain machine-local
// paths (per AGENTS.md §4 octal perms).
func Save(ctx context.Context, cfg *Config, scope Scope) error

// Default returns the embedded built-in default configuration. The
// returned value is freshly allocated; callers may mutate it
// without affecting future calls.
func Default() *Config

// Validate reports the first validation error found in cfg, or nil
// if cfg is internally consistent. Validate is called by Load and
// exposed for tests and the `bharatcode config validate` command.
func Validate(cfg *Config) error

// GlobalPath returns the canonical path of the global config file
// for the current host: $XDG_CONFIG_HOME/bharatcode/config.json if
// set, else $HOME/.config/bharatcode/config.json on Unix, else
// %APPDATA%/bharatcode/config.json on Windows.
func GlobalPath() string

// ProjectPath returns the path of the nearest .bharatcode.json
// found by walking up from dir. Returns "" if no project file is
// found before reaching the filesystem root.
func ProjectPath(dir string) string
```

### Built-in defaults

`Default()` returns a `*Config` populated from `defaults/config.json` embedded via `//go:embed defaults/config.json`. The embedded file ships with these providers and a representative model per provider:

| Provider | Type | API key env | Default model | Context | $/Mtok in | $/Mtok out |
|---|---|---|---|---|---|---|
| anthropic | anthropic | `ANTHROPIC_API_KEY` | `claude-sonnet-4-5` | 200000 | 3.00 | 15.00 |
| openai | openai | `OPENAI_API_KEY` | `gpt-4o` | 128000 | 2.50 | 10.00 |
| deepseek | openai_compatible | `DEEPSEEK_API_KEY` | `deepseek-chat` (V3) | 64000 | 0.27 | 1.10 |
| deepseek | openai_compatible | `DEEPSEEK_API_KEY` | `deepseek-reasoner` (R1) | 64000 | 0.55 | 2.19 |
| moonshot | openai_compatible | `MOONSHOT_API_KEY` | `kimi-k2-0905-preview` | 200000 | 0.60 | 2.50 |
| groq | openai_compatible | `GROQ_API_KEY` | `llama-3.3-70b-versatile` | 128000 | 0.59 | 0.79 |
| groq | openai_compatible | `GROQ_API_KEY` | `qwen-2.5-coder-32b` | 128000 | 0.79 | 0.79 |
| together | openai_compatible | `TOGETHER_API_KEY` | `meta-llama/Llama-3.3-70B-Instruct-Turbo` | 128000 | 0.88 | 0.88 |
| fireworks | openai_compatible | `FIREWORKS_API_KEY` | `accounts/fireworks/models/qwen2p5-coder-32b-instruct` | 128000 | 0.90 | 0.90 |
| openrouter | openai_compatible | `OPENROUTER_API_KEY` | `anthropic/claude-sonnet-4-5` | 200000 | 3.00 | 15.00 |
| ollama | ollama | (none) | `qwen2.5-coder:32b` | 128000 | 0.00 | 0.00 |
| lmstudio | lmstudio | (none) | `qwen2.5-coder-32b-instruct` | 128000 | 0.00 | 0.00 |

Default `LedgerConfig`: `Currency: "INR"`, `UsdInrRate: 83.50`, no caps set (zero = unlimited). Default `PermConfig`: `AutoApprove: ["view","ls","grep","glob"]`, `AlwaysPrompt: ["bash","edit","write","multiedit","fetch"]`. Default agent: one entry named `"coder"` using `deepseek-chat`.

Prices and the FX rate are accurate as of 2026-05-01. Implementers should not chase the live numbers; the spec lists what the embedded file contains. `bharatcode update-providers` refreshes them at runtime from [models.dev](https://models.dev).

## Dependencies

- `internal/util` — `util.ExpandPath` resolves `~` and env vars in `Options.DataDir` and similar path fields; `util/fsext.AtomicWrite` writes config files safely; `util/fsext.EnsureDir` creates the parent directory.
- stdlib: `encoding/json`, `embed`, `os`, `path/filepath`, `errors`, `fmt`, `strings`, `regexp`, `context`, `time`.
- External: none in production code. The locked stack pins `github.com/spf13/viper` for config but **this module uses a custom merge instead**. See *Notes for the implementer* below for the reason and the architectural decision record path.

## Acceptance criteria

1. `go test ./internal/config/...` passes on linux, darwin, and windows runners.
2. `go test -race ./internal/config/...` passes.
3. `go test -cover ./internal/config/...` reports ≥ 90% statement coverage.
4. `golangci-lint run ./internal/config/...` is clean.
5. `Default()` returns a non-nil `*Config` for which `Validate(Default()) == nil`.
6. A test `TestZeroFileLoad` runs `LoadFrom(ctx, "", "")` and asserts the returned `*Config` deep-equals `Default()`.
7. A test `TestProjectOverridesGlobal` writes a global file with `Currency: "USD"` and a project file with `Currency: "INR"`, calls `LoadFrom`, and asserts the loaded `Ledger.Currency == "INR"`.
8. A test `TestProjectArrayReplacesGlobal` writes a global `providers: [A, B]` and project `providers: [C]`, asserts the merged result is `[C]` (slices replace, they do not append; document this on `Config.merge`).
9. A test `TestEnvVarInterpolation` sets `DEEPSEEK_API_KEY=sk-test` in `t.Setenv`, loads a config where `providers[0].api_key_env: "DEEPSEEK_API_KEY"` (the *name* of the env var, resolved by the LLM client at request time, not by Load), and verifies the loaded value is the literal `"DEEPSEEK_API_KEY"` — env-var values never enter the config struct.
10. A test `TestEnvVarInterpolationInBaseURL` sets `OLLAMA_HOST=http://10.0.0.1:11434`, loads a config with `providers[ollama].base_url: "${OLLAMA_HOST}"`, asserts the resolved struct has `BaseURL == "http://10.0.0.1:11434"`. `${VAR}` and `$VAR` forms both work, applied to string fields of `Provider.BaseURL`, `MCPServer.URL`, `MCPServer.Command`, and any `Env` map values.
11. A test `TestValidateRejectsDuplicateProviderNames` returns an error containing the duplicate name and the JSON pointer path (`/providers/1/name`).
12. A test `TestValidateRejectsModelReferencingMissingProvider` returns an error naming both the model ID and the missing provider.
13. A test `TestSaveRoundtrip` saves, reloads, and asserts the round-tripped config equals the input. Verify the on-disk file mode is `0o600` (skip on Windows).
14. A test `TestProjectPathWalksUp` creates `t.TempDir()/a/b/c/`, places `.bharatcode.json` at `a/`, calls `ProjectPath("…/a/b/c")`, asserts it returns `…/a/.bharatcode.json`. Returns `""` when no file is found before the filesystem root.
15. `go build ./...` succeeds with `CGO_ENABLED=0`.
16. The embedded defaults file `defaults/config.json` parses cleanly: `go run ./scripts/validate-defaults.go` exits 0 (script is a thin wrapper over `Default()` + `Validate`).

## Notes for the implementer

- **viper vs. custom merge.** AGENTS.md §2 locks the stack to `github.com/spf13/viper`. The user's spec for this module explicitly recommends a custom merge instead of viper. Both directives come from the same author; this module follows the user's per-module guidance (custom merge) because viper's auto-bind and case-insensitive matching produce surprising results on snake_case JSON and on nested arrays. Write the decision down in `docs/decisions/<today>-config-merge-strategy.md` so the deviation is auditable. Do not import `viper` from this package.
- The custom merge is a straightforward struct walk: for scalars project-wins, for slices project replaces global, for maps project values override per-key. Define `func (c *Config) merge(other *Config)` and unit-test it with table-driven cases.
- Env-var interpolation is regex-based: `\$\{?([A-Z_][A-Z0-9_]*)\}?` against any string field tagged `interpolate:"true"` *or* listed in a small allowlist (`Provider.BaseURL`, `MCPServer.URL`, `MCPServer.Command`, `MCPServer.Args[*]`, `LSPServer.Command`, `LSPServer.Args[*]`, `Options.DataDir`, all map values in `MCPServer.Env`). A missing variable resolves to the empty string and the field is recorded in a `warnings` slice returned alongside the config. Do NOT call `os.ExpandEnv` on the entire JSON blob; it eats `$schema` references and similar.
- API keys themselves are NEVER stored in the config struct. The struct stores the *name* of the env var (`APIKeyEnv`). The LLM client reads `os.Getenv(provider.APIKeyEnv)` at request time. This makes the config file safe to commit, share, or copy between machines.
- Embed defaults via `//go:embed defaults/config.json`. Parse on first call and cache in a package-level `sync.Once`-guarded variable. `Default()` returns a deep copy of the cached value (JSON marshal + unmarshal is acceptable; this is not a hot path).
- Validation rules:
  1. Every `Model.Provider` matches a `Provider.Name`.
  2. Every `Agent.Model` matches a `Model.ID`.
  3. Every `Hook.Event` is a known `HookEvent` constant.
  4. Provider names are unique within `Providers`.
  5. Model IDs are unique within `Models`.
  6. `LedgerConfig.Currency` is one of `"INR"`, `"USD"`.
  7. `LedgerConfig.UsdInrRate > 0`.
  8. All `MaxInr*` fields are `>= 0`.
  9. `ProviderType` is one of the five constants.
  10. `MCPServer.Transport` is one of `"stdio"`, `"http"`, `"sse"`.
- All error messages name the file path (`global` / `project`) and a JSON pointer to the failing field (e.g. `/providers/1/name`). Wrap with `fmt.Errorf("validating %s: %w", path, err)`.
- Tests use `t.TempDir()` for config-file paths, `t.Setenv()` for env vars, `httptest.NewServer` for any future remote-config fetch, and `github.com/stretchr/testify/require` for assertions (per AGENTS.md §4-§5). Never write to the user's real `~/.config/bharatcode/` from a test.
- File permissions are octal literals (`0o600` for config files, `0o755` for parent directories) per AGENTS.md §4.
- Comments end in a period; doc comments capitalise the first word and identify the function they document.
- This module imports only `internal/util`. It must not import `internal/db`, `internal/llm`, or anything else — the dependency graph runs `util → config → llm → agent`, not the other way around.

## Implementation status

- **Status:** Completed
- **Files created:**
  - `internal/config/config.go`: Struct definitions for the configuration schema and custom JSON duration marshaling.
  - `internal/config/defaults/config.json`: Default configuration with standard providers, models, permissions, ledger, and agents.
  - `internal/config/paths.go`: Global and project configuration path resolution.
  - `internal/config/validate.go`: Configuration validation logic.
  - `internal/config/load.go`: Merging logic and environment variable interpolation.
  - `internal/config/store.go`: Atomic configuration save logic using `util/fsext`.
  - `internal/config/config_test.go`: Complete test suite with 93.2% statement coverage.
  - `scripts/validate-defaults.go`: Validation runner for the embedded default config.
  - `docs/decisions/2026-05-26-config-merge-strategy.md`: ADR on using a custom merge strategy rather than Viper.
- **Line Count:**
  - `internal/config/`: ~1450 lines (including tests and default config)
- **Test Pass Count:** 21 passing test suites and subtests.
- **Statement Coverage:** 93.2% statement coverage for the `internal/config` package.
- **Deviations:** None. The Viper dependency was bypassed in favor of a custom merge strategy as recommended by the spec and documented in the ADR.
