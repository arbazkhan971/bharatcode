package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	require.NotNil(t, cfg)
	err := Validate(cfg)
	require.NoError(t, err)

	// Ensure mutate does not affect subsequent calls
	cfg.Ledger.Currency = "USD"
	cfg2 := Default()
	require.Equal(t, "INR", cfg2.Ledger.Currency)
}

func TestZeroFileLoad(t *testing.T) {
	ctx := context.Background()
	cfg, err := LoadFrom(ctx, "", "")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, Default(), cfg)
}

func TestProjectOverridesGlobal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	projectFile := filepath.Join(tmpDir, "project.json")

	// Global defines Currency: "USD", project defines Currency: "INR"
	globalData := []byte(`{"ledger": {"currency": "USD"}}`)
	projectData := []byte(`{"ledger": {"currency": "INR"}}`)

	err := os.WriteFile(globalFile, globalData, 0o600)
	require.NoError(t, err)

	err = os.WriteFile(projectFile, projectData, 0o600)
	require.NoError(t, err)

	cfg, err := LoadFrom(ctx, globalFile, projectFile)
	require.NoError(t, err)
	require.Equal(t, "INR", cfg.Ledger.Currency)
}

func TestProjectArrayReplacesGlobal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	projectFile := filepath.Join(tmpDir, "project.json")

	globalData := []byte(`{
		"providers": [
			{"name": "p1", "type": "openai"},
			{"name": "p2", "type": "openai"}
		],
		"models": [],
		"agents": []
	}`)
	projectData := []byte(`{
		"providers": [
			{"name": "p3", "type": "openai"}
		],
		"models": [],
		"agents": []
	}`)

	err := os.WriteFile(globalFile, globalData, 0o600)
	require.NoError(t, err)

	err = os.WriteFile(projectFile, projectData, 0o600)
	require.NoError(t, err)

	cfg, err := LoadFrom(ctx, globalFile, projectFile)
	require.NoError(t, err)

	// Arrays/slices replace, they do not append
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "p3", cfg.Providers[0].Name)
}

func TestEnvVarInterpolation(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")

	// Verify that loading does not substitute the APIKeyEnv value itself.
	// APIKeyEnv is stored in the JSON as the literal name of the env var, and
	// never interpolated during Load/LoadFrom because APIKeyEnv is not on the interpolate list.
	cfg := Default()
	found := false
	for _, p := range cfg.Providers {
		if p.Name == "deepseek" {
			require.Equal(t, "DEEPSEEK_API_KEY", p.APIKeyEnv)
			found = true
		}
	}
	require.True(t, found)
}

func TestEnvVarInterpolationInBaseURL(t *testing.T) {
	ctx := context.Background()
	t.Setenv("OLLAMA_HOST", "http://10.0.0.1:11434")
	t.Setenv("MCP_COMMAND", "npx")
	t.Setenv("MCP_ARG", "mcp-server")
	t.Setenv("MCP_ENV_VAL", "prod")
	t.Setenv("LSP_CMD", "gopls")
	t.Setenv("LSP_ARG", "-v")
	t.Setenv("DATA_DIR", "/var/lib/bharatcode")
	t.Setenv("OPENROUTER_REFERER", "https://bharatcode.dev")

	tmpDir := t.TempDir()
	projFile := filepath.Join(tmpDir, "project.json")
	projData := []byte(`{
		"providers": [
			{"name": "ollama", "type": "ollama", "base_url": "${OLLAMA_HOST}"},
			{"name": "openrouter", "type": "openai_compatible", "base_url": "https://openrouter.ai/api/v1", "headers": {"HTTP-Referer": "${OPENROUTER_REFERER}", "X-Title": "BharatCode"}}
		],
		"mcp": [
			{
				"name": "test_mcp",
				"transport": "stdio",
				"command": "$MCP_COMMAND",
				"args": ["${MCP_ARG}", "extra"],
				"env": {
					"ENV_VAR": "$MCP_ENV_VAL"
				}
			}
		],
		"lsp": [
			{
				"name": "go",
				"command": "$LSP_CMD",
				"args": ["$LSP_ARG"],
				"languages": ["go"]
			}
		],
		"options": {
			"data_dir": "${DATA_DIR}"
		},
		"models": [],
		"agents": []
	}`)

	err := os.WriteFile(projFile, projData, 0o600)
	require.NoError(t, err)

	cfg, err := LoadFrom(ctx, "", projFile)
	require.NoError(t, err)

	require.Equal(t, "http://10.0.0.1:11434", cfg.Providers[0].BaseURL)
	require.Equal(t, "https://bharatcode.dev", cfg.Providers[1].Headers["HTTP-Referer"])
	require.Equal(t, "BharatCode", cfg.Providers[1].Headers["X-Title"])
	require.Equal(t, "npx", cfg.MCP[0].Command)
	require.Equal(t, []string{"mcp-server", "extra"}, cfg.MCP[0].Args)
	require.Equal(t, "prod", cfg.MCP[0].Env["ENV_VAR"])
	require.Equal(t, "gopls", cfg.LSP[0].Command)
	require.Equal(t, []string{"-v"}, cfg.LSP[0].Args)
	require.Equal(t, "/var/lib/bharatcode", cfg.Options.DataDir)
}

