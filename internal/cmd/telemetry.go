package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
	"github.com/spf13/cobra"
)

// telemetryDisclaimer explains, in one line, what enabling telemetry
// does and does not do. BharatCode ships no telemetry sender, so the
// preference only records consent locally; nothing is transmitted.
const telemetryDisclaimer = "Telemetry is opt-in and off by default. " +
	"BharatCode sends no data; this preference only records your consent locally."

func newTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry [on|off|status]",
		Short: "View or set the local telemetry consent preference",
		Long: "Manage the explicit, opt-in telemetry preference.\n\n" +
			telemetryDisclaimer + "\n\n" +
			"With no argument, prints the current status. \"on\" and \"off\" set\n" +
			"the preference and persist it to the user config.",
		Example: "  bharatcode telemetry status\n" +
			"  bharatcode telemetry on\n" +
			"  bharatcode telemetry off",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off", "status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			action := "status"
			if len(args) == 1 {
				action = args[0]
			}

			path := telemetryConfigPath(getRootOptions(cmd))

			switch action {
			case "status":
				enabled, err := readTelemetryPreference(path)
				if err != nil {
					return err
				}
				printTelemetryStatus(cmd, enabled)
				return nil
			case "on", "off":
				enabled := action == "on"
				if err := writeTelemetryPreference(path, enabled); err != nil {
					return err
				}
				printTelemetryStatus(cmd, enabled)
				return nil
			default:
				return fmt.Errorf("unknown telemetry action %q: expected on, off, or status", action)
			}
		},
	}
	return cmd
}

// telemetryConfigPath resolves the user config file the preference is
// stored in, mirroring the resolution used elsewhere in the command
// tree: the explicit --config path if set, otherwise the global path.
func telemetryConfigPath(opts *rootOptions) string {
	path := opts.configPath
	if path == "" {
		path = config.GlobalPath()
	}
	return util.ExpandPath(path)
}

// readTelemetryPreference reports whether telemetry is enabled in the
// config file at path. A missing file or a missing key means the
// preference is off, matching the opt-in default. It reads only the
// boolean and never contacts the network.
func readTelemetryPreference(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading config file: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, fmt.Errorf("parsing config file: %w", err)
	}
	optsRaw, ok := doc["options"]
	if !ok {
		return false, nil
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(optsRaw, &options); err != nil {
		return false, fmt.Errorf("parsing config options: %w", err)
	}
	telRaw, ok := options["telemetry"]
	if !ok {
		return false, nil
	}
	var enabled bool
	if err := json.Unmarshal(telRaw, &enabled); err != nil {
		return false, fmt.Errorf("parsing telemetry preference: %w", err)
	}
	return enabled, nil
}

// writeTelemetryPreference persists the telemetry preference into the
// config file at path, preserving every other key untouched. Enabling
// sets options.telemetry to true; disabling deletes the key so the file
// returns to its default (off) shape rather than recording a redundant
// false. When the file does not yet exist a minimal overlay is created;
// the embedded defaults still merge in at application load. No data
// leaves the machine: this only edits a local boolean.
func writeTelemetryPreference(path string, enabled bool) error {
	doc := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parsing config file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	options := map[string]json.RawMessage{}
	if optsRaw, ok := doc["options"]; ok {
		if err := json.Unmarshal(optsRaw, &options); err != nil {
			return fmt.Errorf("parsing config options: %w", err)
		}
	}

	if enabled {
		options["telemetry"] = json.RawMessage("true")
	} else {
		delete(options, "telemetry")
	}

	optsRaw, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("marshaling config options: %w", err)
	}
	doc["options"] = optsRaw

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config file: %w", err)
	}
	out = append(out, '\n')

	if err := fsext.EnsureDir(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensuring config directory: %w", err)
	}
	if err := fsext.AtomicWrite(path, out, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	return nil
}

// printTelemetryStatus writes the current preference and the no-data
// disclaimer to stdout in a stable, scriptable format.
func printTelemetryStatus(cmd *cobra.Command, enabled bool) {
	state := "off"
	if enabled {
		state = "on"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Telemetry: %s\n%s\n", state, telemetryDisclaimer)
}
