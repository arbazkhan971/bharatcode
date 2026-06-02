// Package ledger white-box tests cover internal helpers that are not
// reachable through the public API alone. These tests live in package
// ledger (not ledger_test) so they can call unexported functions.
package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
