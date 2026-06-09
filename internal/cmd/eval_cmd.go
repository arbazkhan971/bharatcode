package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

// newEvalCmd builds the "eval" subcommand. It discovers the built-in task
// suites, runs them offline with a scripted stub provider, and reports
// per-task pass/fail, step counts, and recovery rates. Use --json for
// machine-readable output suitable for CI pipelines.
func newEvalCmd() *cobra.Command {
	var (
		jsonOut      bool
		suiteName    string
		listOnly     bool
		maxSteps     int
		liveProvider bool
		maxTasks     int
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
Use --json to emit newline-delimited JSON for CI ingestion.

Use --live-provider to run a small subset against the configured real
provider instead of the deterministic stub. This spends real tokens and is
gated behind the BHARATCODE_LIVE_EVAL=1 environment variable; --max-tasks
caps how many tasks run (default 1). Each live task reports its changed
files, verification command, duration, and pass/fail, and persists a
JSONL artifact under .bharatcode/evals/live/ for debugging.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if liveProvider {
				return runLiveProviderEval(cmd, maxTasks)
			}

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
	cmd.Flags().BoolVar(&liveProvider, "live-provider", false, "run a subset against the configured real provider (requires BHARATCODE_LIVE_EVAL=1)")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 0, "max tasks to run in --live-provider mode (0 = default 1)")
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

// liveEvalTasks returns the curated subset of tasks the live-provider path may
// run. They are intentionally small, self-verifying fixtures (each pairs a
// Goal+Fixture with a concrete verification command) so a single live task
// exercises the real provider end-to-end — edit, then prove the edit with a
// build/test — without a large token bill. The first task is the cheapest, so
// the default --max-tasks=1 runs it.
func liveEvalTasks() []eval.LiveTask {
	return []eval.LiveTask{
		{Task: eval.SyntaxErrorTask(), VerifyCommand: "go build ./..."},
		{Task: eval.GoBugFixTask(), VerifyCommand: "go test ./..."},
		{Task: eval.MissingFunctionTask(), VerifyCommand: "go build ./..."},
	}
}

// runLiveProviderEval drives the live-provider eval path for the eval command.
// It resolves the configured provider/model, builds a per-task isolated app
// rooted at a throwaway fixture dir, runs the real agent, verifies the result,
// and delegates the gate/cap/timeout enforcement to eval.RunLiveProviderEval.
// It deliberately does not touch the deterministic stub path.
func runLiveProviderEval(cmd *cobra.Command, maxTasks int) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	opts := getRootOptions(cmd)

	projectDir := opts.projectDir
	if projectDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectDir = cwd
		} else {
			projectDir = "."
		}
	}

	providerName, modelName := resolveLiveProviderModel(ctx, opts)

	cfg := eval.LiveEvalConfig{
		Tasks:        liveEvalTasks(),
		MaxTasks:     maxTasks,
		ProviderName: providerName,
		ModelName:    modelName,
		ProjectDir:   projectDir,
		Run:          newLiveTaskRunner(opts, providerName, modelName),
	}

	res, err := eval.RunLiveProviderEval(ctx, w, cfg)
	if err != nil {
		return err
	}

	// Exit non-zero when any live task failed so CI can detect a regression,
	// while still leaving the persisted JSONL artifacts in place to debug.
	for _, rep := range res.Reports {
		if !rep.Passed {
			return fmt.Errorf("one or more live eval tasks failed (see %s)", eval.LiveReportDir(projectDir))
		}
	}
	return nil
}

// resolveLiveProviderModel determines the provider and model names to report
// for a live run, reading the configured coder agent's model from config. It is
// best-effort: a missing config still yields sensible placeholders so the risk
// line and artifacts remain populated.
func resolveLiveProviderModel(ctx context.Context, opts *rootOptions) (provider, model string) {
	cfg, _, err := loadConfig(ctx, opts)
	if err != nil || cfg == nil {
		return "", ""
	}
	for _, a := range cfg.Agents {
		if a.Name == "coder" && a.Model != "" {
			return "", a.Model
		}
	}
	// Fall back to the first configured model, if any.
	if len(cfg.Models) > 0 {
		return cfg.Models[0].Provider, cfg.Models[0].ID
	}
	return "", ""
}

