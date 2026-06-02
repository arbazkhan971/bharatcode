package ledger_test

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
)

// reportModels prices every test model at $1 per million input tokens and
// $0 per output token, so each entry's USD cost is exactly
// input_tokens / 1_000_000. That keeps the expected INR arithmetic
// hand-checkable while still exercising the sum-then-round boundary.
func reportModels() []config.Model {
	return []config.Model{
		{
			ID:                    "claude",
			Provider:              "anthropic",
			InputPricePerMTokUSD:  1.0,
			OutputPricePerMTokUSD: 0,
		},
		{
			ID:                    "chat",
			Provider:              "deepseek",
			InputPricePerMTokUSD:  1.0,
			OutputPricePerMTokUSD: 0,
		},
	}
}

// TestUsageReportCSV_RowsTotalsAndRounding records entries across two
// providers/models and asserts the exact CSV: the header, one sorted row
// per (provider, model) group with summed tokens and costs, and a TOTAL
// row whose INR is derived sum-then-round.
//
// The token counts discriminate the rounding fix on two independent
// fronts (the same way TestRecord_ThenSummary_INRPrecision separates 1.16
// from 1.17):
//
//   - Within-group: anthropic/claude pools entries of 100 and 180 input.
//     Summing the stored per-entry cost_inr gives 0.01 + 0.02 = 0.03, but
//     the correct row is roundCents((280/1e6)*83.50) = roundCents(0.02338)
//     = 0.02. The row must be 0.02.
//   - Cross-group: the TOTAL is roundCents((420/1e6)*83.50) =
//     roundCents(0.03507) = 0.04, while summing the displayed per-row INR
//     (0.02 + 0.01) yields only 0.03. The TOTAL must be 0.04.
func TestUsageReportCSV_RowsTotalsAndRounding(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)

	// anthropic/claude: two entries (100 + 180 input) -> group input 280.
	// Output tokens are non-zero but priced at $0, so they affect only the
	// output_tokens column, not the cost.
	require.NoError(t, l.Record(ctx, newEntry("sess-1", "a1", "anthropic", "claude", 100, 30)))
	require.NoError(t, l.Record(ctx, newEntry("sess-1", "a2", "anthropic", "claude", 180, 70)))
	// deepseek/chat: one entry, 140 input.
	require.NoError(t, l.Record(ctx, newEntry("sess-1", "d1", "deepseek", "chat", 140, 5)))

	out, err := l.UsageReportCSV(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)

	// Expected USD per group: input/1e6. INR per row: roundCents(usd*83.50).
	//   anthropic/claude: 280/1e6 = 0.000280 USD -> 0.02 INR
	//   deepseek/chat   : 140/1e6 = 0.000140 USD -> 0.01 INR
	//   TOTAL           : 420/1e6 = 0.000420 USD -> roundCents(0.03507) = 0.04 INR
	wantCSV := strings.Join([]string{
		"provider,model,input_tokens,output_tokens,cost_usd,cost_inr",
		"anthropic,claude,280,100,0.000280,0.02",
		"deepseek,chat,140,5,0.000140,0.01",
		"TOTAL,,420,105,0.000420,0.04",
		"",
	}, "\n")
	require.Equal(t, wantCSV, out,
		"CSV must list sorted per-group rows plus a sum-then-round TOTAL (0.04, not the per-row sum 0.03)")

	// Also parse it structurally to assert the discrimination explicitly:
	// the claude row is 0.02 (sum-then-round) not the per-entry sum 0.03,
	// and the TOTAL is 0.04 not the per-row sum 0.03.
	records, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 4, "header + 2 group rows + TOTAL row")
	require.Equal(t,
		[]string{"provider", "model", "input_tokens", "output_tokens", "cost_usd", "cost_inr"},
		records[0])
	require.Equal(t, "anthropic", records[1][0], "rows must be sorted by provider")
	require.Equal(t, "deepseek", records[2][0])
	require.Equal(t, "TOTAL", records[3][0])
	require.Equal(t, "0.02", records[1][5], "claude row INR is sum-then-round, not the per-entry sum 0.03")
	require.Equal(t, "0.01", records[2][5])
	require.Equal(t, "0.04", records[3][5], "TOTAL INR is sum-then-round, not the per-row sum 0.03")
}

