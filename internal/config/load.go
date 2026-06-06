package config

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

//go:embed defaults/config.json
var defaultJSON []byte

var (
	defaultConfig *Config
	defaultOnce   sync.Once
	defaultErr    error
)

var envRegex = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)

// initDefault parses the embedded default configuration once.
func initDefault() {
	defaultOnce.Do(func() {
		var cfg Config
		if err := json.Unmarshal(defaultJSON, &cfg); err != nil {
			defaultErr = fmt.Errorf("parsing embedded default config: %w", err)
			return
		}
		defaultConfig = &cfg
	})
}

// Default returns a freshly allocated deep copy of the embedded default configuration.
func Default() *Config {
	initDefault()
	if defaultErr != nil {
		panic(defaultErr)
	}

	// Create a deep copy of defaultConfig using JSON marshal and unmarshal.
	data, err := json.Marshal(defaultConfig)
	if err != nil {
		panic(fmt.Errorf("marshaling default config for deep copy: %w", err))
	}

	var copyConfig Config
	if err := json.Unmarshal(data, &copyConfig); err != nil {
		panic(fmt.Errorf("unmarshaling default config for deep copy: %w", err))
	}
	populateLedgerModels(&copyConfig)

	return &copyConfig
}

// populateLedgerModels populates Ledger.Models map from the Models slice.
func populateLedgerModels(cfg *Config) {
	cfg.Ledger.Models = make(map[string]ModelPricing)
	for _, m := range cfg.Models {
		key := m.Provider + "/" + m.ID
		cfg.Ledger.Models[key] = ModelPricing{
			InputPricePerMTokUSD:  m.InputPricePerMTokUSD,
			OutputPricePerMTokUSD: m.OutputPricePerMTokUSD,
		}
	}
}

// Load reads the embedded defaults, overlays the global file (if
// any), overlays the project file (if any, discovered by walking up
// from the current working directory looking for .bharatcode.json),
// resolves env-var interpolation in API keys and command args, and
// validates the result. Load never panics; configuration errors
// return a wrapped error with the offending file path and JSON
// pointer to the failing field.
func Load(ctx context.Context) (*Config, error) {
	cwd, err := os.Getwd()
	var projPath string
	if err == nil {
		projPath = ProjectPath(cwd)
	}
	return LoadFrom(ctx, GlobalPath(), projPath)
}

// LoadFrom is like Load but accepts explicit file paths. Either
// path may be empty to skip that layer. Used by tests.
func LoadFrom(ctx context.Context, globalPath, projectPath string) (*Config, error) {
	return loadFromWithProfile(ctx, globalPath, projectPath, "")
}

// ProfilePath returns the canonical path of the named profile overlay
// file: <name>.json under the global config directory (the same
// directory that holds the global config.json). The name is the bare
// profile name without extension.
func ProfilePath(name string) string {
	return filepath.Join(filepath.Dir(GlobalPath()), name+".json")
}

// LoadWithProfile behaves like Load but additionally overlays the
// named profile file (<name>.json under the global config directory)
// on top of the merged global and project configuration, so profile
// values win. An empty name reproduces Load exactly. A named profile
// whose file is absent is an error, because selecting a profile is an
// explicit request rather than an optional layer.
func LoadWithProfile(ctx context.Context, profileName string) (*Config, error) {
	cwd, err := os.Getwd()
	var projPath string
	if err == nil {
		projPath = ProjectPath(cwd)
	}
	var profilePath string
	if profileName != "" {
		profilePath = ProfilePath(profileName)
	}
	return loadFromWithProfile(ctx, GlobalPath(), projPath, profilePath)
}

// LoadFromWithProfile is like LoadFrom but additionally overlays the named
// profile file on top of the merged global and project configuration. An
// empty profileName skips the profile layer, reproducing LoadFrom exactly.
// A non-empty profileName whose file is absent returns an error.
func LoadFromWithProfile(ctx context.Context, globalPath, projectPath, profileName string) (*Config, error) {
	var profilePath string
	if profileName != "" {
		profilePath = ProfilePath(profileName)
	}
	return loadFromWithProfile(ctx, globalPath, projectPath, profilePath)
}

