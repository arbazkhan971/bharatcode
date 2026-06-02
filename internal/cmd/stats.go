package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:     "stats",
		Short:   "Print usage statistics",
		Example: "  bharatcode stats --since month",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := parseSince(since); err != nil {
				return err
			}
			application, err := buildApp(cmd.Context(), getRootOptions(cmd))
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)
			window := ledger.WindowAll
			if since == "" || since == "30d" || since == "month" {
				window = ledger.WindowMonth
			}
			sum, err := application.Ledger.Summary(cmd.Context(), "", window)
			if err != nil {
				return fmt.Errorf("loading usage: %w", err)
			}
			if sum.CallCount == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no usage recorded")
				return nil
			}
			rows := [][]string{
				{"WINDOW", "CALLS", "INPUT", "OUTPUT", "COST(₹)"},
				{string(window), fmt.Sprintf("%d", sum.CallCount), fmt.Sprintf("%d", sum.InputTokens), fmt.Sprintf("%d", sum.OutputTokens), formatRupees(sum.CostINR)},
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), renderTable(rows))
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "30d", "period: 7d, 30d, month, all")
	return cmd
}
