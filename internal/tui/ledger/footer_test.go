package ledger

import (
	"strings"
	"testing"

	rootledger "github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

func TestFooterUpdateAndBudgetStyling(t *testing.T) {
	t.Parallel()

	footer := Footer{Theme: styles.Default(), MonthlyBudgetINR: 100}
	footer.ApplySummary(rootledger.Summary{
		SessionID:    "abcdef123456",
		InputTokens:  120,
		OutputTokens: 80,
		CostUSD:      0.0101,
		CostINR:      80,
	})
	footer.MonthlyUsedINR = 80

	warn := footer.Render(240)
	require.Contains(t, warn, "in 120")
	require.Contains(t, warn, "out 80")
	require.Contains(t, warn, "80% used")
	require.True(t, strings.Contains(warn, "\x1b["))

	footer.MonthlyUsedINR = 100
	errOut := footer.Render(240)
	require.Contains(t, errOut, "100% used")
	require.True(t, strings.Contains(errOut, "\x1b["))
}
