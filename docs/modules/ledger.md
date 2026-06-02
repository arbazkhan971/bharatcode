# Ledger

**Path:** `internal/ledger/`
**Status:** Completed

## Purpose

The `ledger` module is **BharatCode's signature feature**: an INR-aware cost ledger that records every LLM API call, totals it across session/day/month/all windows, and enforces user-configured budget caps before the agent makes a request that would breach them. Every entry stores provider, model, input tokens, output tokens, USD cost, and INR cost (USD multiplied by `cfg.UsdInrRate`, default 83.50). Every `Record` also publishes a refreshed `Summary` on a typed pubsub topic so the TUI footer (`session 4b2a · in 12,453 · out 3,201 · cost $0.018 · ₹1.51 · budget ₹500/mo (0.3% used)`) can re-render without polling. Budget enforcement is centralized here so a "would this request exceed my session/day/month cap?" check is a single function call, returning one of three verdicts: allow, require confirmation, or deny. Costs are recomputed from token counts and config prices on every record — the ledger never trusts a provider or agent to report `cost_usd` directly, because providers lie and BharatCode's whole credibility on this rests on the numbers being right.

This module exists because users paying out of pocket in rupees are the audience BharatCode is built for, and the #1 user complaint about leading terminal coding agents in 2026 is unpredictable spend. The ledger is the cure.

## Public interface

```go
// Entry is one recorded LLM API call.
type Entry struct {
    ID           string    `json:"id"`
    SessionID    string    `json:"session_id"`
    Provider     string    `json:"provider"`   // e.g. "anthropic", "deepseek", "moonshot".
    Model        string    `json:"model"`      // Provider-scoped model ID.
    InputTokens  int       `json:"input_tokens"`
    OutputTokens int       `json:"output_tokens"`
    CostUSD      float64   `json:"cost_usd"`
    CostINR      float64   `json:"cost_inr"`
    At           time.Time `json:"at"`
}

// Summary rolls Entry rows up over a Window.
type Summary struct {
    Window       Window  `json:"window"`
    SessionID    string  `json:"session_id,omitempty"` // Set when Window == WindowSession.
    CostUSD      float64 `json:"cost_usd"`
    CostINR      float64 `json:"cost_inr"`
    InputTokens  int     `json:"input_tokens"`
    OutputTokens int     `json:"output_tokens"`
    CallCount    int     `json:"call_count"`
}

// Window is a rollup window.
type Window string

const (
    WindowSession Window = "session"
    WindowDay     Window = "day"   // Today (local time).
    WindowMonth   Window = "month" // This calendar month (local time).
    WindowAll     Window = "all"
)

// VerdictKind is the outcome of a CheckBudget call.
type VerdictKind string

const (
    VerdictAllowProceed         VerdictKind = "allow"
    VerdictRequireConfirmation  VerdictKind = "confirm"
    VerdictDeny                 VerdictKind = "deny"
)

// BudgetVerdict carries the outcome of CheckBudget plus the inputs
// the TUI needs to render a meaningful message to the user.
type BudgetVerdict struct {
    Kind       VerdictKind `json:"kind"`
    Reason     string      `json:"reason"`     // Human-readable, INR-formatted.
    Window     Window      `json:"window"`     // Which window triggered the verdict.
    CapINR     float64     `json:"cap_inr"`    // The cap that applies to that window.
    CurrentINR float64     `json:"current_inr"`// Spend in that window so far.
    PlannedINR float64     `json:"planned_inr"`// The plannedCostINR passed in.
}

// Ledger is the public handle for cost tracking and budget enforcement.
type Ledger struct {
    // Unexported: *db.DB, *db.Queries, *config.LedgerConfig,
    // *pubsub.Topic[Summary], sync.Mutex for record-then-publish atomicity.
}

// New constructs a Ledger. bus may be nil, in which case Record
// persists but does not publish.
func New(database *db.DB, cfg *config.LedgerConfig, bus *pubsub.Topic[Summary]) *Ledger

// Record persists e and, on success, publishes a fresh
// WindowSession Summary for e.SessionID on the bus.
// CostUSD and CostINR on the incoming Entry are ignored;
// Record recomputes both from e.InputTokens, e.OutputTokens,
// and config-derived prices for (e.Provider, e.Model).
// Returns ErrUnknownModel if no pricing is configured for the model.
func (l *Ledger) Record(ctx context.Context, e Entry) error

// Summary returns a rollup for the given window. When window is
// WindowSession, sessionID must be non-empty. For WindowDay,
// WindowMonth, and WindowAll, sessionID is ignored (pass "").
func (l *Ledger) Summary(ctx context.Context, sessionID string, window Window) (Summary, error)

// CheckBudget answers: "if I add plannedCostINR rupees to the
// session/day/month totals, what should I do?". Returns:
//
//   - VerdictDeny if any cap is configured > 0 and current+planned
//     would exceed it. Reason names the binding window and amounts.
//   - VerdictRequireConfirmation if any cap is configured > 0 and
//     current+planned would cross the 80% threshold for the first
//     time (i.e. current < 80% but current+planned >= 80%).
//   - VerdictAllowProceed otherwise.
//
// When multiple windows would deny, session > day > month
// (most-specific wins). The strictest deny wins outright over any
// confirmation. plannedCostINR < 0 returns ErrInvalidArgument.
func (l *Ledger) CheckBudget(ctx context.Context, sessionID string, plannedCostINR float64) (BudgetVerdict, error)

// Sentinel errors.
var (
    ErrUnknownModel     = errors.New("no pricing configured for model")
    ErrInvalidArgument  = errors.New("invalid argument")
    ErrBudgetExceeded   = errors.New("budget exceeded")
)
```