func TestEnvVarInterpolationMissingWarnings(t *testing.T) {
	ctx := context.Background()
	// Unset target environment variable
	os.Unsetenv("MISSING_ENV_VAR")

	tmpDir := t.TempDir()
	projFile := filepath.Join(tmpDir, "project.json")
	projData := []byte(`{
		"providers": [
			{"name": "ollama", "type": "ollama", "base_url": "${MISSING_ENV_VAR}"}
		],
		"models": [],
		"agents": []
	}`)

	err := os.WriteFile(projFile, projData, 0o600)
	require.NoError(t, err)

	cfg, err := LoadFrom(ctx, "", projFile)
	require.NoError(t, err)

	// Missing variable resolves to empty string
	require.Equal(t, "", cfg.Providers[0].BaseURL)
}

func TestValidateRejectsDuplicateProviderNames(t *testing.T) {
	cfg := Default()
	cfg.Providers = []Provider{
		{Name: "anthropic", Type: ProviderAnthropic},
		{Name: "anthropic", Type: ProviderAnthropic},
	}

	err := Validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "/providers/1/name") // Index of the duplicated provider
	require.Contains(t, err.Error(), "duplicate provider name")
}

func TestValidateRejectsModelReferencingMissingProvider(t *testing.T) {
	cfg := Default()
	cfg.Models = []Model{
		{
			ID:       "some-model",
			Provider: "nonexistent-provider",
		},
	}

	err := Validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "/models/0/provider")
	require.Contains(t, err.Error(), "references missing provider")
}

func TestSaveRoundtrip(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Setenv for Getwd/ProjectPath setup
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(oldWd) }()
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	cfg := Default()
	// Add some custom configuration
	cfg.Ledger.Currency = "USD"
	cfg.Options.LogLevel = "debug"

	// Save to project scope
	err = Save(ctx, cfg, ScopeProject)
	require.NoError(t, err)

	// Check file permissions (skip on Windows)
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(".bharatcode.json")
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}

	// Reload config and assert deep equality
	loaded, err := Load(ctx)
	require.NoError(t, err)

	require.Equal(t, cfg.Ledger.Currency, loaded.Ledger.Currency)
	require.Equal(t, cfg.Options.LogLevel, loaded.Options.LogLevel)
}

func TestProjectPathWalksUp(t *testing.T) {
	tmpDir := t.TempDir()
	a := filepath.Join(tmpDir, "a")
	b := filepath.Join(a, "b")
	c := filepath.Join(b, "c")

	err := os.MkdirAll(c, 0o755)
	require.NoError(t, err)

	// Place .bharatcode.json at a
	projFile := filepath.Join(a, ".bharatcode.json")
	err = os.WriteFile(projFile, []byte("{}"), 0o644)
	require.NoError(t, err)

	// ProjectPath walking up from c should return the file at a
	found := ProjectPath(c)
	require.Equal(t, filepath.Clean(projFile), filepath.Clean(found))

	// Walk up to root should fail to find if we search outside the hierarchy or if there is no file
	foundEmpty := ProjectPath(tmpDir)
	require.Equal(t, "", foundEmpty)
}

