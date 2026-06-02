package cmd

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// readTelemetryPref reads the raw config file and reports whether the
// persisted telemetry preference is true. A missing key means the
// preference is off, matching the default.
func readTelemetryPref(t *testing.T, configPath string) (present bool, value bool) {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	options, ok := cfg["options"].(map[string]any)
	if !ok {
		return false, false
	}
	v, ok := options["telemetry"]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	require.True(t, ok, "telemetry must be a JSON boolean, got %T", v)
	return true, b
}

func TestTelemetryDefaultStatusIsOff(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "telemetry", "status")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: off")

	// status must not write anything: the config must remain free of a
	// telemetry preference until the user explicitly opts in.
	present, _ := readTelemetryPref(t, configPath)
	require.False(t, present, "status must not persist a telemetry preference")
}

func TestTelemetryNoArgDefaultsToStatus(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "telemetry")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: off")
}

func TestTelemetryOnPersistsTrue(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "telemetry", "on")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: on")

	present, value := readTelemetryPref(t, configPath)
	require.True(t, present, "telemetry on must persist the preference")
	require.True(t, value, "telemetry on must persist true")

	// status reads back the persisted preference as on.
	stdout, stderr, err = executeRoot(t, "--config", configPath, "telemetry", "status")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: on")
}

func TestTelemetryOffPersistsFalse(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())

	// Turn it on first so off has something to flip back.
	_, _, err := executeRoot(t, "--config", configPath, "telemetry", "on")
	require.NoError(t, err)
	_, value := readTelemetryPref(t, configPath)
	require.True(t, value)

	stdout, stderr, err := executeRoot(t, "--config", configPath, "telemetry", "off")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: off")

	// After off, the persisted preference must read back as off (whether
	// stored explicitly as false or omitted entirely).
	present, value := readTelemetryPref(t, configPath)
	if present {
		require.False(t, value, "telemetry off must not leave a true preference")
	}

	stdout, _, err = executeRoot(t, "--config", configPath, "telemetry", "status")
	require.NoError(t, err)
	require.Contains(t, stdout, "Telemetry: off")
}

func TestTelemetryStatusMissingFileIsOff(t *testing.T) {
	// A fresh machine has no config file yet; status must report off
	// without creating one, since the preference defaults to opt-out.
	missing := filepath.Join(t.TempDir(), "nope", "config.json")
	stdout, stderr, err := executeRoot(t, "--config", missing, "telemetry", "status")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: off")

	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "status must not create the config file")
}

func TestTelemetryOnCreatesMissingFile(t *testing.T) {
	// "on" must work even when no config file exists yet, creating a
	// minimal overlay that records consent; defaults merge at load.
	missing := filepath.Join(t.TempDir(), "nope", "config.json")
	stdout, stderr, err := executeRoot(t, "--config", missing, "telemetry", "on")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Telemetry: on")

	present, value := readTelemetryPref(t, missing)
	require.True(t, present, "on must persist the preference into a new file")
	require.True(t, value)

	stdout, _, err = executeRoot(t, "--config", missing, "telemetry", "status")
	require.NoError(t, err)
	require.Contains(t, stdout, "Telemetry: on")
}

func TestTelemetryPreservesOtherConfigKeys(t *testing.T) {
	// Persisting the preference must not disturb any other config key,
	// since storage is a surgical edit of options.telemetry only.
	configPath := writeConfig(t, defaultTestConfig())
	_, _, err := executeRoot(t, "--config", configPath, "telemetry", "on")
	require.NoError(t, err)

	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))

	providers, ok := cfg["providers"].([]any)
	require.True(t, ok, "providers must survive the telemetry edit")
	require.Len(t, providers, 1)
	ledger, ok := cfg["ledger"].(map[string]any)
	require.True(t, ok, "ledger must survive the telemetry edit")
	require.Equal(t, "INR", ledger["currency"])
}

func TestTelemetryStatusReportsDisclaimer(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, _, err := executeRoot(t, "--config", configPath, "telemetry", "status")
	require.NoError(t, err)
	require.Contains(t, stdout, "sends no data")
}

func TestTelemetryRejectsUnknownAction(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	stdout, stderr, err := executeRoot(t, "--config", configPath, "telemetry", "bogus")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "unknown telemetry action")
}

// TestTelemetryHasNoSender is a static guard: the telemetry command must
// not import any network capability. Because there is no sender, no data
// can leave the machine regardless of the stored preference. This proves
// the consent-only design at the source level, deterministically and
// offline.
func TestTelemetryHasNoSender(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "telemetry.go", nil, parser.ImportsOnly)
	require.NoError(t, err)

	banned := []string{
		"net/http",
		"net/url",
		"net",
		"crypto/tls",
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		for _, b := range banned {
			require.NotEqual(t, b, path, "telemetry command must not import network package %q", b)
		}
	}

	// Belt-and-suspenders: no network-dialing identifiers appear in the
	// source either, so a future edit that adds a sender trips this test.
	src, err := os.ReadFile("telemetry.go")
	require.NoError(t, err)
	for _, needle := range []string{"http.", "net.Dial", "url.Parse", ".Post(", ".Get("} {
		require.NotContains(t, string(src), needle,
			"telemetry command must not contain network call %q", needle)
	}
}
