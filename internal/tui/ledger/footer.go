// Package ledger renders the INR-aware ledger footer.
package ledger

import (
	"fmt"

	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
)

// Footer caches ledger totals for rendering.
type Footer struct {
	Theme            styles.Theme
	SessionID        string
	InputTokens      int
	OutputTokens     int
	CostUSD          float64
	CostINR          float64
	MonthlyBudgetINR float64
	MonthlyUsedINR   float64
	// Subscription is true when the active model is served by a flat-rate
	// subscription provider (e.g. a ChatGPT-plan login), where every request is
	// billed to the plan rather than per token. The cost/budget segments are
	// always zero there, so they are dropped to keep the footer meaningful.
	Subscription bool
}

// ApplySummary updates token and cost totals.
func (f *Footer) ApplySummary(s rootledger.Summary) {
	if s.SessionID != "" {
		f.SessionID = s.SessionID
	}
	f.InputTokens = s.InputTokens
	f.OutputTokens = s.OutputTokens
	f.CostUSD = s.CostUSD
	f.CostINR = s.CostINR
	if f.MonthlyUsedINR == 0 {
		f.MonthlyUsedINR = s.CostINR
	}
}

// Render returns the footer line.
func (f Footer) Render(width int) string {
	pct := 0.0
	if f.MonthlyBudgetINR > 0 {
		pct = f.MonthlyUsedINR / f.MonthlyBudgetINR * 100
	}
	var line string
	if f.Subscription {
		// On a subscription plan cost is always zero, so show only the session and
		// token counts (with a clear "subscription" marker) instead of $0.00 noise.
		line = fmt.Sprintf(
			"session %s · in %d · out %d · subscription",
			shortID(f.SessionID),
			f.InputTokens,
			f.OutputTokens,
		)
	} else {
		percent := fmt.Sprintf("%.0f%% used", pct)
		switch {
		case pct >= 100:
			percent = f.Theme.Error.Render(percent)
		case pct >= 80:
			percent = f.Theme.Warn.Render(percent)
		}
		line = fmt.Sprintf(
			"session %s · in %d · out %d · cost $%.4f · ₹%.2f · budget ₹%.2f/mo (%s)",
			shortID(f.SessionID),
			f.InputTokens,
			f.OutputTokens,
			f.CostUSD,
			f.CostINR,
			f.MonthlyBudgetINR,
			percent,
		)
	}
	if len([]rune(line)) > width && width > 0 {
		line = string([]rune(line)[:width])
	}
	return f.Theme.Footer.Render(line)
}

func shortID(id string) string {
	if len(id) <= 8 {
		if id == "" {
			return "new"
		}
		return id
	}
	return id[:8]
}
