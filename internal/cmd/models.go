package cmd

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/spf13/cobra"
)

// selectModel presents the configured model IDs and returns the one the
// user chose. It is a package variable so tests can stub the interactive
// selection without driving a real terminal. The default implementation
// prints a numbered menu and reads a choice from stdin.
var selectModel = func(ids []string) (string, error) {
	return promptModelChoice(defaultPickIn, defaultPickOut, ids)
}

// defaultPickIn and defaultPickOut are the I/O streams the default
// selectModel implementation uses. The picker wires them to the command's
// stdin/stdout at call time so output is captured by Cobra's writers.
var (
	defaultPickIn  io.Reader = nil
	defaultPickOut io.Writer = nil
)

func newModelsCmd() *cobra.Command {
	var (
		pick     bool
		modelArg string
	)
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List configured models",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			cfg, path, err := loadConfig(cmd.Context(), opts)
			if err != nil {
				return err
			}

			// Non-interactive selection: --model wins over --pick so a
			// scripted invocation never blocks on the selector.
			if modelArg != "" {
				return applyDefaultModel(cmd, path, cfg, modelArg)
			}
			if pick {
				ids := sortedModelIDs(cfg)
				if len(ids) == 0 {
					return fmt.Errorf("no models configured")
				}
				defaultPickIn = cmd.InOrStdin()
				defaultPickOut = cmd.OutOrStdout()
				chosen, err := selectModel(ids)
				if err != nil {
					return fmt.Errorf("selecting model: %w", err)
				}
				return applyDefaultModel(cmd, path, cfg, chosen)
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
				// Show the effective context window: a compat override takes
				// precedence over the catalog value, mirroring the registry's
				// resolution, so the listing reflects what the agent will
				// actually enforce rather than the raw config field.
				contextWindow := m.ContextWindow
				if m.Compat != nil && m.Compat.ContextWindow != nil && *m.Compat.ContextWindow > 0 {
					contextWindow = *m.Compat.ContextWindow
				}
				models = append(models, struct {
					provider string
					id       string
					context  int
					input    float64
					output   float64
				}{m.Provider, m.ID, contextWindow, m.InputPricePerMTokUSD, m.OutputPricePerMTokUSD})
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
	cmd.Flags().BoolVar(&pick, "pick", false, "interactively choose the default model")
	cmd.Flags().StringVar(&modelArg, "model", "", "set the default model by ID (non-interactive)")
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
			return applyDefaultModel(cmd, path, cfg, args[0])
		},
	}
}

// sortedModelIDs returns the configured model IDs in stable alphabetical
// order so the picker menu is deterministic.
func sortedModelIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

// applyDefaultModel validates that modelID names a configured model, sets
// it as the default by writing it onto the "coder" agent (the convention
// the agent loop resolves as the default), persists the config, and reports
// the change. It is shared by "models switch", "models --model", and the
// interactive "models --pick" path so all three behave identically.
func applyDefaultModel(cmd *cobra.Command, path string, cfg *config.Config, modelID string) error {
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
}

// promptModelChoice renders a numbered menu of model IDs to out and reads a
// 1-based selection from in. It is the default, TTY-free selector body; the
// injectable selectModel var lets tests bypass it entirely.
func promptModelChoice(in io.Reader, out io.Writer, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", fmt.Errorf("no models to choose from")
	}
	for i, id := range ids {
		_, _ = fmt.Fprintf(out, "%d) %s\n", i+1, id)
	}
	_, _ = fmt.Fprint(out, "Select model: ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading selection: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("no selection made")
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(ids) {
		return "", fmt.Errorf("invalid selection %q", line)
	}
	return ids[n-1], nil
}
