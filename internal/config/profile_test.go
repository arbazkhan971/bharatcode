package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadWithProfileOverridesMergedConfig(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	profileFile := filepath.Join(tmpDir, "work.json")

	// Base (global) sets currency=USD and a distinctive usd_inr_rate.
	globalData := []byte(`{"ledger": {"currency": "USD", "usd_inr_rate": 80.0}}`)
	require.NoError(t, os.WriteFile(globalFile, globalData, 0o600))

	// Profile overrides only currency -> INR; leaves usd_inr_rate alone.
	profileData := []byte(`{"ledger": {"currency": "INR"}}`)
	require.NoError(t, os.WriteFile(profileFile, profileData, 0o600))

	cfg, err := loadFromWithProfile(ctx, globalFile, "", profileFile)
	require.NoError(t, err)

	// Overridden field takes the profile's value.
	require.Equal(t, "INR", cfg.Ledger.Currency)
	// Non-overridden field retains the base (global) value.
	require.Equal(t, 80.0, cfg.Ledger.UsdInrRate)
}

func TestLoadWithProfileWinsOverProject(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	projectFile := filepath.Join(tmpDir, "project.json")
	profileFile := filepath.Join(tmpDir, "prod.json")

	require.NoError(t, os.WriteFile(globalFile, []byte(`{"ledger": {"currency": "USD"}}`), 0o600))
	require.NoError(t, os.WriteFile(projectFile, []byte(`{"ledger": {"max_inr_per_day": 500}}`), 0o600))
	// Profile overrides currency; project's max_inr_per_day must survive.
	require.NoError(t, os.WriteFile(profileFile, []byte(`{"ledger": {"currency": "INR"}}`), 0o600))

	cfg, err := loadFromWithProfile(ctx, globalFile, projectFile, profileFile)
	require.NoError(t, err)

	require.Equal(t, "INR", cfg.Ledger.Currency)
	require.Equal(t, 500.0, cfg.Ledger.MaxInrPerDay)
}

func TestLoadWithoutProfileUnchanged(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	require.NoError(t, os.WriteFile(globalFile, []byte(`{"ledger": {"currency": "USD"}}`), 0o600))

	// Empty profile path must reproduce LoadFrom exactly.
	withEmpty, err := loadFromWithProfile(ctx, globalFile, "", "")
	require.NoError(t, err)

	viaLoadFrom, err := LoadFrom(ctx, globalFile, "")
	require.NoError(t, err)

	require.Equal(t, viaLoadFrom, withEmpty)
	require.Equal(t, "USD", withEmpty.Ledger.Currency)
}

func TestLoadWithProfileMissingFileErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "global.json")
	require.NoError(t, os.WriteFile(globalFile, []byte(`{"ledger": {"currency": "USD"}}`), 0o600))

	missing := filepath.Join(tmpDir, "does-not-exist.json")
	_, err := loadFromWithProfile(ctx, globalFile, "", missing)
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading profile config file")
}

func TestLoadWithProfileInvalidJSONErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	profileFile := filepath.Join(tmpDir, "bad.json")
	require.NoError(t, os.WriteFile(profileFile, []byte("{invalid-json"), 0o600))

	_, err := loadFromWithProfile(ctx, "", "", profileFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing profile config file")
}

func TestLoadFromWithProfileByNameResolvesSibling(t *testing.T) {
	ctx := context.Background()
	// Control GlobalPath() by pointing XDG_CONFIG_HOME at a temp dir.
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Write a global config under the standard path derived from XDG_CONFIG_HOME.
	globalDir := filepath.Join(tmpDir, "bharatcode")
	require.NoError(t, os.MkdirAll(globalDir, 0o755))
	globalFile := filepath.Join(globalDir, "config.json")
	require.NoError(t, os.WriteFile(globalFile, []byte(`{"ledger": {"currency": "USD"}}`), 0o600))

	// Write a sibling profile file (same dir as config.json).
	profileFile := filepath.Join(globalDir, "ollama.json")
	require.NoError(t, os.WriteFile(profileFile, []byte(`{"ledger": {"currency": "INR"}}`), 0o600))

	// LoadFromWithProfile resolves the profile by name relative to GlobalPath().
	cfg, err := LoadFromWithProfile(ctx, globalFile, "", "ollama")
	require.NoError(t, err)
	require.Equal(t, "INR", cfg.Ledger.Currency)
}

func TestLoadFromWithProfileEmptyNameSkipsOverlay(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	globalFile := filepath.Join(tmpDir, "config.json")
	require.NoError(t, os.WriteFile(globalFile, []byte(`{"ledger": {"currency": "USD"}}`), 0o600))

	// Empty profile name must reproduce LoadFrom exactly.
	cfg, err := LoadFromWithProfile(ctx, globalFile, "", "")
	require.NoError(t, err)
	require.Equal(t, "USD", cfg.Ledger.Currency)
}

func TestLoadFromWithProfileMissingFileErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	globalDir := filepath.Join(tmpDir, "bharatcode")
	require.NoError(t, os.MkdirAll(globalDir, 0o755))
	globalFile := filepath.Join(globalDir, "config.json")
	require.NoError(t, os.WriteFile(globalFile, []byte(`{}`), 0o600))

	// Profile "noexist" has no sibling file; expect an error.
	_, err := LoadFromWithProfile(ctx, globalFile, "", "noexist")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading profile config file")
}

func TestProfilePathDerivesFromGlobalDir(t *testing.T) {
	if os.Getenv("APPDATA") == "" {
		t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
		require.Equal(t, "/custom/xdg/bharatcode/work.json", ProfilePath("work"))
	}
}
