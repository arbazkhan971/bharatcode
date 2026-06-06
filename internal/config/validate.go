package config

import (
	"fmt"
	"regexp"
)

var mcpServerNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Validate reports the first validation error found in cfg, or nil
// if cfg is internally consistent. Validate is called by Load and
// exposed for tests and the `bharatcode config validate` command.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	// 4. Provider names are unique within Providers.
	// 9. ProviderType is one of the five constants.
	provNames := make(map[string]bool)
	for i, prov := range cfg.Providers {
		if prov.Name == "" {
			return fmt.Errorf("provider name cannot be empty at /providers/%d/name", i)
		}
		if provNames[prov.Name] {
			return fmt.Errorf("duplicate provider name %q at /providers/%d/name", prov.Name, i)
		}
		provNames[prov.Name] = true

		switch prov.Type {
		case ProviderAnthropic, ProviderOpenAI, ProviderOpenAICompatible, ProviderOllama, ProviderLMStudio,
			ProviderOpenAIResponses, ProviderCodexOAuth, ProviderGemini:
			// Valid
		default:
			return fmt.Errorf("invalid provider type %q at /providers/%d/type", prov.Type, i)
		}
	}

	// 5. Model IDs are unique within Models.
	// 1. Every Model.Provider matches a Provider.Name.
	modelIDs := make(map[string]bool)
	for i, model := range cfg.Models {
		if model.ID == "" {
			return fmt.Errorf("model ID cannot be empty at /models/%d/id", i)
		}
		if modelIDs[model.ID] {
			return fmt.Errorf("duplicate model ID %q at /models/%d/id", model.ID, i)
		}
		modelIDs[model.ID] = true

		if !provNames[model.Provider] {
			return fmt.Errorf("model %q references missing provider %q at /models/%d/provider", model.ID, model.Provider, i)
		}
	}

	// 2. Every Agent.Model matches a Model.ID.
	for i, agent := range cfg.Agents {
		if agent.Name == "" {
			return fmt.Errorf("agent name cannot be empty at /agents/%d/name", i)
		}
		if agent.Model != "" && !modelIDs[agent.Model] {
			return fmt.Errorf("agent %q references missing model %q at /agents/%d/model", agent.Name, agent.Model, i)
		}
	}

	// 3. Every Hook.Event is a known HookEvent constant.
	for i, hook := range cfg.Hooks {
		switch hook.Event {
		case HookPreToolUse, HookPostToolUse, HookUserPromptSubmit, HookOnError, HookOnSession,
			HookSessionStart, HookSessionEnd, HookFileEdit:
			// Valid
		default:
			return fmt.Errorf("invalid hook event %q at /hooks/%d/event", hook.Event, i)
		}
	}

	// 10. MCPServer.Transport is one of "stdio", "http", "sse".
	for i, mcp := range cfg.MCP {
		if mcp.Name == "" {
			return fmt.Errorf("mcp server name cannot be empty at /mcp/%d/name", i)
		}
		if !mcpServerNameRE.MatchString(mcp.Name) {
			return fmt.Errorf("mcp server name %q must match [a-z][a-z0-9_]{0,31} at /mcp/%d/name", mcp.Name, i)
		}
		switch mcp.Transport {
		case "stdio":
			if mcp.Command == "" {
				return fmt.Errorf("mcp server %q stdio transport requires command at /mcp/%d/command", mcp.Name, i)
			}
		case "http", "sse":
			if mcp.URL == "" {
				return fmt.Errorf("mcp server %q %s transport requires url at /mcp/%d/url", mcp.Name, mcp.Transport, i)
			}
		default:
			return fmt.Errorf("invalid mcp transport %q at /mcp/%d/transport", mcp.Transport, i)
		}
	}

	// 6. LedgerConfig.Currency is one of "INR", "USD".
	switch cfg.Ledger.Currency {
	case "INR", "USD":
		// Valid
	default:
		return fmt.Errorf("invalid currency %q at /ledger/currency", cfg.Ledger.Currency)
	}

	// 7. LedgerConfig.UsdInrRate > 0.
	if cfg.Ledger.UsdInrRate <= 0 {
		return fmt.Errorf("invalid usd_inr_rate %f at /ledger/usd_inr_rate", cfg.Ledger.UsdInrRate)
	}

	// 8. All MaxInr* fields are >= 0.
	if cfg.Ledger.MaxInrPerSession < 0 {
		return fmt.Errorf("invalid max_inr_per_session %f at /ledger/max_inr_per_session", cfg.Ledger.MaxInrPerSession)
	}
	if cfg.Ledger.MaxInrPerDay < 0 {
		return fmt.Errorf("invalid max_inr_per_day %f at /ledger/max_inr_per_day", cfg.Ledger.MaxInrPerDay)
	}
	if cfg.Ledger.MaxInrPerMonth < 0 {
		return fmt.Errorf("invalid max_inr_per_month %f at /ledger/max_inr_per_month", cfg.Ledger.MaxInrPerMonth)
	}

	return nil
}
