package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/spf13/cobra"
)

// providerUpdateSummary reports how many providers and models were
// newly added to the local catalog by an update-providers run.
type providerUpdateSummary struct {
	Providers int
	Models    int
}

// modelsDevProvider is one provider entry in the Models.dev registry
// (https://models.dev/api.json). The top-level document is a map of
// provider id to this structure. Only the fields BharatCode consumes
// are decoded; unknown fields (name, npm, doc, ...) are ignored.
type modelsDevProvider struct {
	ID     string                    `json:"id"`
	API    string                    `json:"api"`
	Env    []string                  `json:"env"`
	Models map[string]modelsDevModel `json:"models"`
}

// modelsDevModel is one model entry under a Models.dev provider.
type modelsDevModel struct {
	ID         string `json:"id"`
	Attachment bool   `json:"attachment"`
	ToolCall   bool   `json:"tool_call"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
}

// fetchProviderRegistry retrieves the raw Models.dev registry bytes
// over HTTP. It is split out from the parse/merge logic so the latter
// can be unit-tested with fixture bytes and no network.
func fetchProviderRegistry(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating provider update request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching provider registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("provider registry returned %s", resp.Status)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("provider registry request failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading provider registry body: %w", err)
	}
	return body, nil
}

// updateProviders fetches the registry, then parses and merges it into
// the config at path. It is a package var so tests may stub the whole
// flow; production callers exercise the real network path. A network
// or parse failure returns an error and leaves the on-disk config
// untouched (mergeProviderRegistry never runs on a failed fetch, and
// the caller only persists on success).
var updateProviders = func(ctx context.Context, url string, cfg *config.Config) (providerUpdateSummary, error) {
	body, err := fetchProviderRegistry(ctx, url)
	if err != nil {
		return providerUpdateSummary{}, err
	}
	return mergeProviderRegistry(cfg, body)
}

// mergeProviderRegistry parses the Models.dev registry bytes and merges
// the providers and models they describe into cfg in place. Existing
// user-defined providers (matched by name) and models (matched by
// provider+id) are preserved verbatim and never overwritten; only
// previously unknown providers and models are appended. The returned
// summary counts the newly added providers and models.
//
// A malformed or empty payload returns an error and leaves cfg
// unchanged, so a bad fetch can never clobber existing packs.
func mergeProviderRegistry(cfg *config.Config, data []byte) (providerUpdateSummary, error) {
	if cfg == nil {
		return providerUpdateSummary{}, fmt.Errorf("merging provider registry: nil config")
	}
	if len(data) == 0 {
		return providerUpdateSummary{}, fmt.Errorf("merging provider registry: empty payload")
	}

	var registry map[string]modelsDevProvider
	if err := json.Unmarshal(data, &registry); err != nil {
		return providerUpdateSummary{}, fmt.Errorf("parsing provider registry: %w", err)
	}
	if len(registry) == 0 {
		return providerUpdateSummary{}, fmt.Errorf("parsing provider registry: no providers found")
	}

	// Index existing config so we never overwrite user-defined entries.
	existingProviders := make(map[string]int, len(cfg.Providers))
	for i, p := range cfg.Providers {
		existingProviders[p.Name] = i
	}
	existingModels := make(map[string]struct{}, len(cfg.Models))
	for _, m := range cfg.Models {
		existingModels[m.Provider+"/"+m.ID] = struct{}{}
	}

	// Iterate providers in a stable order so appended entries and the
	// summary counts are deterministic across runs.
	providerIDs := make([]string, 0, len(registry))
	for id := range registry {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)

	var summary providerUpdateSummary
	for _, id := range providerIDs {
		src := registry[id]
		providerName := src.ID
		if providerName == "" {
			providerName = id
		}

		if _, ok := existingProviders[providerName]; !ok {
			cfg.Providers = append(cfg.Providers, config.Provider{
				Name:      providerName,
				Type:      providerTypeFor(src),
				BaseURL:   src.API,
				APIKeyEnv: firstEnv(src.Env),
				Models:    modelIDsFor(src),
			})
			existingProviders[providerName] = len(cfg.Providers) - 1
			summary.Providers++
		}

		// Merge models in a stable order regardless of map iteration.
		modelIDs := make([]string, 0, len(src.Models))
		for mid := range src.Models {
			modelIDs = append(modelIDs, mid)
		}
		sort.Strings(modelIDs)
		for _, mid := range modelIDs {
			sm := src.Models[mid]
			modelID := sm.ID
			if modelID == "" {
				modelID = mid
			}
			key := providerName + "/" + modelID
			if _, ok := existingModels[key]; ok {
				continue
			}
			cfg.Models = append(cfg.Models, config.Model{
				ID:                    modelID,
				Provider:              providerName,
				ContextWindow:         sm.Limit.Context,
				InputPricePerMTokUSD:  sm.Cost.Input,
				OutputPricePerMTokUSD: sm.Cost.Output,
				SupportsImages:        supportsImages(sm),
				SupportsTools:         sm.ToolCall,
			})
			existingModels[key] = struct{}{}
			summary.Models++
		}
	}

	return summary, nil
}

// providerTypeFor maps a Models.dev provider to a BharatCode
// ProviderType. The mapping keys off the provider id alone; anything
// that is not a recognised first-party dialect falls back to
// openai_compatible, which is what the bulk of Models.dev providers
// (the "@ai-sdk/openai-compatible" adapter) use and the safe default
// for unknown OpenAI-style APIs.
func providerTypeFor(p modelsDevProvider) config.ProviderType {
	switch p.ID {
	case "anthropic":
		return config.ProviderAnthropic
	case "openai":
		return config.ProviderOpenAI
	case "ollama":
		return config.ProviderOllama
	case "lmstudio", "lm-studio":
		return config.ProviderLMStudio
	}
	return config.ProviderOpenAICompatible
}

// supportsImages reports whether a model accepts image input. Models.dev
// signals this via the attachment flag and/or an "image" input modality.
func supportsImages(m modelsDevModel) bool {
	if m.Attachment {
		return true
	}
	for _, in := range m.Modalities.Input {
		if in == "image" {
			return true
		}
	}
	return false
}

// firstEnv returns the first API-key env var name a provider declares,
// or the empty string when none are listed.
func firstEnv(env []string) string {
	if len(env) == 0 {
		return ""
	}
	return env[0]
}

// modelIDsFor returns the model ids a provider exposes, in stable order.
func modelIDsFor(p modelsDevProvider) []string {
	ids := make([]string, 0, len(p.Models))
	for mid, m := range p.Models {
		id := m.ID
		if id == "" {
			id = mid
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func newUpdateProvidersCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "update-providers",
		Short: "Refresh provider model metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			opts := getRootOptions(cmd)
			cfg, path, err := loadConfig(cmd.Context(), opts)
			if err != nil {
				return err
			}
			summary, err := updateProviders(cmd.Context(), url, cfg)
			if err != nil {
				// A failed fetch or parse must not touch the config on
				// disk; return before persisting so existing packs survive.
				return err
			}
			if err := saveConfigPath(cmd.Context(), path, cfg); err != nil {
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
