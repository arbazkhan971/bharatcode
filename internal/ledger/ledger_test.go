package ledger_test

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	dbsqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// openTestDB opens a fresh in-memory SQLite database for one test. The
// returned *db.DB and its cleanup function are both wired to t.Cleanup.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := t.TempDir() + "/test.db"
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// createSession inserts a minimal session row so ledger FK constraint passes.
func createSession(t *testing.T, d *db.DB, sessionID string) {
	t.Helper()
	ctx := context.Background()
	_, err := d.Queries.CreateSession(ctx, dbsqlc.CreateSessionParams{
		ID:          sessionID,
		ProjectPath: "/tmp/project",
		Title:       "Test session",
		Model:       "deepseek-chat",
		Agent:       "coder",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)
}

// defaultCfg returns a LedgerConfig with the default INR rate and no caps.
func defaultCfg() *config.LedgerConfig {
	return &config.LedgerConfig{
		Currency:   "INR",
		UsdInrRate: 83.50,
	}
}

// defaultModels returns a minimal set of model pricing entries.
func defaultModels() []config.Model {
	return []config.Model{
		{
			ID:                    "deepseek-chat",
			Provider:              "deepseek",
			InputPricePerMTokUSD:  0.27,
			OutputPricePerMTokUSD: 1.10,
		},
		{
			ID:                    "claude-sonnet-4-5",
			Provider:              "anthropic",
			InputPricePerMTokUSD:  3.00,
			OutputPricePerMTokUSD: 15.00,
		},
	}
}

// newEntry builds a test Entry with sensible defaults.
func newEntry(sessionID, id, provider, model string, input, output int) ledger.Entry {
	return ledger.Entry{
		ID:           id,
		SessionID:    sessionID,
		Provider:     provider,
		Model:        model,
		InputTokens:  input,
		OutputTokens: output,
		// CostUSD / CostINR intentionally left as the zero value — they
		// must be recomputed by Record.
		CostUSD: 999.0, // should be ignored.
		CostINR: 999.0, // should be ignored.
		At:      time.Now(),
	}
}

// -------------------------------- Record tests --------------------------------

func TestRecord_PersistsEntry(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	e := newEntry("sess-1", "entry-1", "deepseek", "deepseek-chat", 1000, 500)
	require.NoError(t, l.Record(ctx, e))

	var count int
	err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM ledger_entries").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRecord_RecomputesCostUSD(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  2.0,
		OutputPricePerMTokUSD: 8.0,
	}}
	cfg := defaultCfg()
	l := ledger.New(d, cfg, models, nil)

	// 1000 input tokens at $2/Mtok = $0.002
	// 500 output tokens at $8/Mtok = $0.004
	// total = $0.006
	expectedUSD := (1000.0*2.0 + 500.0*8.0) / 1_000_000

	e := newEntry("sess-1", "entry-1", "myprovider", "mymodel", 1000, 500)
	require.NoError(t, l.Record(ctx, e))

	var gotUSD float64
	err := d.QueryRowContext(ctx, "SELECT cost_usd FROM ledger_entries WHERE id = ?", "entry-1").Scan(&gotUSD)
	require.NoError(t, err)
	require.InDelta(t, expectedUSD, gotUSD, 1e-9, "cost_usd must be recomputed, not taken from entry")
}

func TestRecord_RecomputesCostINR_DefaultRate(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Target: CostUSD == 0.10 => CostINR == 8.35 at rate 83.50.
	// Input: x/1M * price = 0.10 => choose 100000 tokens at $1/Mtok.
	models := []config.Model{{
		ID:                    "testmodel",
		Provider:              "testprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 0,
	}}
	cfg := &config.LedgerConfig{Currency: "INR", UsdInrRate: 83.50}
	l := ledger.New(d, cfg, models, nil)

	e := newEntry("sess-1", "entry-1", "testprovider", "testmodel", 100_000, 0)
	require.NoError(t, l.Record(ctx, e))

	var gotINR float64
	err := d.QueryRowContext(ctx, "SELECT cost_inr FROM ledger_entries WHERE id = ?", "entry-1").Scan(&gotINR)
	require.NoError(t, err)
	// 100000 / 1e6 * 1.0 = 0.10 USD; 0.10 * 83.50 = 8.35 INR.
	require.InDelta(t, 8.35, gotINR, 0.01, "cost_inr should be 8.35")
}

