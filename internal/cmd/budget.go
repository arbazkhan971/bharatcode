package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newBudgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Configure spend limits",
	}
	cmd.AddCommand(newBudgetSetCmd())
	return cmd
}

func newBudgetSetCmd() *cobra.Command {
	var month float64
	cmd := &cobra.Command{
		Use:     "set",
		Short:   "Set budget caps",
		Example: "  bharatcode budget set --month 500",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if month < 0 {
				return fmt.Errorf("--month must be >= 0")
			}
			opts := getRootOptions(cmd)
			cfg, path, err := loadConfig(cmd.Context(), opts)
			if err != nil {
				return err
			}
			cfg.Ledger.MaxInrPerMonth = month
			if err := saveConfigPath(cmd.Context(), path, cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Monthly budget set to ₹%s\n", formatRupees(month))
			return nil
		},
	}
	cmd.Flags().Float64Var(&month, "month", 0, "monthly budget in INR")
	return cmd
}
