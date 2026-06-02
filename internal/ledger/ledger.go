// Package ledger tracks LLM API call costs in both USD and INR,
// enforces per-session/day/month budget caps, and publishes rolling
// summaries on a typed pubsub topic so the TUI footer can re-render
// without polling.
//
// Cost computation always uses config-derived prices and the configured
// USD→INR rate. Any CostUSD/CostINR fields on an incoming Entry are
// discarded and recomputed from token counts — this is the load-bearing
// invariant that makes the ledger trustworthy.
//
// Time windows (WindowDay, WindowMonth) are evaluated in local time
// so that "today" and "this calendar month" match what the user sees
// in their terminal.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	dbsqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// Sentinel errors returned by Ledger methods.
var (
	// ErrUnknownModel is returned by Record when no pricing is
	// configured for the (provider, model) pair in the incoming entry.
	ErrUnknownModel = errors.New("no pricing configured for model")

	// ErrInvalidArgument is returned when a method receives an argument
	// that violates a documented precondition.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrBudgetExceeded is a sentinel callers can use with errors.Is
	// after CheckBudget returns VerdictDeny.
	ErrBudgetExceeded = errors.New("budget exceeded")
)

// Window is a rollup window for Summary queries.
type Window string

const (
	// WindowSession rolls up all entries for a specific session.
	WindowSession Window = "session"
	// WindowDay rolls up entries created today in local time.
	WindowDay Window = "day"
	// WindowMonth rolls up entries created this calendar month in local
	// time.
	WindowMonth Window = "month"
	// WindowAll includes every ledger entry regardless of when it was
	// recorded.
	WindowAll Window = "all"
)

// VerdictKind is the outcome classification of a CheckBudget call.
type VerdictKind string

const (
	// VerdictAllowProceed means the planned spend fits within all caps.
	VerdictAllowProceed VerdictKind = "allow"
	// VerdictRequireConfirmation means the planned spend would cross the
	// 80% threshold for the first time on at least one cap window.
	VerdictRequireConfirmation VerdictKind = "confirm"
	// VerdictDeny means the planned spend would exceed at least one cap.
	VerdictDeny VerdictKind = "deny"
)

// Entry is one recorded LLM API call. CostUSD and CostINR on the
// incoming value are ignored; Record recomputes both from token counts.
type Entry struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Provider     string    `json:"provider"` // E.g. "anthropic", "deepseek", "moonshot".
	Model        string    `json:"model"`    // Provider-scoped model ID.
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"` // Ignored on input; recomputed.
	CostINR      float64   `json:"cost_inr"` // Ignored on input; recomputed.
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

// BudgetVerdict carries the outcome of CheckBudget plus the fields
// the TUI needs to render a meaningful message to the user.
type BudgetVerdict struct {
	Kind       VerdictKind `json:"kind"`
	Reason     string      `json:"reason"`      // Human-readable, INR-formatted.
	Window     Window      `json:"window"`      // Which window triggered the verdict.
	CapINR     float64     `json:"cap_inr"`     // The cap that applies to that window.
	CurrentINR float64     `json:"current_inr"` // Spend in that window so far.
	PlannedINR float64     `json:"planned_inr"` // The plannedCostINR argument.
}

// modelPricing holds per-million-token prices for one model.
type modelPricing struct {
	inputPerMTok  float64
	outputPerMTok float64
}

// Ledger is the public handle for cost tracking and budget enforcement.
// Construct with New; the zero value is not usable.
type Ledger struct {
	db      *db.DB
	queries *dbsqlc.Queries
	cfg     *config.LedgerConfig
	pricing map[string]modelPricing // keyed by "provider/model".
	bus     *pubsub.Topic[Summary]
	mu      sync.Mutex // guards Record → Publish atomicity.
}

