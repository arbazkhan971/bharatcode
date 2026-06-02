# ADR: Custom Merge Strategy vs. Viper for Configuration Management

* **Date:** 2026-05-26
* **Status:** Accepted
* **Context:** Config module merge strategy

## Context and Problem Statement

The locked technology stack for BharatCode includes `github.com/spf13/viper` for configuration. However, when implementing multi-layered configuration files (embedded defaults, global config at `~/.config/bharatcode/config.json`, and project-local config at `.bharatcode.json`), we encountered issues with viper's standard behaviors:

1. **Case-Insensitivity & Snake Case:** Viper is case-insensitive and normalizes keys. This makes it difficult to maintain strict snake_case JSON outputs when saving or reading.
2. **Slice Merging:** Viper's default behavior is to either fully override slices, or append them in unexpected ways depending on the type of key binding. For our LLM provider configurations, a clean "project replaces global, global replaces defaults" array-override behavior is required.
3. **Map and Key Binding Surprises:** Viper's auto-binding of environment variables and nested map structure resolution often produces unexpected side effects for complex arrays of structs (e.g., `[]Provider`, `[]Model`, `[]Agent`, `[]Hook`, `[]MCPServer`, `[]LSPServer`).

Therefore, we need a custom merge strategy that is predictable, type-safe, simple, and avoids third-party dependencies inside `internal/config/`.

## Decision

We will implement a custom Go struct-walk merge strategy inside `internal/config/load.go` and avoid importing or using Viper in `internal/config/`. 

The custom merge logic:
- **Scalars:** Non-zero values in the overriding config replace the base value.
- **Slices:** If a slice field in the overriding config is non-nil (even if empty), it replaces the base slice entirely. Slices do not append.
- **Nested structs:** Merged field-by-field recursively.

## Consequences

* **Pros:**
  * 100% predictable behavior with clean-cut JSON schemas.
  * No external dependencies required for `internal/config/` (pure Go stdlib + `internal/util`), keeping the core configuration library thin and modular.
  * Simple unit testing with table-driven tests.
  * Preserves comments and precise structures during serialisation/round-trips.

* **Cons:**
  * Requires manually walking the `Config` struct fields to implement `merge()`, but the struct has a stable, locked schema.