func TestRecord_RecomputesCostINR_CustomRate(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "testmodel",
		Provider:              "testprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 0,
	}}
	cfg := &config.LedgerConfig{Currency: "INR", UsdInrRate: 90.00}
	l := ledger.New(d, cfg, models, nil)

	e := newEntry("sess-1", "entry-1", "testprovider", "testmodel", 100_000, 0)
	require.NoError(t, l.Record(ctx, e))

	var gotINR float64
	err := d.QueryRowContext(ctx, "SELECT cost_inr FROM ledger_entries WHERE id = ?", "entry-1").Scan(&gotINR)
	require.NoError(t, err)
	// 0.10 USD * 90.00 = 9.00 INR.
	require.InDelta(t, 9.00, gotINR, 0.01, "cost_inr should be 9.00 at rate 90.00")
}

func TestRecord_UnknownModel_ReturnsErrUnknownModel(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	e := newEntry("sess-1", "entry-1", "unknownprovider", "unknownmodel", 1000, 500)
	err := l.Record(ctx, e)
	require.Error(t, err)
	require.ErrorIs(t, err, ledger.ErrUnknownModel)
}

func TestRecord_PublishesSummary(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	bus := pubsub.NewTopic[ledger.Summary]("test-ledger", 8)
	defer bus.Close()

	events, cancel := bus.Subscribe()
	defer cancel()

	l := ledger.New(d, defaultCfg(), defaultModels(), bus)
	e := newEntry("sess-1", "entry-1", "deepseek", "deepseek-chat", 1000, 500)
	require.NoError(t, l.Record(ctx, e))

	select {
	case sum := <-events:
		require.Equal(t, ledger.WindowSession, sum.Window)
		require.Equal(t, "sess-1", sum.SessionID)
		require.Equal(t, 1, sum.CallCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for summary on bus")
	}
}

func TestRecord_NilBus_NoPanic(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Pass nil bus — should not panic.
	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	e := newEntry("sess-1", "entry-1", "deepseek", "deepseek-chat", 1000, 500)
	require.NoError(t, l.Record(ctx, e))

	var count int
	err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM ledger_entries").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// -------------------------------- Summary tests -------------------------------

func TestSummary_Session_AggregatesEntries(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  2.0,
		OutputPricePerMTokUSD: 8.0,
	}}
	l := ledger.New(d, defaultCfg(), models, nil)

	e1 := newEntry("sess-1", "e1", "myprovider", "mymodel", 500, 200)
	e2 := newEntry("sess-1", "e2", "myprovider", "mymodel", 300, 100)
	require.NoError(t, l.Record(ctx, e1))
	require.NoError(t, l.Record(ctx, e2))

	sum, err := l.Summary(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)
	require.Equal(t, ledger.WindowSession, sum.Window)
	require.Equal(t, "sess-1", sum.SessionID)
	require.Equal(t, 2, sum.CallCount)
	require.Equal(t, 800, sum.InputTokens)
	require.Equal(t, 300, sum.OutputTokens)
	// Verify CostUSD = (500*2 + 200*8 + 300*2 + 100*8) / 1M = (1000+1600+600+800)/1M = 4000/1M.
	expectedUSD := 4000.0 / 1_000_000
	require.InDelta(t, expectedUSD, sum.CostUSD, 1e-9)
	// INR = round_cents(USD * 83.50).
	expectedINR := math.Round(expectedUSD*83.50*100) / 100
	require.InDelta(t, expectedINR, sum.CostINR, 0.01)
}

func TestSummary_Day_ScopedToToday(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 1.0,
	}}
	l := ledger.New(d, defaultCfg(), models, nil)

	// Insert entry for today.
	e1 := newEntry("sess-1", "today-entry", "myprovider", "mymodel", 1000, 0)
	e1.At = time.Now()
	require.NoError(t, l.Record(ctx, e1))

	// Insert entry for yesterday directly so we can set its created_at.
	yesterday := time.Now().AddDate(0, 0, -1)
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "yesterday-entry",
		SessionID:    "sess-1",
		Provider:     "myprovider",
		Model:        "mymodel",
		InputTokens:  5000,
		OutputTokens: 0,
		CostUsd:      0.005,
		CostInr:      0.42,
		CreatedAt:    yesterday.UnixMilli(),
	})
	require.NoError(t, err)

	sum, err := l.Summary(ctx, "", ledger.WindowDay)
	require.NoError(t, err)
	require.Equal(t, ledger.WindowDay, sum.Window)
	// Only today's entry should be counted.
	require.Equal(t, 1, sum.CallCount)
	require.Equal(t, 1000, sum.InputTokens)
}

