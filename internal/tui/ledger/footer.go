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
	percent := fmt.Sprintf("%.0f%% used", pct)
	switch {
	case pct >= 100:
		percent = f.Theme.Error.Render(percent)
	case pct >= 80:
		percent = f.Theme.Warn.Render(percent)
	}
	line := fmt.Sprintf(
		"session %s · in %d · out %d · cost $%.4f · ₹%.2f · budget ₹%.2f/mo (%s)",
		shortID(f.SessionID),
		f.InputTokens,
		f.OutputTokens,
		f.CostUSD,
		f.CostINR,
		f.MonthlyBudgetINR,
		percent,
	)
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
