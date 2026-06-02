package ledger

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"
)

// usageCSVHeader is the fixed column order of the usage report. The
// columns mirror the per-(provider, model) breakdown the report emits:
// the two identity columns, the summed token counts, and the rolled-up
// USD and INR costs.
var usageCSVHeader = []string{
	"provider",
	"model",
	"input_tokens",
	"output_tokens",
	"cost_usd",
	"cost_inr",
}

// usageTotalLabel is the deterministic provider label of the trailing
// totals row. It is upper-cased so it sorts and reads distinctly from
// real provider names, which Record stores verbatim.
const usageTotalLabel = "TOTAL"

// usageRow is one aggregated (provider, model) group from the ledger.
// CostUSD is the precise summed USD for the group; CostINR is derived
// from it at the report boundary via summaryINR, never read back from the
// stored per-entry cost_inr column.
type usageRow struct {
	provider     string
	model        string
	inputTokens  int64
	outputTokens int64
	costUSD      float64
}

// UsageReportCSV renders a deterministic CSV usage report for the given
// window, one row per (provider, model) pair plus a trailing TOTAL row.
//
// The window selection mirrors Summary: WindowSession scopes to
// sessionID with no time bound (sessionID must be non-empty); WindowDay,
// WindowMonth, and WindowAll use the same inclusive, local-time
// created_at bounds and ignore sessionID. Rows are sorted by provider
// then model so the output is byte-for-byte reproducible across runs.
//
// Every INR value is derived from summed USD using the ledger's
// sum-then-round formula (summaryINR): each row's INR is
// roundCents(rowUSD * rate) and the TOTAL row's INR is
// roundCents(totalUSD * rate). Because each row rounds independently, the
// TOTAL INR is not in general the sum of the displayed per-row INR
// values; the TOTAL is the authoritative figure, matching Summary.
func (l *Ledger) UsageReportCSV(ctx context.Context, sessionID string, window Window) (string, error) {
	rows, err := l.usageRows(ctx, sessionID, window)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(usageCSVHeader); err != nil {
		return "", fmt.Errorf("writing usage report header: %w", err)
	}

	var totalUSD float64
	var totalInput, totalOutput int64
	for _, r := range rows {
		totalUSD += r.costUSD
		totalInput += r.inputTokens
		totalOutput += r.outputTokens
		if err := w.Write(l.usageCSVRow(r.provider, r.model, r.inputTokens, r.outputTokens, r.costUSD)); err != nil {
			return "", fmt.Errorf("writing usage report row for %s/%s: %w", r.provider, r.model, err)
		}
	}

	if err := w.Write(l.usageCSVRow(usageTotalLabel, "", totalInput, totalOutput, totalUSD)); err != nil {
		return "", fmt.Errorf("writing usage report total row: %w", err)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("flushing usage report: %w", err)
	}

	return buf.String(), nil
}

// usageCSVRow formats one report row. Token counts are rendered as plain
// integers, USD at fixed six-decimal precision (the micro-USD granularity
// the ledger computes at), and INR via summaryINR at two decimals so the
// rounding matches Summary.
func (l *Ledger) usageCSVRow(provider, model string, input, output int64, costUSD float64) []string {
	return []string{
		provider,
		model,
		strconv.FormatInt(input, 10),
		strconv.FormatInt(output, 10),
		strconv.FormatFloat(costUSD, 'f', 6, 64),
		strconv.FormatFloat(l.summaryINR(costUSD), 'f', 2, 64),
	}
}

// usageRows runs the per-(provider, model) aggregation for the given
// window and returns the groups sorted by provider then model. It selects
// SUM(cost_usd) and the token sums only — never SUM(cost_inr) — so INR is
// always re-derived from summed USD at the report boundary.
func (l *Ledger) usageRows(ctx context.Context, sessionID string, window Window) ([]usageRow, error) {
	const selectExpr = `SELECT provider, model,
        COALESCE(SUM(input_tokens), 0) AS total_input,
        COALESCE(SUM(output_tokens), 0) AS total_output,
        COALESCE(SUM(cost_usd), 0.0) AS total_usd
    FROM ledger_entries`
	const groupOrder = ` GROUP BY provider, model ORDER BY provider ASC, model ASC`

	var (
		query string
		args  []any
	)
	switch window {
	case WindowSession:
		if sessionID == "" {
			return nil, fmt.Errorf("sessionID must be non-empty for window %q: %w", window, ErrInvalidArgument)
		}
		query = selectExpr + ` WHERE session_id = ?` + groupOrder
		args = []any{sessionID}

	case WindowDay:
		now := time.Now().Local()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end := start.Add(24 * time.Hour).Add(-time.Millisecond)
		query = selectExpr + ` WHERE created_at >= ? AND created_at <= ?` + groupOrder
		args = []any{start.UnixMilli(), end.UnixMilli()}

	case WindowMonth:
		now := time.Now().Local()
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 1, 0).Add(-time.Millisecond)
		query = selectExpr + ` WHERE created_at >= ? AND created_at <= ?` + groupOrder
		args = []any{start.UnixMilli(), end.UnixMilli()}

	case WindowAll:
		epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		far := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
		query = selectExpr + ` WHERE created_at >= ? AND created_at <= ?` + groupOrder
		args = []any{epoch.UnixMilli(), far.UnixMilli()}

	default:
		return nil, fmt.Errorf("unknown window %q: %w", window, ErrInvalidArgument)
	}

	sqlRows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying usage rows for window %q: %w", window, err)
	}
	defer func() { _ = sqlRows.Close() }()

	var out []usageRow
	for sqlRows.Next() {
		var r usageRow
		if err := sqlRows.Scan(&r.provider, &r.model, &r.inputTokens, &r.outputTokens, &r.costUSD); err != nil {
			return nil, fmt.Errorf("scanning usage row: %w", err)
		}
		out = append(out, r)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating usage rows: %w", err)
	}

	return out, nil
}