The `config.LedgerConfig` shape this module reads (defined in `internal/config/`) is approximately:

```go
type LedgerConfig struct {
    UsdInrRate         float64                  // Default 83.50.
    MaxINRPerSession   float64                  // 0 disables.
    MaxINRPerDay       float64                  // 0 disables.
    MaxINRPerMonth     float64                  // 0 disables.
    Models             map[string]ModelPricing  // Keyed by "provider/model".
}

type ModelPricing struct {
    InputPricePerMTokUSD  float64
    OutputPricePerMTokUSD float64
}
```

The `pubsub.Topic[Summary]` contract is the same as elsewhere: `Publish(Summary)` is non-blocking; `Subscribe() <-chan Summary` is the consumer's path. Ledger only calls `Publish`.

## Dependencies

- `internal/util` — id generation, time helpers (day/month bucket boundaries in local time).
- `internal/db` — `*db.DB` handle and `*db.Queries` sqlc bindings for the `ledger_entries` table.
- `internal/config` — `*config.LedgerConfig` for prices, INR rate, and budget caps.
- `internal/pubsub` — `*pubsub.Topic[Summary]` for publishing summary updates.

Per `docs/architecture.md`, ledger is a Layer 2 (core data) module, parallel-safe with `session`. It is consumed by `internal/llm` (every provider call ends in a `Record`), by `internal/agent` (which calls `CheckBudget` before dispatching), and by `internal/tui` (subscriber).

## Design notes

The INR-aware ledger, the 80% confirmation threshold, the per-window budget verdict, and the recompute-from-prices invariant are BharatCode design choices not present in any prior tool. Token-usage tracking — including fallback estimation when a provider returns zero counts — is intentionally out of scope here: BharatCode requires token counts on the provider response, and estimation is the `llm` module's concern, not this module's.

## Acceptance criteria