// newLiveTaskRunner returns an eval.LiveTaskRunner that runs one task against
// the real provider. Each invocation builds its own app rooted at a throwaway
// fixture directory (so the agent's file tools and verify command operate on an
// isolated copy, never the user's repo) with YOLO enabled (live eval is
// non-interactive). It records the files the agent changed, runs the task's
// verification command, captures token usage from the ledger, and returns a
// fully-populated LiveReport.
func newLiveTaskRunner(opts *rootOptions, providerName, modelName string) eval.LiveTaskRunner {
	return func(ctx context.Context, task eval.LiveTask) (eval.LiveReport, error) {
		start := time.Now()
		rep := eval.LiveReport{
			Provider:      providerName,
			Model:         modelName,
			TaskID:        task.Task.ID,
			TaskName:      task.Task.Name,
			VerifyCommand: task.VerifyCommand,
		}

		dir, err := os.MkdirTemp("", "live-eval-"+task.Task.ID+"-*")
		if err != nil {
			return rep, fmt.Errorf("creating live eval dir: %w", err)
		}
		defer os.RemoveAll(dir)

		if task.Task.Fixture != nil {
			if err := task.Task.Fixture(dir); err != nil {
				return rep, fmt.Errorf("building fixture: %w", err)
			}
		}

		// Build an isolated app rooted at the fixture dir. YOLO is forced on so
		// the non-interactive run never blocks on a permission prompt.
		liveOpts := *opts
		liveOpts.projectDir = dir
		liveOpts.yolo = true
		application, err := newApp(ctx, app.Options{
			ConfigPath: liveOpts.configPath,
			ProjectDir: dir,
			YOLO:       true,
			Verbose:    liveOpts.verbose,
			Offline:    liveOpts.offline,
			Profile:    liveOpts.profile,
		})
		if err != nil {
			return rep, fmt.Errorf("constructing live eval app: %w", err)
		}
		defer closeApp(ctx, application)

		loop, err := application.Agent.Agent("coder")
		if err != nil {
			return rep, fmt.Errorf("resolving coder agent: %w", err)
		}
		if p := loop.Provider(); p != nil && rep.Provider == "" {
			rep.Provider = p.Name()
		}

		sess := &session.Session{
			ProjectPath: dir,
			Title:       task.Task.Name,
			Model:       modelName,
			Agent:       "coder",
		}
		if err := application.Sessions.Create(ctx, sess); err != nil {
			return rep, fmt.Errorf("creating live eval session: %w", err)
		}

		before := snapshotWorkspace(dir)

		runErr := loop.Run(ctx, sess.ID, userMessage(task.Task.Goal))

		// Record changed files regardless of run error — a partial edit is itself
		// debuggable signal.
		for _, ch := range diffWorkspace(dir, before) {
			rep.ChangedFiles = append(rep.ChangedFiles, ch.path)
		}
		rep.InputTokens, rep.OutputTokens = liveSessionTokens(ctx, application.Ledger, sess.ID)

		if runErr != nil {
			rep.Reason = runErr.Error()
			rep.DurationMS = time.Since(start).Milliseconds()
			// Surface the error so the caller persists the artifact, but the
			// report carries enough state to debug without a rerun.
			return rep, nil
		}

		// Verify. An empty command means "pass when the agent completed cleanly".
		if task.VerifyCommand == "" {
			rep.Passed = true
		} else {
			out, vErr := runLiveVerify(ctx, dir, task.VerifyCommand)
			rep.VerifyOutput = trimVerifyOutput(out)
			rep.VerifyPassed = vErr == nil
			rep.Passed = vErr == nil
			if vErr != nil {
				rep.Reason = fmt.Sprintf("verification failed: %s: %v", task.VerifyCommand, vErr)
			}
		}

		rep.DurationMS = time.Since(start).Milliseconds()
		return rep, nil
	}
}

// liveSessionTokens reads the session's input/output token totals from the
// ledger. It returns zeros when no ledger is wired or the session recorded no
// calls (e.g. the run errored before the first provider turn).
func liveSessionTokens(ctx context.Context, l *ledger.Ledger, sessionID string) (in, out int) {
	if l == nil {
		return 0, 0
	}
	sum, err := l.Summary(ctx, sessionID, ledger.WindowSession)
	if err != nil {
		return 0, 0
	}
	return sum.InputTokens, sum.OutputTokens
}

// runLiveVerify runs command in dir via the shell and returns its combined
// output. It honours the caller's context so a verification command cannot
// outlive the run's wall-clock budget.
func runLiveVerify(ctx context.Context, dir, command string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// trimVerifyOutput bounds the captured verification output to a debuggable size
// so a noisy build log does not bloat the JSONL artifact. The tail is kept,
// since the failing lines are usually at the end of a build/test run.
func trimVerifyOutput(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-max:]
}