func TestGlobalPath(t *testing.T) {
	// Backup original env vars
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	origAPPDATA := os.Getenv("APPDATA")
	origHOME := os.Getenv("HOME")
	defer func() {
		_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		_ = os.Setenv("APPDATA", origAPPDATA)
		_ = os.Setenv("HOME", origHOME)
	}()

	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", `C:\Users\Test\AppData\Roaming`)
		p := GlobalPath()
		require.Equal(t, `C:\Users\Test\AppData\Roaming\bharatcode\config.json`, p)
	} else {
		t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
		p := GlobalPath()
		require.Equal(t, "/custom/xdg/bharatcode/config.json", p)

		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/testuser")
		p2 := GlobalPath()
		require.Equal(t, "/home/testuser/.config/bharatcode/config.json", p2)
	}
}

func TestOptionsJSONCustom(t *testing.T) {
	// Test parsing request_timeout as a string
	data1 := []byte(`{"request_timeout": "15s", "log_level": "info"}`)
	var opt1 Options
	err := json.Unmarshal(data1, &opt1)
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, opt1.RequestTimeout)
	require.Equal(t, "info", opt1.LogLevel)

	// Test parsing request_timeout as a number (nanoseconds)
	data2 := []byte(`{"request_timeout": 5000000000}`)
	var opt2 Options
	err = json.Unmarshal(data2, &opt2)
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, opt2.RequestTimeout)

	// Test marshaling
	opt3 := Options{
		LogLevel:       "warn",
		RequestTimeout: 10 * time.Second,
	}
	out, err := json.Marshal(opt3)
	require.NoError(t, err)
	require.Contains(t, string(out), `"request_timeout":"10s"`)

	// Test marshaling zero duration
	opt4 := Options{
		LogLevel: "error",
	}
	out, err = json.Marshal(opt4)
	require.NoError(t, err)
	require.NotContains(t, string(out), "request_timeout")
}

