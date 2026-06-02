// Package ledger white-box tests cover internal helpers that are not
// reachable through the public API alone. These tests live in package
// ledger (not ledger_test) so they can call unexported functions.
package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	dbsqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
)

// openCacheTestDB opens a fresh in-memory SQLite database with one session
// row so the ledger FK constraint passes, returning the *db.DB.
func openCacheTestDB(t *testing.T, sessionID string) *db.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, t.TempDir()+"/test.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	_, err = d.Queries.CreateSession(ctx, dbsqlc.CreateSessionParams{
		ID:          sessionID,
		ProjectPath: "/tmp/project",
		Title:       "Test session",
		Model:       "m",
		Agent:       "coder",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)
	return d
}

// TestRecord_CacheReadTokens_IncludedWhenRateSet verifies that cache-read
// tokens contribute to CostUSD when the model has a cache rate, and
// contribute nothing when it does not. This is a white-box test because no
// public construction path sets a cache rate yet (config.Model has no cache
// field), so the rate is injected directly into l.pricing.
func TestRecord_CacheReadTokens_IncludedWhenRateSet(t *testing.T) {
	ctx := context.Background()
	cfg := &config.LedgerConfig{Currency: "INR", UsdInrRate: 83.50}

	// Case 1: cache rate configured — the cache term must be added.
	t.Run("with cache rate", func(t *testing.T) {
		d := openCacheTestDB(t, "sess-cache")
		l := New(d, cfg, nil, nil)
		// Inject pricing with a distinct cache rate so the cache term is
		// identifiable in the recomputed cost.
		l.pricing["p/m"] = modelPricing{
			inputPerMTok:     1.0,
			outputPerMTok:    2.0,
			cacheReadPerMTok: 0.50,
		}

		e := Entry{
			ID:              "e-cache",
			SessionID:       "sess-cache",
			Provider:        "p",
			Model:           "m",
			InputTokens:     1_000,
			OutputTokens:    500,
			CacheReadTokens: 4_000,
			At:              time.Now(),
		}
		require.NoError(t, l.Record(ctx, e))

		// 1000*1.0 + 500*2.0 + 4000*0.5 = 1000 + 1000 + 2000 = 4000 micro-USD.
		wantUSD := (1_000*1.0 + 500*2.0 + 4_000*0.50) / 1_000_000

		var gotUSD float64
		require.NoError(t, d.QueryRowContext(ctx,
			"SELECT cost_usd FROM ledger_entries WHERE id = ?", "e-cache").Scan(&gotUSD))
		require.InDelta(t, wantUSD, gotUSD, 1e-12,
			"cache-read tokens must be priced at the cache rate")
	})

	// Case 2: no cache rate — cache tokens must contribute nothing, so the
	// cost equals the input+output-only cost.
	t.Run("without cache rate", func(t *testing.T) {
		d := openCacheTestDB(t, "sess-nocache")
		l := New(d, cfg, nil, nil)
		l.pricing["p/m"] = modelPricing{
			inputPerMTok:  1.0,
			outputPerMTok: 2.0,
			// cacheReadPerMTok left zero.
		}

		e := Entry{
			ID:              "e-nocache",
			SessionID:       "sess-nocache",
			Provider:        "p",
			Model:           "m",
			InputTokens:     1_000,
			OutputTokens:    500,
			CacheReadTokens: 4_000, // priced at zero — must not affect cost.
			At:              time.Now(),
		}
		require.NoError(t, l.Record(ctx, e))

		wantUSD := (1_000*1.0 + 500*2.0) / 1_000_000

		var gotUSD float64
		require.NoError(t, d.QueryRowContext(ctx,
			"SELECT cost_usd FROM ledger_entries WHERE id = ?", "e-nocache").Scan(&gotUSD))
		require.InDelta(t, wantUSD, gotUSD, 1e-12,
			"cache-read tokens must contribute nothing when no cache rate is set")
	})
}

// TestToFloat64 covers the concrete type branches of the converter.
func TestToFloat64(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  float64
	}{
		{"float64", float64(3.14), 3.14},
		{"int64", int64(42), 42.0},
		{"default/string", "unexpected", 0.0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := toFloat64(tc.input)
			require.InDelta(t, tc.want, got, 1e-9)
		})
	}
}

// TestToInt64 covers the concrete type branches of the converter.
func TestToInt64(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  int64
	}{
		{"int64", int64(99), 99},
		{"float64", float64(7.9), 7},
		{"default/bool", true, 0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := toInt64(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestDenyReason covers every window branch of the reason formatter.
func TestDenyReason(t *testing.T) {
	tests := []struct {
		name     string
		window   Window
		cap      float64
		proj     float64
		contains string
	}{
		{"session", WindowSession, 500.0, 512.3, "session cap"},
		{"day", WindowDay, 200.0, 210.5, "daily cap"},
		{"month", WindowMonth, 1000.0, 1010.0, "monthly cap"},
		{"unknown", Window("other"), 100.0, 110.0, "cap"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reason := denyReason(tc.window, tc.cap, tc.proj)
			require.Contains(t, reason, tc.contains)
		})
	}
}

// TestRoundCents verifies the rounding behaviour.
func TestRoundCents(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0.1049, 0.10},
		{0.1050, 0.11},
		{0.0, 0.0},
		{8.345, 8.35},
	}
	for _, tc := range tests {
		tc := tc
		t.Run("", func(t *testing.T) {
			got := roundCents(tc.input)
			require.InDelta(t, tc.want, got, 1e-9)
		})
	}
}