// New constructs a Ledger. bus may be nil, in which case Record
// persists but does not publish.
func New(database *db.DB, cfg *config.LedgerConfig, models []config.Model, bus *pubsub.Topic[Summary]) *Ledger {
	pricing := make(map[string]modelPricing, len(models))
	for _, m := range models {
		key := strings.ToLower(m.Provider) + "/" + m.ID
		pricing[key] = modelPricing{
			inputPerMTok:  m.InputPricePerMTokUSD,
			outputPerMTok: m.OutputPricePerMTokUSD,
		}
	}
	return &Ledger{
		db:      database,
		queries: database.Queries,
		cfg:     cfg,
		pricing: pricing,
		bus:     bus,
	}
}

// roundCents rounds x to two decimal places using banker's-rounding
// semantics via math.Round, which satisfies the half-away-from-zero
// convention used throughout the INR cost display.
func roundCents(x float64) float64 {
	return math.Round(x*100) / 100
}

// Record persists e and, on success, publishes a fresh WindowSession
// Summary for e.SessionID on the bus. CostUSD and CostINR on the
// incoming Entry are ignored; Record recomputes both from e.InputTokens,
// e.OutputTokens, and config-derived prices for (e.Provider, e.Model).
// Returns ErrUnknownModel if no pricing is configured for the model.
func (l *Ledger) Record(ctx context.Context, e Entry) error {
	key := strings.ToLower(e.Provider) + "/" + e.Model
	p, ok := l.pricing[key]
	if !ok {
		return fmt.Errorf("pricing for %s/%s: %w", e.Provider, e.Model, ErrUnknownModel)
	}

	// Recompute costs from token counts; never trust caller-supplied values.
	usd := (float64(e.InputTokens)*p.inputPerMTok + float64(e.OutputTokens)*p.outputPerMTok) / 1_000_000
	inr := roundCents(usd * l.cfg.UsdInrRate)

	at := e.At
	if at.IsZero() {
		at = time.Now()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	_, err := l.queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           e.ID,
		SessionID:    e.SessionID,
		Provider:     e.Provider,
		Model:        e.Model,
		InputTokens:  int64(e.InputTokens),
		OutputTokens: int64(e.OutputTokens),
		CostUsd:      usd,
		CostInr:      inr,
		CreatedAt:    at.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("recording entry %s: %w", e.ID, err)
	}

	slog.InfoContext(
		ctx, "Recorded LLM call",
		"provider", e.Provider,
		"model", e.Model,
		"cost_inr", inr,
		"cost_usd", usd,
	)

	if l.bus != nil {
		sum, err := l.sessionSummary(ctx, e.SessionID)
		if err != nil {
			slog.WarnContext(
				ctx, "Failed to compute session summary for publish",
				"session_id", e.SessionID,
				"err", err,
			)
		} else {
			l.bus.Publish(ctx, sum)
		}
	}

	return nil
}

// sessionSummary builds a WindowSession Summary for the given session.
// Must be called with l.mu held to ensure consistent read-after-write.
func (l *Ledger) sessionSummary(ctx context.Context, sessionID string) (Summary, error) {
	row, err := l.queries.SumLedgerCostBySession(ctx, sessionID)
	if err != nil {
		return Summary{}, fmt.Errorf("querying session summary: %w", err)
	}
	return Summary{
		Window:       WindowSession,
		SessionID:    sessionID,
		CostUSD:      toFloat64(row.TotalUsd),
		CostINR:      toFloat64(row.TotalInr),
		InputTokens:  int(toInt64(row.TotalInput)),
		OutputTokens: int(toInt64(row.TotalOutput)),
		CallCount:    int(row.CallCount),
	}, nil
}