func TestSummary_Month_ScopedToCurrentMonth(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 1.0,
	}}
	l := ledger.New(d, defaultCfg(), models, nil)

	// Today's entry.
	e1 := newEntry("sess-1", "this-month-entry", "myprovider", "mymodel", 1000, 0)
	e1.At = time.Now()
	require.NoError(t, l.Record(ctx, e1))

	// Last month entry — inserted directly.
	lastMonth := time.Now().AddDate(0, -1, 0)
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "last-month-entry",
		SessionID:    "sess-1",
		Provider:     "myprovider",
		Model:        "mymodel",
		InputTokens:  9000,
		OutputTokens: 0,
		CostUsd:      0.009,
		CostInr:      0.75,
		CreatedAt:    lastMonth.UnixMilli(),
	})
	require.NoError(t, err)

	sum, err := l.Summary(ctx, "", ledger.WindowMonth)
	require.NoError(t, err)
	require.Equal(t, ledger.WindowMonth, sum.Window)
	require.Equal(t, 1, sum.CallCount)
}

func TestSummary_All_IncludesEverything(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 1.0,
	}}
	l := ledger.New(d, defaultCfg(), models, nil)

	// Today.
	e1 := newEntry("sess-1", "today", "myprovider", "mymodel", 100, 0)
	e1.At = time.Now()
	require.NoError(t, l.Record(ctx, e1))

	// Yesterday.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "yesterday",
		SessionID:    "sess-1",
		Provider:     "myprovider",
		Model:        "mymodel",
		InputTokens:  200,
		OutputTokens: 0,
		CostUsd:      0.0002,
		CostInr:      0.02,
		CreatedAt:    time.Now().AddDate(0, 0, -1).UnixMilli(),
	})
	require.NoError(t, err)

	// Last month.
	_, err = d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "last-month",
		SessionID:    "sess-1",
		Provider:     "myprovider",
		Model:        "mymodel",
		InputTokens:  300,
		OutputTokens: 0,
		CostUsd:      0.0003,
		CostInr:      0.03,
		CreatedAt:    time.Now().AddDate(0, -1, 0).UnixMilli(),
	})
	require.NoError(t, err)

	sum, err := l.Summary(ctx, "", ledger.WindowAll)
	require.NoError(t, err)
	require.Equal(t, 3, sum.CallCount)
	require.Equal(t, 600, sum.InputTokens)
}

func TestSummary_EmptyWindow_ReturnsZeroes(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)

	for _, win := range []ledger.Window{
		ledger.WindowSession,
		ledger.WindowDay,
		ledger.WindowMonth,
		ledger.WindowAll,
	} {
		sum, err := l.Summary(ctx, "sess-1", win)
		require.NoError(t, err, "window: %s", win)
		require.Equal(t, 0.0, sum.CostUSD, "window: %s", win)
		require.Equal(t, 0.0, sum.CostINR, "window: %s", win)
		require.Equal(t, 0, sum.CallCount, "window: %s", win)
	}
}

// ----------------------------- CheckBudget tests ------------------------------

func TestCheckBudget_AllowProceed_NoCapsConfigured(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{UsdInrRate: 83.50} // all MaxINR* are 0.
	l := ledger.New(d, cfg, defaultModels(), nil)

	verdict, err := l.CheckBudget(ctx, "sess-1", 100.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictAllowProceed, verdict.Kind)
}

func TestCheckBudget_AllowProceed_UnderAllThresholds(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Current spend = 0; planned = 30 INR; cap = 100 INR. 30% => allow.
	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 100.0,
		MaxInrPerDay:     200.0,
		MaxInrPerMonth:   500.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	verdict, err := l.CheckBudget(ctx, "sess-1", 30.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictAllowProceed, verdict.Kind)
}

