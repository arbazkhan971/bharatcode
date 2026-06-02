package ledger_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
)

// monthCapCfg returns a LedgerConfig with a monthly cap and default rate.
func monthCapCfg(maxMonth float64) *config.LedgerConfig {
	return &config.LedgerConfig{
		Currency:       "INR",
		UsdInrRate:     83.50,
		MaxInrPerMonth: maxMonth,
	}
}

// thresholds extracts the crossed threshold fractions from a slice of alerts
// for concise assertions.
func thresholds(alerts []ledger.BudgetAlert) []float64 {
	out := make([]float64, len(alerts))
	for i, a := range alerts {
		out[i] = a.Threshold
	}
	return out
}

// TestCheckSpendAlerts_CrossesFiftyThenEighty_FiresOnceEach is the core
// scenario: spend crosses 50% then 80% of the monthly budget. Each alert must
// fire exactly once, and a re-check at the same spend must fire nothing.
func TestCheckSpendAlerts_CrossesFiftyThenEighty_FiresOnceEach(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// Monthly cap = 100 INR. Default thresholds 50%/80%/100%.
	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	// Spend = 55 INR: crosses 50% (>= 50) but not 80% (< 80).
	got := l.CheckSpendAlerts(ctx, 55.0)
	require.Equal(t, []float64{0.50}, thresholds(got), "only the 50%% alert fires at 55%% spend")

	// Re-check at the same spend: 50% already fired, nothing new.
	got = l.CheckSpendAlerts(ctx, 55.0)
	require.Empty(t, got, "re-checking the same spend must not re-fire the 50%% alert")

	// Spend rises to 82 INR: crosses 80% (>= 80) but not 100%.
	got = l.CheckSpendAlerts(ctx, 82.0)
	require.Equal(t, []float64{0.80}, thresholds(got), "only the 80%% alert fires at 82%% spend")

	// Re-check at 82: nothing new (50% and 80% already fired).
	got = l.CheckSpendAlerts(ctx, 82.0)
	require.Empty(t, got, "re-checking 82%% spend must not re-fire 50%% or 80%%")

	// Even dipping back down (refund / correction) must not re-arm.
	got = l.CheckSpendAlerts(ctx, 60.0)
	require.Empty(t, got, "lower spend must not re-fire an already-fired threshold")
}

// TestCheckSpendAlerts_OverBudget_FiresHundredPercentOnce verifies the
// 100%/over-budget alert fires when spend reaches the cap and fires only once.
func TestCheckSpendAlerts_OverBudget_FiresHundredPercentOnce(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	// Jump straight to 120 INR (over budget): all three thresholds cross at
	// once in a single call.
	got := l.CheckSpendAlerts(ctx, 120.0)
	require.Equal(t, []float64{0.50, 0.80, 1.00}, thresholds(got),
		"a jump over budget fires 50%%, 80%% and 100%% together, in ascending order")

	// The 100% alert carries the cap and spend for rendering.
	last := got[len(got)-1]
	require.Equal(t, 1.00, last.Threshold)
	require.Equal(t, 100.0, last.CapINR)
	require.Equal(t, 120.0, last.SpendINR)

	// Re-check while still over budget: nothing re-fires.
	got = l.CheckSpendAlerts(ctx, 130.0)
	require.Empty(t, got, "the over-budget alert must not re-fire on subsequent checks")
}

// TestCheckSpendAlerts_ExactlyAtThreshold_Fires verifies the boundary: spend
// exactly equal to threshold*cap counts as crossed (>=, not >).
func TestCheckSpendAlerts_ExactlyAtThreshold_Fires(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(200.0), defaultModels(), nil)

	// Exactly 50% of 200 = 100.
	got := l.CheckSpendAlerts(ctx, 100.0)
	require.Equal(t, []float64{0.50}, thresholds(got), "spend exactly at 50%% of cap fires the 50%% alert")

	// Exactly at the cap fires 80% and 100% together.
	got = l.CheckSpendAlerts(ctx, 200.0)
	require.Equal(t, []float64{0.80, 1.00}, thresholds(got), "spend exactly at the cap fires 80%% and 100%%")
}