- `TestRecord_PersistsEntry` — Record then a raw `*sql.DB` query finds one row in `ledger_entries`.
- `TestRecord_RecomputesCostUSD` — pass `CostUSD = 999.0` on the incoming Entry with known token counts and known prices; persisted `CostUSD` is the recomputed value, not 999.
- `TestRecord_RecomputesCostINR_DefaultRate` — with `cfg.UsdInrRate == 83.50` and `CostUSD == 0.10`, persisted `CostINR == 8.35` exact to 0.01.
- `TestRecord_RecomputesCostINR_CustomRate` — with `cfg.UsdInrRate == 90.00`, the same call yields `CostINR == 9.00`.
- `TestRecord_UnknownModel_ReturnsErrUnknownModel` — model not present in `cfg.Models` yields `ErrUnknownModel`.
- `TestRecord_PublishesSummary` — subscriber on the topic receives a `Summary{Window: WindowSession, SessionID: <id>}` reflecting the new total.
- `TestRecord_NilBus_NoPanic` — same scenario with bus=nil; entry persists; no goroutine panics.
- `TestSummary_Session_AggregatesEntries` — two entries in one session sum to the right CostINR, CostUSD, InputTokens, OutputTokens, CallCount=2.
- `TestSummary_Day_ScopedToToday` — entry from yesterday is excluded from `WindowDay`.
- `TestSummary_Month_ScopedToCurrentMonth` — entry from last calendar month is excluded from `WindowMonth`.
- `TestSummary_All_IncludesEverything` — yesterday + last month + today all counted.
- `TestSummary_EmptyWindow_ReturnsZeroes` — Summary with no entries returns a zero-valued Summary (no error).
- `TestCheckBudget_AllowProceed_NoCapsConfigured` — all `MaxINR*` are 0; verdict is `VerdictAllowProceed`.
- `TestCheckBudget_AllowProceed_UnderAllThresholds` — caps set, current spend at 10% of session cap, planned brings it to 30%; verdict is `VerdictAllowProceed`.
- `TestCheckBudget_RequireConfirmation_CrossesEightyPercent` — current at 70% of session cap, planned brings it to 85%; verdict is `VerdictRequireConfirmation` with `Window == WindowSession`.
- `TestCheckBudget_Deny_SessionCapExceeded` — current + planned > `MaxINRPerSession`; verdict is `VerdictDeny`, reason contains the session cap and projected total in INR (`"session cap (₹500.00) exceeded — would be ₹512.30"`).
- `TestCheckBudget_Deny_DayCapExceeded` — same logic for day window.
- `TestCheckBudget_Deny_MonthCapExceeded` — same logic for month window.
- `TestCheckBudget_DenyWinsOverConfirmation` — current at 70% session (would confirm) but day cap would deny; verdict is `VerdictDeny`.
- `TestCheckBudget_NegativePlanned_ReturnsErrInvalidArgument` — `plannedCostINR == -1` returns `ErrInvalidArgument`.
- `TestRecord_ThenSummary_INRPrecision` — three entries with awkward USD costs (0.0007, 0.013, 0.00021); summed `CostINR` matches `round-to-0.01(sum * 83.50)`.
- `TestConcurrentRecord_NoDataRace` — 16 goroutines `Record`-ing different entries; `go test -race` passes; final Summary call-count is 16.

`go test -race ./internal/ledger/...` must pass.

## Notes for the implementer

- Pricing source of truth is `cfg.Models[provider+"/"+model]`. Look up by composite key. If absent, return `ErrUnknownModel` wrapped: `fmt.Errorf("pricing for %s/%s: %w", e.Provider, e.Model, ErrUnknownModel)`. The agent should never reach this branch in production — `internal/llm` is responsible for refusing to dispatch to a model the config does not price.
- Cost formula:
  - `usd = (inputTokens * inputPricePerMTokUSD + outputTokens * outputPricePerMTokUSD) / 1_000_000`
  - `inr = round_to_cents(usd * cfg.UsdInrRate)` where `round_to_cents(x) = math.Round(x*100) / 100`.
  - The "0.01 INR accuracy" acceptance criterion is satisfied by `math.Round` at cents; do not use string formatting for arithmetic.