func TestCheckBudget_RequireConfirmation_CrossesEightyPercent(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Session cap = 100; set current to ~70 by direct insert.
	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 100.0,
	}
	models := []config.Model{{
		ID:                    "mymodel",
		Provider:              "myprovider",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 0,
	}}
	l := ledger.New(d, cfg, models, nil)

	// Inject 70 INR directly to simulate current session spend.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "existing",
		SessionID:    "sess-1",
		Provider:     "myprovider",
		Model:        "mymodel",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      0.838,
		CostInr:      70.0, // 70% of 100 cap.
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	// Planned = 15 INR: 70 + 15 = 85 >= 80 but < 100 => confirmation.
	verdict, err := l.CheckBudget(ctx, "sess-1", 15.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictRequireConfirmation, verdict.Kind)
	require.Equal(t, ledger.WindowSession, verdict.Window)
}

func TestCheckBudget_Deny_SessionCapExceeded(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 500.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	// Current = 493; planned = 19.30 => 512.30 > 500 => deny.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "existing",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      5.90,
		CostInr:      493.0,
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	verdict, err := l.CheckBudget(ctx, "sess-1", 19.30)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictDeny, verdict.Kind)
	require.Equal(t, ledger.WindowSession, verdict.Window)
	require.Contains(t, verdict.Reason, "session cap")
	require.Contains(t, verdict.Reason, "₹500.00")
}

