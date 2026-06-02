package ledger

import (
	"context"
	"log/slog"
	"sort"
	"sync"
)

// DefaultAlertThresholds are the fractions of the monthly budget at which
// a budget alert fires by default: 50%, 80%, and 100% (over-budget). They
// are expressed as fractions in (0, 1].
var DefaultAlertThresholds = []float64{0.50, 0.80, 1.00}

// BudgetAlert describes a single monthly-budget threshold that was newly
// crossed by the current spend. It carries the numbers the TUI needs to
// render a message without recomputing them.
type BudgetAlert struct {
	// Threshold is the crossed fraction of the monthly budget, e.g. 0.80
	// for the 80% alert.
	Threshold float64 `json:"threshold"`
	// CapINR is the configured monthly budget (MaxInrPerMonth) at the time
	// of the check.
	CapINR float64 `json:"cap_inr"`
	// SpendINR is the current month-to-date spend that triggered the alert.
	SpendINR float64 `json:"spend_inr"`
}

// AlertSink is an injectable callback invoked once for each newly-crossed
// threshold, in ascending threshold order. It is optional; when nil, alerts
// are only returned from CheckSpendAlerts.
type AlertSink func(ctx context.Context, alert BudgetAlert)

// alertState tracks which monthly-budget thresholds have already fired so a
// given threshold is reported at most once within the tracking window. The
// zero value is ready to use.
type alertState struct {
	mu         sync.Mutex
	thresholds []float64 // Sorted ascending; the configured alert fractions.
	sink       AlertSink
	fired      map[float64]bool // Thresholds already reported.
}

// SetAlertThresholds configures the monthly-budget alert thresholds as
// fractions of MaxInrPerMonth (e.g. 0.50, 0.80, 1.00). Values outside the
// (0, 1] range are ignored. Thresholds are deduplicated and sorted ascending.
// Calling this resets the fired-threshold state so newly configured
// thresholds can fire on the next check.
func (l *Ledger) SetAlertThresholds(thresholds ...float64) {
	cleaned := make([]float64, 0, len(thresholds))
	seen := make(map[float64]bool, len(thresholds))
	for _, t := range thresholds {
		if t <= 0 || t > 1 || seen[t] {
			continue
		}
		seen[t] = true
		cleaned = append(cleaned, t)
	}
	sort.Float64s(cleaned)

	l.alerts.mu.Lock()
	defer l.alerts.mu.Unlock()
	l.alerts.thresholds = cleaned
	l.alerts.fired = make(map[float64]bool, len(cleaned))
}

// SetAlertSink installs (or clears, when sink is nil) the callback invoked
// for each newly-crossed monthly-budget threshold. The sink runs inside
// CheckSpendAlerts in ascending threshold order, after the fired-state is
// updated, so a sink that itself re-checks observes the threshold as fired.
func (l *Ledger) SetAlertSink(sink AlertSink) {
	l.alerts.mu.Lock()
	defer l.alerts.mu.Unlock()
	l.alerts.sink = sink
}

// configuredThresholds returns the active alert thresholds, falling back to
// DefaultAlertThresholds when none were set explicitly. Must be called with
// l.alerts.mu held.
func (l *Ledger) configuredThresholds() []float64 {
	if l.alerts.thresholds != nil {
		return l.alerts.thresholds
	}
	return DefaultAlertThresholds
}

// CheckSpendAlerts reports which monthly-budget alert thresholds are newly
// crossed by spendINR (month-to-date spend in INR) since the last check, and
// invokes the configured AlertSink once for each, in ascending threshold
// order. A threshold fires at most once: a subsequent call with the same or
// higher spend returns no alerts for thresholds already reported.
//
// A threshold T is crossed when spendINR >= T * MaxInrPerMonth. The 100%
// (T == 1.0) threshold therefore fires when spend reaches or exceeds the
// monthly cap. When several thresholds are crossed at once (for example a
// single large entry takes spend from 0 to 90% of the cap), every newly
// crossed threshold is reported in the same call.
//
// CheckSpendAlerts is a no-op (returns nil) when no monthly cap is
// configured (MaxInrPerMonth <= 0) or when spendINR is not positive.
func (l *Ledger) CheckSpendAlerts(ctx context.Context, spendINR float64) []BudgetAlert {
	capINR := l.cfg.MaxInrPerMonth
	if capINR <= 0 || spendINR <= 0 {
		return nil
	}

	l.alerts.mu.Lock()
	if l.alerts.fired == nil {
		l.alerts.fired = make(map[float64]bool)
	}
	thresholds := l.configuredThresholds()
	sink := l.alerts.sink

	var newlyCrossed []BudgetAlert
	for _, t := range thresholds {
		if l.alerts.fired[t] {
			continue
		}
		if spendINR >= t*capINR {
			l.alerts.fired[t] = true
			newlyCrossed = append(newlyCrossed, BudgetAlert{
				Threshold: t,
				CapINR:    capINR,
				SpendINR:  spendINR,
			})
		}
	}
	l.alerts.mu.Unlock()

	for _, a := range newlyCrossed {
		slog.InfoContext(
			ctx, "Budget alert threshold crossed",
			"threshold", a.Threshold,
			"spend_inr", a.SpendINR,
			"cap_inr", a.CapINR,
		)
		if sink != nil {
			sink(ctx, a)
		}
	}

	return newlyCrossed
}

// ResetSpendAlerts clears the fired-threshold state so every configured
// threshold can fire again on the next CheckSpendAlerts call. It is intended
// for window rollovers (e.g. a new calendar month) where alerts should
// re-arm.
func (l *Ledger) ResetSpendAlerts() {
	l.alerts.mu.Lock()
	defer l.alerts.mu.Unlock()
	l.alerts.fired = make(map[float64]bool, len(l.alerts.thresholds))
}