// loadFromWithProfile is the shared core of LoadFrom and
// LoadWithProfile. profilePath may be empty to skip the profile
// overlay; when non-empty the file must exist.
func loadFromWithProfile(ctx context.Context, globalPath, projectPath, profilePath string) (*Config, error) {
	cfg := Default()

	// Overlay global config if provided and exists.
	if globalPath != "" {
		if fi, err := os.Stat(globalPath); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(globalPath)
			if err != nil {
				return nil, fmt.Errorf("reading global config file %s: %w", globalPath, err)
			}
			var globalCfg Config
			if err := json.Unmarshal(data, &globalCfg); err != nil {
				return nil, fmt.Errorf("parsing global config file %s: %w", globalPath, err)
			}
			cfg.merge(&globalCfg)
		}
	}

	// Overlay project config if provided and exists.
	if projectPath != "" {
		if fi, err := os.Stat(projectPath); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(projectPath)
			if err != nil {
				return nil, fmt.Errorf("reading project config file %s: %w", projectPath, err)
			}
			var projectCfg Config
			if err := json.Unmarshal(data, &projectCfg); err != nil {
				return nil, fmt.Errorf("parsing project config file %s: %w", projectPath, err)
			}
			cfg.merge(&projectCfg)
		}
	}

	// Overlay the profile config if requested. A named profile must
	// exist; profile values win over global and project layers.
	if profilePath != "" {
		fi, err := os.Stat(profilePath)
		if err != nil || fi.IsDir() {
			return nil, fmt.Errorf("reading profile config file %s: %w", profilePath, os.ErrNotExist)
		}
		data, err := os.ReadFile(profilePath)
		if err != nil {
			return nil, fmt.Errorf("reading profile config file %s: %w", profilePath, err)
		}
		var profileCfg Config
		if err := json.Unmarshal(data, &profileCfg); err != nil {
			return nil, fmt.Errorf("parsing profile config file %s: %w", profilePath, err)
		}
		cfg.merge(&profileCfg)
	}

	// Resolve env-var interpolation.
	warnings := cfg.interpolate()
	for _, warn := range warnings {
		slog.Warn("Environment variable warning during config interpolation", "warning", warn)
	}

	// Validate the final merged configuration.
	var errPath string
	if profilePath != "" {
		errPath = profilePath
	} else if projectPath != "" {
		errPath = projectPath
	} else if globalPath != "" {
		errPath = globalPath
	} else {
		errPath = "defaults"
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validating %s: %w", errPath, err)
	}

	populateLedgerModels(cfg)

	return cfg, nil
}