- The ledger **never** trusts `e.CostUSD` / `e.CostINR` on input. Overwrite both before insert. This is the load-bearing invariant of the module — the recompute test is non-negotiable.
- Day and month windows are evaluated in **local time**: `time.Now().Local()` for "today" and "this month". The TUI footer renders in local time; storing UTC and converting at read time is fine, but the bucket boundaries used by `Summary(window=Day|Month)` must be local. Document this in code comments.
- The 80% confirmation threshold is one-shot per window: once `current >= 80% * cap`, the verdict downgrades to `VerdictAllowProceed` if `current+planned < cap` (we already warned; no point warning again), or `VerdictDeny` if `current+planned >= cap`. The cross-threshold test verifies the first crossing fires confirmation; a follow-up test (`TestCheckBudget_AlreadyOverEighty_DoesNotReconfirm`) should verify subsequent calls in the same window stay `VerdictAllowProceed` until they hit the hard cap. **Add that test even though it is not in the list above.**
- The "session > day > month" precedence on deny means: if both session and month caps would be exceeded, the verdict's `Window` is `WindowSession`. This makes the reason string useful to the user (the cap they can most directly act on is the session cap).
- Reason strings are INR-formatted with two decimals and the `₹` rune: `fmt.Sprintf("session cap (₹%.2f) exceeded — would be ₹%.2f", cap, projected)`. Day: `"daily cap (₹%.2f) exceeded — would be ₹%.2f"`. Month: `"monthly cap (₹%.2f) exceeded — would be ₹%.2f"`. Confirmation: `"approaching %s cap (%.0f%% of ₹%.2f used after this call)"` with window name lowercased.
- SQLite schema row (defined in `internal/db/migrations/`) is approximately:

  ```sql
  CREATE TABLE ledger_entries (
      id            TEXT PRIMARY KEY,
      session_id    TEXT NOT NULL,
      provider      TEXT NOT NULL,
      model         TEXT NOT NULL,
      input_tokens  INTEGER NOT NULL,
      output_tokens INTEGER NOT NULL,
      cost_usd      REAL NOT NULL,
      cost_inr      REAL NOT NULL,
      at            INTEGER NOT NULL,  -- Unix seconds, UTC.
      FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
  );
  CREATE INDEX idx_ledger_entries_session ON ledger_entries(session_id, at);
  CREATE INDEX idx_ledger_entries_at      ON ledger_entries(at);
  ```

  Coordinate with the `db` module owner. Costs are stored as SQLite `REAL`; for Phase 1 the float64 precision is acceptable — we are not a bank.

- Use sqlc-generated queries via `*db.Queries`. Add `internal/db/queries/ledger.sql` for sum-aggregates by window. Do not embed raw SQL in this package.
- `Record` -> `Publish` is internally synchronized with `sync.Mutex` so a concurrent reader using `Subscribe()` plus `Summary()` cannot observe an inconsistent (newer row inserted, older summary published) state. The test `TestRecord_PublishesSummary` is the canary for this.
- Errors are wrapped: `fmt.Errorf("recording entry %s: %w", e.ID, err)`. Sentinels (`ErrUnknownModel`, `ErrInvalidArgument`, `ErrBudgetExceeded`) are returned via `errors.Is`-friendly wrapping.
- `context.Context` is the first parameter on every method.
- Use `log/slog`. Capitalized messages, no trailing period: `slog.Info("Recorded LLM call", "provider", e.Provider, "model", e.Model, "cost_inr", e.CostINR)`.
- Tests: `testify/require`, `t.TempDir()` for the SQLite handle, table-driven for the `CheckBudget` verdict matrix. Use `t.Setenv()` if you need to fake locale; otherwise tests run in whatever local timezone CI sets — be careful in the day/month tests to construct entries with `at` values relative to `time.Now().Local()` rather than literal timestamps so the suite is timezone-stable.
- Run `gofumpt -w .` and `golangci-lint run` before declaring the module done. Append an `## Implementation status` section to this file listing what was built and any deliberate deviations.

## Implementation status

Implemented in `internal/ledger/`:

- `Ledger`, `Entry`, `Summary`, and budget verdict types with sentinel errors
  for unknown models, invalid arguments, and exceeded budgets.
- Cost recording through sqlc queries, with USD and INR costs recomputed from
  configured model prices and the configured USD/INR rate before persistence.
- Session, day, month, and all-time summaries, with day and month windows
  evaluated in local time.
- Budget checks for session, day, and month caps, including the one-shot 80%
  confirmation threshold and hard-deny precedence from session to day to
  month.
- Optional publication of per-session summaries after successful records.
- Unit tests covering persistence, recomputation, unknown models, summary
  windows, publication, budget verdicts, cap precedence, and concurrent
  recording.

Deliberate deviations and notes:

- The implementation builds its pricing map from `config.Model` values passed
  to `New` rather than from `LedgerConfig.Models`. This matches the current
  `config` module shape, where provider/model catalog entries live in the root
  `Models` list and `LedgerConfig.Models` is optional override metadata.
- The environment currently does not have `gofumpt` or `golangci-lint`
  installed. The code has been formatted with `gofmt`, and `go test ./...`
  passes in the current workspace.
