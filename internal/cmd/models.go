package cmd

import (
	"fmt"
	"sort"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List configured models",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			cfg, _, err := loadConfig(cmd.Context(), getRootOptions(cmd))
			if err != nil {
				return err
			}
			rows := [][]string{{"PROVIDER", "MODEL", "CONTEXT", "INPUT$/MTOK", "OUTPUT$/MTOK"}}
			models := append([]struct {
				provider string
				id       string
				context  int
				input    float64
				output   float64
			}{}, nil...)
			for _, m := range cfg.Models {
				models = append(models, struct {
					provider string
					id       string
					context  int
					input    float64
					output   float64
				}{m.Provider, m.ID, m.ContextWindow, m.InputPricePerMTokUSD, m.OutputPricePerMTokUSD})
			}
			sort.Slice(models, func(i, j int) bool {
				if models[i].provider == models[j].provider {
					return models[i].id < models[j].id
				}
				return models[i].provider < models[j].provider
			})
			for _, m := range models {
				rows = append(rows, []string{
					m.provider,
					m.id,
					fmt.Sprintf("%d", m.context),
					fmt.Sprintf("%.2f", m.input),
					fmt.Sprintf("%.2f", m.output),
				})
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), renderTable(rows))
			return nil
		},
	}
	cmd.AddCommand(newModelsSwitchCmd())
	return cmd
}

func newModelsSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "switch <model>",
		Short:   "Set the default model",
		Example: "  bharatcode models switch deepseek-chat",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := getRootOptions(cmd)
			cfg, path, err := loadConfig(cmd.Context(), opts)
			if err != nil {
				return err
			}
			modelID := args[0]
			found := false
			for _, model := range cfg.Models {
				if model.ID == modelID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("model %s not found", modelID)
			}
			updated := false
			for i := range cfg.Agents {
				if cfg.Agents[i].Name == "coder" {
					cfg.Agents[i].Model = modelID
					updated = true
					break
				}
			}
			if !updated {
				cfg.Agents = append(cfg.Agents, config.Agent{Name: "coder", Model: modelID})
			}
			if err := saveConfigPath(cmd.Context(), path, cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Default model set to %s\n", modelID)
			return nil
		},
	}
}
