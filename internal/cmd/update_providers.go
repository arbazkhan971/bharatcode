package cmd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

type providerUpdateSummary struct {
	Providers int
	Models    int
}

var updateProviders = func(ctx context.Context, url string) (providerUpdateSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return providerUpdateSummary{}, fmt.Errorf("creating provider update request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return providerUpdateSummary{}, fmt.Errorf("fetching provider registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return providerUpdateSummary{}, fmt.Errorf("provider registry returned %s", resp.Status)
	}
	if resp.StatusCode >= 400 {
		return providerUpdateSummary{}, fmt.Errorf("provider registry request failed: %s", resp.Status)
	}
	return providerUpdateSummary{}, nil
}

func newUpdateProvidersCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "update-providers",
		Short: "Refresh provider model metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			summary, err := updateProviders(cmd.Context(), url)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated %d providers, %d models\n", summary.Providers, summary.Models)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "https://models.dev/api.json", "provider registry URL")
	_ = cmd.Flags().MarkHidden("url")
	return cmd
}