func TestCheckBudget_Deny_DayCapExceeded(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{
		UsdInrRate:   83.50,
		MaxInrPerDay: 200.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	// Current day spend = 195 INR; planned = 10 => 205 > 200 => deny.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "today",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      2.34,
		CostInr:      195.0,
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	verdict, err := l.CheckBudget(ctx, "sess-1", 10.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictDeny, verdict.Kind)
	require.Equal(t, ledger.WindowDay, verdict.Window)
	require.Contains(t, verdict.Reason, "daily cap")
}

func TestCheckBudget_Deny_MonthCapExceeded(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{
		UsdInrRate:     83.50,
		MaxInrPerMonth: 1000.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	// Current month spend = 995 INR; planned = 10 => 1005 > 1000 => deny.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "this-month",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      11.92,
		CostInr:      995.0,
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	verdict, err := l.CheckBudget(ctx, "sess-1", 10.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictDeny, verdict.Kind)
	require.Equal(t, ledger.WindowMonth, verdict.Window)
	require.Contains(t, verdict.Reason, "monthly cap")
}

func TestCheckBudget_DenyWinsOverConfirmation(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Setup: session cap=100, day cap=50.
	// Place 48 INR today; session=48 (48% of 100), day=48 (96% of 50).
	// Planned = 3 INR: session 48+3=51 → below 80% → allow.
	//                  day    48+3=51 > 50 cap → deny.
	// Deny must win over allow.
	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 100.0,
		MaxInrPerDay:     50.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "spend",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      0.575,
		CostInr:      48.0,
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	verdict, err := l.CheckBudget(ctx, "sess-1", 3.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictDeny, verdict.Kind)
	require.Equal(t, ledger.WindowDay, verdict.Window)
}

func TestCheckBudget_NegativePlanned_ReturnsErrInvalidArgument(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	_, err := l.CheckBudget(ctx, "sess-1", -1.0)
	require.Error(t, err)
	require.ErrorIs(t, err, ledger.ErrInvalidArgument)
}

func TestRecord_ThenSummary_INRPrecision(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Three entries with awkward USD costs; verify summed INR matches
	// round-to-cent(sum * 83.50).
	models := []config.Model{{
		ID:                    "m",
		Provider:              "p",
		InputPricePerMTokUSD:  1.0,
		OutputPricePerMTokUSD: 1.0,
	}}
	// Entry costs in USD (from token counts):
	// 700 in + 0 out @ $1/Mtok = 0.0007 USD
	// 13000 in + 0 out @ $1/Mtok = 0.013 USD
	// 210 in + 0 out @ $1/Mtok = 0.00021 USD
	l := ledger.New(d, defaultCfg(), models, nil)

	for i, tokens := range []int{700, 13000, 210} {
		e := newEntry("sess-1", fmt.Sprintf("e%d", i), "p", "m", tokens, 0)
		require.NoError(t, l.Record(ctx, e))
	}

	sum, err := l.Summary(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)

	totalUSD := (700.0 + 13000.0 + 210.0) / 1_000_000
	// Each record is rounded individually; sum may differ from sum-then-round.
	// The acceptance criterion says: summed INR matches round(sum * 83.50).
	expectedINR := math.Round(totalUSD*83.50*100) / 100
	require.InDelta(t, expectedINR, sum.CostINR, 0.02)
}

func TestCheckBudget_AlreadyOverEighty_DoesNotReconfirm(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 100.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	// Set current spend to 85 INR (already over the 80% threshold).
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "over80",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      1.0,
		CostInr:      85.0, // 85% of 100.
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	// Planned = 5 INR: 85 + 5 = 90 (still < 100). Current already >= 80%.
	// We are already past the threshold → should NOT reconfirm → allow.
	verdict, err := l.CheckBudget(ctx, "sess-1", 5.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictAllowProceed, verdict.Kind,
		"already over 80%%, subsequent calls should not re-trigger confirmation")
}

func TestConcurrentRecord_NoDataRace(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			e := newEntry("sess-1", fmt.Sprintf("entry-%d", id), "deepseek", "deepseek-chat", 100, 50)
			if err := l.Record(ctx, e); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	sum, err := l.Summary(ctx, "sess-1", ledger.WindowSession)
	require.NoError(t, err)
	require.Equal(t, workers, sum.CallCount)
}

// TestSummary_InvalidWindow_ReturnsError verifies that an unknown Window
// constant is rejected with ErrInvalidArgument.
func TestSummary_InvalidWindow_ReturnsError(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	_, err := l.Summary(ctx, "sess-1", ledger.Window("bogus"))
	require.Error(t, err)
	require.ErrorIs(t, err, ledger.ErrInvalidArgument)
}

// TestRecord_ZeroAt_UsesNow verifies that when Entry.At is the zero time,
// Record substitutes time.Now() so the entry is visible in WindowDay.
func TestRecord_ZeroAt_UsesNow(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	e := ledger.Entry{
		ID:           "e-zero-at",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  1000,
		OutputTokens: 0,
		// At is intentionally left as zero time.
	}
	require.NoError(t, l.Record(ctx, e))

	sum, err := l.Summary(ctx, "", ledger.WindowDay)
	require.NoError(t, err)
	require.Equal(t, 1, sum.CallCount, "entry with zero At should appear in today's window")
}

// TestCheckBudget_SessionAndMonthDeny_SessionWins verifies that when both
// session and month caps would be denied, session (more specific) wins.
func TestCheckBudget_SessionAndMonthDeny_SessionWins(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	cfg := &config.LedgerConfig{
		UsdInrRate:       83.50,
		MaxInrPerSession: 100.0,
		MaxInrPerMonth:   200.0,
	}
	l := ledger.New(d, cfg, defaultModels(), nil)

	// Inject 99 INR today for sess-1; both session and month are at 99%.
	_, err := d.Queries.AppendLedgerEntry(ctx, dbsqlc.AppendLedgerEntryParams{
		ID:           "big",
		SessionID:    "sess-1",
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InputTokens:  0,
		OutputTokens: 0,
		CostUsd:      1.19,
		CostInr:      99.0,
		CreatedAt:    time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	// planned = 5: session 99+5=104 > 100 (deny); month 99+5=104 < 200 (allow).
	// Session deny wins.
	verdict, err := l.CheckBudget(ctx, "sess-1", 5.0)
	require.NoError(t, err)
	require.Equal(t, ledger.VerdictDeny, verdict.Kind)
	require.Equal(t, ledger.WindowSession, verdict.Window)
	require.Contains(t, verdict.Reason, "session cap")
}

// TestRecord_DBError propagates the DB error so the caller gets a wrapped
// error instead of a panic.
func TestRecord_DBError(t *testing.T) {
	d := openTestDB(t)
	// Do NOT create session — FK constraint will cause AppendLedgerEntry to fail.
	ctx := context.Background()

	l := ledger.New(d, defaultCfg(), defaultModels(), nil)
	e := newEntry("nonexistent-session", "e1", "deepseek", "deepseek-chat", 100, 50)
	err := l.Record(ctx, e)
	require.Error(t, err, "should propagate DB error for missing session FK")
}