// merge overlays other config settings onto c.
// Slices replace existing slices; non-zero scalars are overwritten.
func (c *Config) merge(other *Config) {
	if other == nil {
		return
	}

	if other.Providers != nil {
		c.Providers = other.Providers
	}
	if other.Models != nil {
		c.Models = other.Models
	}

	// PermConfig merge
	if other.Permissions.AllowAll {
		c.Permissions.AllowAll = true
	}
	if other.Permissions.AutoApprove != nil {
		c.Permissions.AutoApprove = other.Permissions.AutoApprove
	}
	if other.Permissions.AlwaysPrompt != nil {
		c.Permissions.AlwaysPrompt = other.Permissions.AlwaysPrompt
	}
	if other.Permissions.Deny != nil {
		c.Permissions.Deny = other.Permissions.Deny
	}
	if other.Permissions.Remembered != nil {
		if c.Permissions.Remembered == nil {
			c.Permissions.Remembered = make(map[string]string)
		}
		for k, v := range other.Permissions.Remembered {
			c.Permissions.Remembered[k] = v
		}
	}

	if other.Agents != nil {
		c.Agents = other.Agents
	}
	if other.Hooks != nil {
		c.Hooks = other.Hooks
	}
	if other.MCP != nil {
		c.MCP = other.MCP
	}
	if other.LSP != nil {
		c.LSP = other.LSP
	}

	// LedgerConfig merge
	if other.Ledger.Currency != "" {
		c.Ledger.Currency = other.Ledger.Currency
	}
	if other.Ledger.UsdInrRate != 0 {
		c.Ledger.UsdInrRate = other.Ledger.UsdInrRate
	}
	if other.Ledger.MaxInrPerSession != 0 {
		c.Ledger.MaxInrPerSession = other.Ledger.MaxInrPerSession
	}
	if other.Ledger.MaxInrPerDay != 0 {
		c.Ledger.MaxInrPerDay = other.Ledger.MaxInrPerDay
	}
	if other.Ledger.MaxInrPerMonth != 0 {
		c.Ledger.MaxInrPerMonth = other.Ledger.MaxInrPerMonth
	}

	// Options merge
	if other.Options.DisableProviderAutoUpdate {
		c.Options.DisableProviderAutoUpdate = true
	}
	if other.Options.DataDir != "" {
		c.Options.DataDir = other.Options.DataDir
	}
	if other.Options.LogLevel != "" {
		c.Options.LogLevel = other.Options.LogLevel
	}
	if other.Options.RequestTimeout != 0 {
		c.Options.RequestTimeout = other.Options.RequestTimeout
	}

	// SandboxConfig merge
	if other.Sandbox.Mode != "" {
		c.Sandbox.Mode = other.Sandbox.Mode
	}

	// CacheConfig merge
	if other.Cache.Enabled {
		c.Cache.Enabled = true
	}
	if other.Cache.MaxEntries != 0 {
		c.Cache.MaxEntries = other.Cache.MaxEntries
	}

	// RoutingConfig merge
	if other.Routing.Enabled {
		c.Routing.Enabled = true
	}
	if other.Routing.PromptLenThreshold != 0 {
		c.Routing.PromptLenThreshold = other.Routing.PromptLenThreshold
	}
	if other.Routing.ToolsImplyComplex {
		c.Routing.ToolsImplyComplex = true
	}
}

// interpolate performs environment variable expansion on specific fields.
func (c *Config) interpolate() []string {
	var warnings []string

	for i := range c.Providers {
		c.Providers[i].BaseURL = interpolateString(c.Providers[i].BaseURL, &warnings, fmt.Sprintf("providers[%d].base_url", i))
		for k, v := range c.Providers[i].Headers {
			c.Providers[i].Headers[k] = interpolateString(v, &warnings, fmt.Sprintf("providers[%d].headers[%s]", i, k))
		}
	}

	for i := range c.MCP {
		c.MCP[i].URL = interpolateString(c.MCP[i].URL, &warnings, fmt.Sprintf("mcp[%d].url", i))
		c.MCP[i].Command = interpolateString(c.MCP[i].Command, &warnings, fmt.Sprintf("mcp[%d].command", i))
		for j := range c.MCP[i].Args {
			c.MCP[i].Args[j] = interpolateString(c.MCP[i].Args[j], &warnings, fmt.Sprintf("mcp[%d].args[%d]", i, j))
		}
		if c.MCP[i].Env != nil {
			for k, v := range c.MCP[i].Env {
				c.MCP[i].Env[k] = interpolateString(v, &warnings, fmt.Sprintf("mcp[%d].env[%s]", i, k))
			}
		}
	}

	for i := range c.LSP {
		c.LSP[i].Command = interpolateString(c.LSP[i].Command, &warnings, fmt.Sprintf("lsp[%d].command", i))
		for j := range c.LSP[i].Args {
			c.LSP[i].Args[j] = interpolateString(c.LSP[i].Args[j], &warnings, fmt.Sprintf("lsp[%d].args[%d]", i, j))
		}
	}

	c.Options.DataDir = interpolateString(c.Options.DataDir, &warnings, "options.data_dir")

	return warnings
}

// interpolateString resolves env var placeholders in a string.
func interpolateString(s string, warnings *[]string, fieldName string) string {
	if s == "" {
		return ""
	}
	return envRegex.ReplaceAllStringFunc(s, func(match string) string {
		submatches := envRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		varName := submatches[1]
		val, exists := os.LookupEnv(varName)
		if !exists {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("environment variable %q is not set for field %q", varName, fieldName))
			}
			return ""
		}
		return val
	})
}