// Summary returns a rollup for the given window. When window is
// WindowSession, sessionID must be non-empty. For WindowDay,
// WindowMonth, and WindowAll, sessionID is ignored (pass "").
//
// Day and month windows are evaluated in local time so that the bucket
// boundaries match what the user sees in their terminal. Entries are
// stored as UTC unix-milliseconds and converted at read time.
func (l *Ledger) Summary(ctx context.Context, sessionID string, window Window) (Summary, error) {
	switch window {
	case WindowSession:
		row, err := l.queries.SumLedgerCostBySession(ctx, sessionID)
		if err != nil {
			return Summary{}, fmt.Errorf("querying session summary for %s: %w", sessionID, err)
		}
		return Summary{
			Window:       WindowSession,
			SessionID:    sessionID,
			CostUSD:      toFloat64(row.TotalUsd),
			CostINR:      toFloat64(row.TotalInr),
			InputTokens:  int(toInt64(row.TotalInput)),
			OutputTokens: int(toInt64(row.TotalOutput)),
			CallCount:    int(row.CallCount),
		}, nil

	case WindowDay:
		now := time.Now().Local()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endOfDay := startOfDay.Add(24 * time.Hour).Add(-time.Millisecond)
		return l.periodSummary(ctx, WindowDay, startOfDay, endOfDay)

	case WindowMonth:
		now := time.Now().Local()
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		endOfMonth := startOfMonth.AddDate(0, 1, 0).Add(-time.Millisecond)
		return l.periodSummary(ctx, WindowMonth, startOfMonth, endOfMonth)

	case WindowAll:
		// Use a very wide range to capture all entries.
		epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		far := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
		return l.periodSummary(ctx, WindowAll, epoch, far)

	default:
		return Summary{}, fmt.Errorf("unknown window %q: %w", window, ErrInvalidArgument)
	}
}

// periodSummary runs a SumLedgerCostAndTokensByPeriod query over the
// given half-open interval [start, end] (both inclusive, stored as
// unix milliseconds).
func (l *Ledger) periodSummary(ctx context.Context, win Window, start, end time.Time) (Summary, error) {
	row, err := l.queries.SumLedgerCostAndTokensByPeriod(ctx, dbsqlc.SumLedgerCostAndTokensByPeriodParams{
		CreatedAt:   start.UnixMilli(),
		CreatedAt_2: end.UnixMilli(),
	})
	if err != nil {
		return Summary{}, fmt.Errorf("querying %s summary: %w", win, err)
	}
	return Summary{
		Window:       win,
		CostUSD:      toFloat64(row.TotalUsd),
		CostINR:      toFloat64(row.TotalInr),
		InputTokens:  int(toInt64(row.TotalInput)),
		OutputTokens: int(toInt64(row.TotalOutput)),
		CallCount:    int(row.CallCount),
	}, nil
}