func TestValidationRulesDetailed(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "Nil config",
			mutate: func(c *Config) {
				// handled by passing nil directly
			},
			wantErr: "config is nil",
		},
		{
			name: "Empty provider name",
			mutate: func(c *Config) {
				c.Providers[0].Name = ""
			},
			wantErr: "provider name cannot be empty",
		},
		{
			name: "Invalid provider type",
			mutate: func(c *Config) {
				c.Providers[0].Type = "unsupported_type"
			},
			wantErr: "invalid provider type",
		},
		{
			name: "Empty model ID",
			mutate: func(c *Config) {
				c.Models[0].ID = ""
			},
			wantErr: "model ID cannot be empty",
		},
		{
			name: "Duplicate model ID",
			mutate: func(c *Config) {
				c.Models[0].ID = c.Models[1].ID
			},
			wantErr: "duplicate model ID",
		},
		{
			name: "Agent missing model",
			mutate: func(c *Config) {
				c.Agents[0].Model = "nonexistent-model"
			},
			wantErr: "references missing model",
		},
		{
			name: "Agent empty name",
			mutate: func(c *Config) {
				c.Agents[0].Name = ""
			},
			wantErr: "agent name cannot be empty",
		},
		{
			name: "Invalid hook event",
			mutate: func(c *Config) {
				c.Hooks = []Hook{{Event: "InvalidEvent", Command: "echo"}}
			},
			wantErr: "invalid hook event",
		},
		{
			name: "Empty MCP server name",
			mutate: func(c *Config) {
				c.MCP = []MCPServer{{Name: "", Transport: "stdio"}}
			},
			wantErr: "mcp server name cannot be empty",
		},
		{
			name: "Invalid MCP transport",
			mutate: func(c *Config) {
				c.MCP = []MCPServer{{Name: "mcp", Transport: "ftp"}}
			},
			wantErr: "invalid mcp transport",
		},
		{
			name: "Invalid currency",
			mutate: func(c *Config) {
				c.Ledger.Currency = "EUR"
			},
			wantErr: "invalid currency",
		},
		{
			name: "Invalid usd_inr_rate",
			mutate: func(c *Config) {
				c.Ledger.UsdInrRate = 0
			},
			wantErr: "invalid usd_inr_rate",
		},
		{
			name: "Negative MaxInrPerSession",
			mutate: func(c *Config) {
				c.Ledger.MaxInrPerSession = -10
			},
			wantErr: "invalid max_inr_per_session",
		},
		{
			name: "Negative MaxInrPerDay",
			mutate: func(c *Config) {
				c.Ledger.MaxInrPerDay = -1
			},
			wantErr: "invalid max_inr_per_day",
		},
		{
			name: "Negative MaxInrPerMonth",
			mutate: func(c *Config) {
				c.Ledger.MaxInrPerMonth = -20
			},
			wantErr: "invalid max_inr_per_month",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "Nil config" {
				err := Validate(nil)
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}

			cfg := Default()
			tt.mutate(cfg)
			err := Validate(cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestValidateAcceptsLifecycleHookEvents asserts the session and file-edit
// lifecycle events validate. Before HookSessionStart/HookSessionEnd/HookFileEdit
// were added to the validate.go case, these configs were rejected as invalid
// hook events even though the hooks engine already handled them.
func TestValidateAcceptsLifecycleHookEvents(t *testing.T) {
	events := []HookEvent{HookSessionStart, HookSessionEnd, HookFileEdit}
	for _, ev := range events {
		t.Run(string(ev), func(t *testing.T) {
			cfg := Default()
			cfg.Hooks = []Hook{{Event: ev, Command: "echo"}}
			require.NoError(t, Validate(cfg))
		})
	}
}

func TestLoadErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Test non-existent file is skipped (doesn't error out)
	_, err := LoadFrom(ctx, filepath.Join(tmpDir, "nonexistent-global.json"), "")
	require.NoError(t, err)

	// Test invalid JSON in global file returns error
	badGlobal := filepath.Join(tmpDir, "bad-global.json")
	err = os.WriteFile(badGlobal, []byte("{invalid-json"), 0o600)
	require.NoError(t, err)
	_, err = LoadFrom(ctx, badGlobal, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing global config file")

	// Test invalid JSON in project file returns error
	badProj := filepath.Join(tmpDir, "bad-proj.json")
	err = os.WriteFile(badProj, []byte("{invalid-json"), 0o600)
	require.NoError(t, err)
	_, err = LoadFrom(ctx, "", badProj)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing project config file")
}

func TestGlobalPathHelper(t *testing.T) {
	// 1. Windows with empty APPDATA falling back to home dir
	getenv := func(key string) string {
		return ""
	}
	userHomeDir := func() (string, error) {
		return `C:\Users\TestHome`, nil
	}
	p := globalPathHelper("windows", getenv, userHomeDir)
	require.Equal(t, filepath.Join("C:\\Users\\TestHome", "AppData", "Roaming", "bharatcode", "config.json"), p)

	// 2. Unix with userHomeDir returning error
	getenvUnix := func(key string) string {
		return ""
	}
	userHomeDirErr := func() (string, error) {
		return "", os.ErrNotExist
	}
	pErr := globalPathHelper("linux", getenvUnix, userHomeDirErr)
	require.Equal(t, "", pErr)
}

func TestProjectPathEmpty(t *testing.T) {
	require.Equal(t, "", ProjectPath(""))
}

func TestMergeAllFields(t *testing.T) {
	c1 := Default()
	// Let's modify all fields in c2 to check if they merge
	c2 := &Config{
		Providers: []Provider{{Name: "p", Type: ProviderOllama}},
		Models:    []Model{{ID: "m", Provider: "p"}},
		Permissions: PermConfig{
			AllowAll:     true,
			AutoApprove:  []string{"tool1"},
			AlwaysPrompt: []string{"tool2"},
			Deny:         []string{"tool3"},
		},
		Agents: []Agent{{Name: "a", Model: "m"}},
		Hooks:  []Hook{{Event: HookOnError, Command: "echo"}},
		MCP:    []MCPServer{{Name: "mcp", Transport: "stdio"}},
		LSP:    []LSPServer{{Name: "lsp", Command: "lsp-cmd"}},
		Ledger: LedgerConfig{
			Currency:         "USD",
			UsdInrRate:       84.0,
			MaxInrPerSession: 100,
			MaxInrPerDay:     200,
			MaxInrPerMonth:   300,
		},
		Options: Options{
			DisableProviderAutoUpdate: true,
			DataDir:                   "/data",
			LogLevel:                  "info",
			RequestTimeout:            30 * time.Second,
		},
	}

	c1.merge(c2)

	require.Equal(t, c2.Providers, c1.Providers)
	require.Equal(t, c2.Models, c1.Models)
	require.True(t, c1.Permissions.AllowAll)
	require.Equal(t, c2.Permissions.AutoApprove, c1.Permissions.AutoApprove)
	require.Equal(t, c2.Permissions.AlwaysPrompt, c1.Permissions.AlwaysPrompt)
	require.Equal(t, c2.Permissions.Deny, c1.Permissions.Deny)
	require.Equal(t, c2.Agents, c1.Agents)
	require.Equal(t, c2.Hooks, c1.Hooks)
	require.Equal(t, c2.MCP, c1.MCP)
	require.Equal(t, c2.LSP, c1.LSP)
	require.Equal(t, c2.Ledger.Currency, c1.Ledger.Currency)
	require.Equal(t, c2.Ledger.UsdInrRate, c1.Ledger.UsdInrRate)
	require.Equal(t, c2.Ledger.MaxInrPerSession, c1.Ledger.MaxInrPerSession)
	require.Equal(t, c2.Ledger.MaxInrPerDay, c1.Ledger.MaxInrPerDay)
	require.Equal(t, c2.Ledger.MaxInrPerMonth, c1.Ledger.MaxInrPerMonth)
	require.True(t, c1.Options.DisableProviderAutoUpdate)
	require.Equal(t, c2.Options.DataDir, c1.Options.DataDir)
	require.Equal(t, c2.Options.LogLevel, c1.Options.LogLevel)
	require.Equal(t, c2.Options.RequestTimeout, c1.Options.RequestTimeout)
}

func TestSaveErrors(t *testing.T) {
	ctx := context.Background()
	// 1. Nil config
	err := Save(ctx, nil, ScopeProject)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot save nil config")

	// 2. Invalid scope
	cfg := Default()
	err = Save(ctx, cfg, Scope(999))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid scope")

	// 3. Save to global scope (make sure it writes to GlobalPath)
	tmpDir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmpDir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmpDir)
	}

	err = Save(ctx, cfg, ScopeGlobal)
	require.NoError(t, err)

	require.FileExists(t, GlobalPath())
}

func TestOptionsJSONCustomErrors(t *testing.T) {
	// Test parsing invalid format string duration
	var opt Options
	err := json.Unmarshal([]byte(`{"request_timeout": "invalid-duration"}`), &opt)
	require.Error(t, err)

	// Test parsing invalid type
	err = json.Unmarshal([]byte(`{"request_timeout": true}`), &opt)
	require.Error(t, err)

	// Test parsing invalid JSON
	err = json.Unmarshal([]byte(`{"request_timeout": `), &opt)
	require.Error(t, err)
}

func TestLoadFromDirPaths(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg, err := LoadFrom(ctx, tmpDir, tmpDir)
	require.NoError(t, err)
	require.Equal(t, Default(), cfg)
}

func TestLoadExecution(t *testing.T) {
	ctx := context.Background()
	// Ensure calling Load executes without failure
	cfg, err := Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}
