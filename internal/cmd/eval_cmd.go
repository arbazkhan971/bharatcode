package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/spf13/cobra"
)

// newEvalCmd builds the "eval" subcommand. It discovers the built-in task
// suites, runs them offline with a scripted stub provider, and reports
// per-task pass/fail, step counts, and recovery rates. Use --json for
// machine-readable output suitable for CI pipelines.
func newEvalCmd() *cobra.Command {
	var (
		jsonOut   bool
		suiteName string
		listOnly  bool
		maxSteps  int
	)

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run offline benchmark suites and report pass/fail metrics",
		Long: `Run BharatCode's built-in task-suite evaluation harness offline.

Each suite contains a set of fixture tasks (e.g. "fix Go syntax error",
"add missing function stub"). The harness spins up an agent with a scripted
stub provider — no real API keys or network access required — executes every
task, and reports per-task pass/fail, step counts, and recovery events.

Use --list to enumerate available suites without running them.
Use --suite <name> to run a specific suite (default: all suites).
Use --json to emit newline-delimited JSON for CI ingestion.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			ctx := cmd.Context()
			w := cmd.OutOrStdout()
			suites := eval.BuiltinSuites()

			if listOnly {
				return runEvalList(w, suites)
			}
			return runEvalSuites(ctx, w, suites, suiteName, jsonOut, maxSteps)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit newline-delimited JSON (one object per suite)")
	cmd.Flags().StringVar(&suiteName, "suite", "", "run a specific suite by name (default: all)")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list available suites without running them")
	cmd.Flags().IntVar(&maxSteps, "max-steps", 0, "max agent steps per task (0 = harness default 20)")
	return cmd
}

func runEvalList(w io.Writer, suites []eval.Suite) error {
	rows := make([][]string, 0, len(suites)+1)
	rows = append(rows, []string{"NAME", "TASKS", "DESCRIPTION"})
	for _, s := range suites {
		rows = append(rows, []string{s.Name, fmt.Sprintf("%d", len(s.Tasks)), s.Description})
	}
	_, err := io.WriteString(w, renderTable(rows))
	return err
}

func runEvalSuites(ctx context.Context, w io.Writer, suites []eval.Suite, filter string, jsonOut bool, maxSteps int) error {
	runner := eval.Runner{MaxSteps: maxSteps}

	var selected []eval.Suite
	for _, s := range suites {
		if filter == "" || s.Name == filter {
			selected = append(selected, s)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("no suite named %q (use --list to see available suites)", filter)
	}

	overallPass := true
	for _, suite := range selected {
		report, err := runner.RunSuite(ctx, suite)
		if err != nil {
			return fmt.Errorf("running suite %s: %w", suite.Name, err)
		}

		if jsonOut {
			b, err := json.Marshal(report)
			if err != nil {
				return fmt.Errorf("marshaling report: %w", err)
			}
			if _, err := fmt.Fprintln(w, string(b)); err != nil {
				return err
			}
		} else {
			printReport(w, report)
		}

		if report.Failed > 0 {
			overallPass = false
		}
	}

	// Exit non-zero when any task failed, so CI pipelines can detect regressions.
	if !overallPass {
		return fmt.Errorf("one or more eval tasks failed")
	}
	return nil
}

func printReport(w io.Writer, r eval.Report) {
	fmt.Fprintf(w, "\nSuite: %s\n", r.SuiteName)
	fmt.Fprintf(w, "  Passed:       %d / %d  (%.1f%%)\n", r.Passed, r.TotalTasks, r.PassPercent)
	fmt.Fprintf(w, "  Avg steps:    %.1f\n", r.AvgSteps)
	fmt.Fprintf(w, "  Recoveries:   %d\n", r.TotalRecovery)
	fmt.Fprintf(w, "  Duration:     %s\n", r.FinishedAt.Sub(r.StartedAt).Round(1e6))

	rows := make([][]string, 0, len(r.Tasks)+1)
	rows = append(rows, []string{"TASK", "PASS", "STEPS", "RECOVERIES", "REASON"})
	for _, t := range r.Tasks {
		pass := "FAIL"
		if t.Passed {
			pass = "PASS"
		}
		reason := t.Reason
		if t.Err != "" {
			reason = "ERROR: " + t.Err
		}
		if len(reason) > 50 {
			reason = reason[:47] + "..."
		}
		rows = append(rows, []string{
			t.TaskID,
			pass,
			fmt.Sprintf("%d", t.Steps),
			fmt.Sprintf("%d", t.Recoveries),
			reason,
		})
	}
	_, _ = io.WriteString(w, indent(renderTable(rows), "  "))
}

// indent prepends prefix to every non-empty line in s.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