// CheckBudget answers: "if I add plannedCostINR rupees to the
// session/day/month totals, what should I do?".
//
// Returns:
//   - VerdictDeny if any cap is configured > 0 and current+planned
//     would exceed it.
//   - VerdictRequireConfirmation if any cap is configured > 0 and
//     current+planned would cross the 80% threshold for the first time
//     (i.e. current < 80% but current+planned >= 80%).
//   - VerdictAllowProceed otherwise.
//
// When multiple windows would deny, session > day > month
// (most-specific wins). The strictest deny wins outright over any
// confirmation. A negative plannedCostINR returns ErrInvalidArgument.
func (l *Ledger) CheckBudget(ctx context.Context, sessionID string, plannedCostINR float64) (BudgetVerdict, error) {
	if plannedCostINR < 0 {
		return BudgetVerdict{}, fmt.Errorf("plannedCostINR must be >= 0: %w", ErrInvalidArgument)
	}

	type windowCheck struct {
		window  Window
		cap     float64
		current float64
	}

	// Gather current spend for each capped window.
	checks := make([]windowCheck, 0, 3)

	if l.cfg.MaxInrPerSession > 0 {
		sum, err := l.Summary(ctx, sessionID, WindowSession)
		if err != nil {
			return BudgetVerdict{}, fmt.Errorf("checking session budget: %w", err)
		}
		checks = append(checks, windowCheck{WindowSession, l.cfg.MaxInrPerSession, sum.CostINR})
	}

	if l.cfg.MaxInrPerDay > 0 {
		sum, err := l.Summary(ctx, "", WindowDay)
		if err != nil {
			return BudgetVerdict{}, fmt.Errorf("checking day budget: %w", err)
		}
		checks = append(checks, windowCheck{WindowDay, l.cfg.MaxInrPerDay, sum.CostINR})
	}

	if l.cfg.MaxInrPerMonth > 0 {
		sum, err := l.Summary(ctx, "", WindowMonth)
		if err != nil {
			return BudgetVerdict{}, fmt.Errorf("checking month budget: %w", err)
		}
		checks = append(checks, windowCheck{WindowMonth, l.cfg.MaxInrPerMonth, sum.CostINR})
	}

	if len(checks) == 0 {
		return BudgetVerdict{
			Kind:       VerdictAllowProceed,
			Reason:     "no budget caps configured",
			PlannedINR: plannedCostINR,
		}, nil
	}

	// Evaluate each window. Collect the first deny (by precedence order
	// session > day > month) and the first confirmation.
	var denyVerdict *BudgetVerdict
	var confirmVerdict *BudgetVerdict

	for i := range checks {
		wc := &checks[i]
		projected := wc.current + plannedCostINR
		eighty := wc.cap * 0.80

		if projected >= wc.cap {
			// Hard deny.
			v := &BudgetVerdict{
				Kind:       VerdictDeny,
				Window:     wc.window,
				CapINR:     wc.cap,
				CurrentINR: wc.current,
				PlannedINR: plannedCostINR,
				Reason:     denyReason(wc.window, wc.cap, projected),
			}
			if denyVerdict == nil {
				// First deny wins (session before day before month).
				denyVerdict = v
			}
		} else if wc.current < eighty && projected >= eighty {
			// First crossing of the 80% threshold — ask for confirmation.
			pct := (projected / wc.cap) * 100
			v := &BudgetVerdict{
				Kind:       VerdictRequireConfirmation,
				Window:     wc.window,
				CapINR:     wc.cap,
				CurrentINR: wc.current,
				PlannedINR: plannedCostINR,
				Reason: fmt.Sprintf(
					"approaching %s cap (%.0f%% of ₹%.2f used after this call)",
					strings.ToLower(string(wc.window)), pct, wc.cap,
				),
			}
			if confirmVerdict == nil {
				confirmVerdict = v
			}
		}
	}

	// Deny always wins over confirmation.
	if denyVerdict != nil {
		return *denyVerdict, nil
	}
	if confirmVerdict != nil {
		return *confirmVerdict, nil
	}

	return BudgetVerdict{
		Kind:       VerdictAllowProceed,
		PlannedINR: plannedCostINR,
	}, nil
}

// denyReason builds the human-readable denial message for a window.
func denyReason(win Window, cap, projected float64) string {
	switch win {
	case WindowSession:
		return fmt.Sprintf("session cap (₹%.2f) exceeded — would be ₹%.2f", cap, projected)
	case WindowDay:
		return fmt.Sprintf("daily cap (₹%.2f) exceeded — would be ₹%.2f", cap, projected)
	case WindowMonth:
		return fmt.Sprintf("monthly cap (₹%.2f) exceeded — would be ₹%.2f", cap, projected)
	default:
		return fmt.Sprintf("cap (₹%.2f) exceeded — would be ₹%.2f", cap, projected)
	}
}

// toFloat64 converts an interface{} value returned by a SQLite COALESCE
// expression to float64. The modernc.org/sqlite driver returns float64
// for REAL columns and int64 for INTEGER columns; COALESCE with a
// numeric literal preserves the column's affinity.
func toFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		// Integer affinity column with a COALESCE default of 0.
		return float64(x)
	default:
		// Defensive fallback; should not be reached in practice.
		_ = x
		return 0
	}
}

// toInt64 converts an interface{} value returned by a SQLite COALESCE
// expression to int64. See toFloat64 for driver type semantics.
func toInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		// Real affinity column stored as COALESCE with 0.0.
		return int64(x)
	default:
		// Defensive fallback; should not be reached in practice.
		_ = x
		return 0
	}
}
