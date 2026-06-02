package cmd

import (
	"fmt"

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

			opts := getRootOptions(cmd)
			cfg, path, err := loadConfig(cmd.Context(), opts)
			if err != nil {
				return err
			}

			switch action {
			case "status":
				printTelemetryStatus(cmd, cfg.Options.Telemetry)
				return nil
			case "on", "off":
				enabled := action == "on"
				cfg.Options.Telemetry = enabled
				if err := saveConfigPath(cmd.Context(), path, cfg); err != nil {
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

// printTelemetryStatus writes the current preference and the no-data
// disclaimer to stdout in a stable, scriptable format.
func printTelemetryStatus(cmd *cobra.Command, enabled bool) {
	state := "off"
	if enabled {
		state = "on"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Telemetry: %s\n%s\n", state, telemetryDisclaimer)
}