// TestCheckSpendAlerts_InvokesSink verifies the injectable callback receives
// each newly-crossed threshold exactly once, in ascending order, and not
// again on re-check.
func TestCheckSpendAlerts_InvokesSink(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	var mu sync.Mutex
	var fired []float64
	l.SetAlertSink(func(_ context.Context, a ledger.BudgetAlert) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, a.Threshold)
	})

	// Cross 50%.
	l.CheckSpendAlerts(ctx, 55.0)
	// Cross 80% and 100% at once.
	l.CheckSpendAlerts(ctx, 100.0)
	// Re-check: no new sink calls.
	l.CheckSpendAlerts(ctx, 100.0)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []float64{0.50, 0.80, 1.00}, fired,
		"sink must be invoked once per threshold, in ascending order")
}

// TestCheckSpendAlerts_NoMonthlyCap_NoAlerts verifies that without a monthly
// cap configured the method is a no-op, mirroring the "cap not configured"
// convention used by CheckBudget.
func TestCheckSpendAlerts_NoMonthlyCap_NoAlerts(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	// MaxInrPerMonth defaults to 0 here.
	l := ledger.New(d, defaultCfg(), defaultModels(), nil)

	got := l.CheckSpendAlerts(ctx, 1_000_000.0)
	require.Empty(t, got, "with no monthly cap configured, no alerts should fire")
}

// TestCheckSpendAlerts_ZeroSpend_NoAlerts verifies a non-positive spend never
// fires an alert even though 0 >= 0 would otherwise satisfy a 0-cap edge.
func TestCheckSpendAlerts_ZeroSpend_NoAlerts(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	require.Empty(t, l.CheckSpendAlerts(ctx, 0.0), "zero spend fires no alerts")
	require.Empty(t, l.CheckSpendAlerts(ctx, -5.0), "negative spend fires no alerts")
}

// TestSetAlertThresholds_CustomConfigurable verifies thresholds are
// configurable and that out-of-range values are ignored.
func TestSetAlertThresholds_CustomConfigurable(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	// Configure 25%/90% plus invalid values that must be dropped.
	l.SetAlertThresholds(0.90, 0.25, 0.0, -0.1, 1.5, 0.25)

	// Spend = 30 INR: only the 25% threshold crosses (not 90%).
	got := l.CheckSpendAlerts(ctx, 30.0)
	require.Equal(t, []float64{0.25}, thresholds(got),
		"custom thresholds replace the defaults; invalid and duplicate values are dropped")

	// Spend = 95 INR: 90% crosses; the default 50%/80%/100% are NOT used.
	got = l.CheckSpendAlerts(ctx, 95.0)
	require.Equal(t, []float64{0.90}, thresholds(got),
		"default thresholds must not fire once custom thresholds are set")
}

// TestSetAlertThresholds_ResetsFiredState verifies reconfiguring thresholds
// re-arms alerts, and that ResetSpendAlerts independently re-arms them.
func TestSetAlertThresholds_ResetsFiredState(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	got := l.CheckSpendAlerts(ctx, 55.0)
	require.Equal(t, []float64{0.50}, thresholds(got))

	// Reconfiguring the same thresholds re-arms them.
	l.SetAlertThresholds(ledger.DefaultAlertThresholds...)
	got = l.CheckSpendAlerts(ctx, 55.0)
	require.Equal(t, []float64{0.50}, thresholds(got), "reconfiguring thresholds re-arms the 50%% alert")

	// ResetSpendAlerts re-arms without changing thresholds.
	l.ResetSpendAlerts()
	got = l.CheckSpendAlerts(ctx, 55.0)
	require.Equal(t, []float64{0.50}, thresholds(got), "ResetSpendAlerts re-arms fired thresholds")
}

// TestCheckSpendAlerts_ConcurrentChecks_NoDataRace exercises concurrent
// callers to prove the alert state is race-safe. Run with -race.
func TestCheckSpendAlerts_ConcurrentChecks_NoDataRace(t *testing.T) {
	d := openTestDB(t)
	createSession(t, d, "sess-1")
	ctx := context.Background()

	l := ledger.New(d, monthCapCfg(100.0), defaultModels(), nil)

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fired []float64
	l.SetAlertSink(func(_ context.Context, a ledger.BudgetAlert) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, a.Threshold)
	})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker reports spend over the cap; thresholds must still
			// each fire at most once across all workers.
			l.CheckSpendAlerts(ctx, 120.0)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, fired, 3, "each of the 3 thresholds fires exactly once despite concurrent checks")
}