// TestUsageReportCSV_Deterministic verifies the report is byte-for-byte
// reproducible and sorted by provider then model regardless of the order
// entries were recorded.
func TestUsageReportCSV_Deterministic(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)

	// Record in an order that does NOT match the sorted output.
	require.NoError(t, l.Record(ctx, newEntry("sess-1", "z", "deepseek", "chat", 50, 0)))
	require.NoError(t, l.Record(ctx, newEntry("sess-1", "y", "anthropic", "claude", 50, 0)))

	first, err := l.UsageReportCSV(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)
	second, err := l.UsageReportCSV(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)
	require.Equal(t, first, second, "report must be deterministic across runs")

	lines := strings.Split(strings.TrimRight(first, "\n"), "\n")
	require.Equal(t, "provider,model,input_tokens,output_tokens,cost_usd,cost_inr", lines[0])
	require.True(t, strings.HasPrefix(lines[1], "anthropic,claude,"),
		"anthropic must sort before deepseek; got %q", lines[1])
	require.True(t, strings.HasPrefix(lines[2], "deepseek,chat,"), "got %q", lines[2])
	require.True(t, strings.HasPrefix(lines[3], "TOTAL,,"), "got %q", lines[3])
}

// TestUsageReportCSV_EmptyWindow returns just the header and a zeroed
// TOTAL row when no entries match the window.
func TestUsageReportCSV_EmptyWindow(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)

	out, err := l.UsageReportCSV(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)

	want := strings.Join([]string{
		"provider,model,input_tokens,output_tokens,cost_usd,cost_inr",
		"TOTAL,,0,0,0.000000,0.00",
		"",
	}, "\n")
	require.Equal(t, want, out)
}

// TestUsageReportCSV_WindowAll includes entries across days and ignores
// the session filter, aggregating every provider/model in the store.
func TestUsageReportCSV_WindowAll(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	createSession(t, d, "sess-2")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)

	// Today, session 1.
	e1 := newEntry("sess-1", "t1", "anthropic", "claude", 1000, 0)
	e1.At = time.Now()
	require.NoError(t, l.Record(ctx, e1))
	// Last month, session 2 — different session, older time.
	e2 := newEntry("sess-2", "t2", "anthropic", "claude", 2000, 0)
	e2.At = time.Now().AddDate(0, -1, 0)
	require.NoError(t, l.Record(ctx, e2))

	out, err := l.UsageReportCSV(ctx, "", ledger.WindowAll)
	require.NoError(t, err)

	// Both entries fold into the single anthropic/claude group: 3000 input.
	// USD = 3000/1e6 = 0.003000; INR = roundCents(0.003*83.50) = 0.25.
	want := strings.Join([]string{
		"provider,model,input_tokens,output_tokens,cost_usd,cost_inr",
		"anthropic,claude,3000,0,0.003000,0.25",
		"TOTAL,,3000,0,0.003000,0.25",
		"",
	}, "\n")
	require.Equal(t, want, out)
}

// TestUsageReportCSV_SessionRequiresID rejects WindowSession with an empty
// session ID, mirroring Summary's precondition.
func TestUsageReportCSV_SessionRequiresID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)
	_, err := l.UsageReportCSV(ctx, "", ledger.WindowSession)
	require.ErrorIs(t, err, ledger.ErrInvalidArgument)
}

// TestUsageReportCSV_InvalidWindow rejects an unknown window constant.
func TestUsageReportCSV_InvalidWindow(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), reportModels(), nil)
	_, err := l.UsageReportCSV(ctx, "sess-1", ledger.Window("bogus"))
	require.ErrorIs(t, err, ledger.ErrInvalidArgument)
}
