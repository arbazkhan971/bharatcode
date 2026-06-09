package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// LiveEvalEnvVar gates live-provider evals. Running real tasks against a
// configured provider spends real tokens and money, so it is opt-in: the gate
// must be explicitly set to "1" or the run refuses to start.
const LiveEvalEnvVar = "BHARATCODE_LIVE_EVAL"

// defaultLiveMaxTasks caps how many tasks a live run executes when the caller
// does not specify a limit. Live runs cost real tokens, so the default is small.
const defaultLiveMaxTasks = 1

// defaultLiveWallClock bounds the total wall-clock time a live run may take.
// A stuck or pathologically slow provider must never hang the harness, so the
// whole run is wrapped in a context with this timeout and exits cleanly when it
// elapses.
const defaultLiveWallClock = 10 * time.Minute

// LiveTask pairs a stub-suite Task (for its Goal + Fixture) with the explicit
// verification command used to judge a live run. The deterministic Script is
// ignored on the live path — the real provider drives the tool calls — so only
// the Goal, Fixture, ID, and Name carry over.
type LiveTask struct {
	// Task supplies ID, Name, Goal, and Fixture. Its Script/Check are unused.
	Task Task
	// VerifyCommand is the shell command run in the task dir after the agent
	// finishes (e.g. "go build ./..."). An empty command skips verification and
	// the task passes whenever the agent completes without error.
	VerifyCommand string
}

// LiveTaskRunner executes one live task against the configured real provider in
// an isolated dir and returns a populated LiveReport. It is injected by the
// command layer (which owns the app/provider wiring) so this package stays free
// of app dependencies. The implementation is responsible for building the
// fixture, running the agent, running VerifyCommand, and filling provider/model,
// changed files, verification status, tokens, and duration. ctx carries the
// run-wide deadline; a well-behaved runner returns promptly when it is done.
type LiveTaskRunner func(ctx context.Context, task LiveTask) (LiveReport, error)

// LiveEvalConfig configures a live-provider eval run.
type LiveEvalConfig struct {
	// Tasks is the candidate set; only the first MaxTasks are executed.
	Tasks []LiveTask
	// MaxTasks caps how many tasks run. A zero or negative value uses
	// defaultLiveMaxTasks. The cap is applied before any task runs.
	MaxTasks int
	// WallClock bounds total run time. A zero or negative value uses
	// defaultLiveWallClock.
	WallClock time.Duration
	// ProviderName and ModelName describe the configured provider/model and are
	// printed in the estimated-risk line so the operator sees what they are
	// about to spend tokens on.
	ProviderName string
	ModelName    string
	// ProjectDir is where JSONL artifacts are persisted (under .bharatcode).
	ProjectDir string
	// Run executes a single live task. Required.
	Run LiveTaskRunner
}

// LiveEvalResult summarises a completed (or timed-out) live run.
type LiveEvalResult struct {
	Reports       []LiveReport
	ArtifactPaths []string
	// TimedOut is true when the wall-clock deadline elapsed mid-run.
	TimedOut bool
}

// runLiveProviderEval runs a small subset of tasks against the configured real
// provider. It is deliberately unexported: the only supported entry point is
// RunLiveProviderEval, which applies the env gate first. Keeping the gate and
// the body in one place makes it impossible to run live work without passing
// the guard.
func runLiveProviderEval(ctx context.Context, w io.Writer, cfg LiveEvalConfig) (LiveEvalResult, error) {
	if cfg.Run == nil {
		return LiveEvalResult{}, errors.New("live eval: no task runner configured")
	}

	maxTasks := cfg.MaxTasks
	if maxTasks <= 0 {
		maxTasks = defaultLiveMaxTasks
	}
	tasks := cfg.Tasks
	if len(tasks) > maxTasks {
		tasks = tasks[:maxTasks]
	}
	if len(tasks) == 0 {
		return LiveEvalResult{}, errors.New("live eval: no tasks to run")
	}

	wall := cfg.WallClock
	if wall <= 0 {
		wall = defaultLiveWallClock
	}

	runID := time.Now().UTC().Format("20060102-150405")
	printRiskLine(w, cfg, len(tasks), wall, runID)

	// Cap total wall time. Each task observes the same shrinking deadline, so a
	// run can never exceed the budget regardless of how the provider behaves.
	ctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	result := LiveEvalResult{}
	for i, task := range tasks {
		// Stop before starting another task once the deadline has elapsed; a
		// timed-out run exits cleanly with the artifacts gathered so far rather
		// than erroring out and discarding them.
		if ctx.Err() != nil {
			result.TimedOut = true
			fmt.Fprintf(w, "live eval: wall-clock limit (%s) reached after %d/%d task(s); stopping cleanly\n",
				wall, i, len(tasks))
			break
		}

		rep, err := cfg.Run(ctx, task)
		if err != nil {
			// A run error (provider failure, timeout) still produces a
			// debuggable artifact: record what we know and persist it.
			if rep.TaskID == "" {
				rep.TaskID = task.Task.ID
				rep.TaskName = task.Task.Name
			}
			rep.Passed = false
			if rep.Reason == "" {
				rep.Reason = err.Error()
			}
			if rep.VerifyCommand == "" {
				rep.VerifyCommand = task.VerifyCommand
			}
		}
		if rep.Provider == "" {
			rep.Provider = cfg.ProviderName
		}
		if rep.Model == "" {
			rep.Model = cfg.ModelName
		}

		result.Reports = append(result.Reports, rep)
		if path, werr := WriteLiveReport(cfg.ProjectDir, runID, rep); werr != nil {
			fmt.Fprintf(w, "live eval: warning: could not persist artifact: %v\n", werr)
		} else {
			result.ArtifactPaths = append(result.ArtifactPaths, path)
		}

		printLiveReport(w, rep)

		// Surface a context timeout that surfaced as a per-task error as a clean
		// timeout for the run as a whole.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
			fmt.Fprintf(w, "live eval: wall-clock limit (%s) reached; stopping cleanly\n", wall)
			break
		}
	}

	return result, nil
}

// RunLiveProviderEval is the gated entry point for live-provider evals. It
// refuses to run unless BHARATCODE_LIVE_EVAL=1 is set in the environment,
// returning a clear, actionable error otherwise. Once past the gate it delegates
// to runLiveProviderEval, which enforces the task cap and wall-clock budget.
func RunLiveProviderEval(ctx context.Context, w io.Writer, cfg LiveEvalConfig) (LiveEvalResult, error) {
	if os.Getenv(LiveEvalEnvVar) != "1" {
		return LiveEvalResult{}, fmt.Errorf(
			"live-provider eval is gated: set %s=1 to run real tasks against the configured provider (this spends real tokens)",
			LiveEvalEnvVar)
	}
	return runLiveProviderEval(ctx, w, cfg)
}

// printRiskLine prints an estimated-risk summary before any live work starts,
// so the operator sees exactly what is about to be spent: which provider/model,
// how many tasks, and the wall-clock cap. Token/cost estimates are intentionally
// qualitative here — the real totals are recorded per task in the JSONL artifact.
func printRiskLine(w io.Writer, cfg LiveEvalConfig, taskCount int, wall time.Duration, runID string) {
	provider := cfg.ProviderName
	if provider == "" {
		provider = "configured provider"
	}
	model := cfg.ModelName
	if model == "" {
		model = "default model"
	}
	fmt.Fprintf(w, "Live-provider eval (run %s)\n", runID)
	fmt.Fprintf(w, "  Estimated risk: %d task(s) against %s/%s — spends REAL tokens; wall-clock cap %s.\n",
		taskCount, provider, model, wall)
	fmt.Fprintf(w, "  Artifacts: %s\n", LiveReportDir(cfg.ProjectDir))
}

// printLiveReport renders one finished live task to w, mirroring the offline
// report's compact style.
func printLiveReport(w io.Writer, rep LiveReport) {
	status := "FAIL"
	if rep.Passed {
		status = "PASS"
	}
	fmt.Fprintf(w, "\n[%s] %s\n", status, rep.TaskID)
	if rep.VerifyCommand != "" {
		verify := "fail"
		if rep.VerifyPassed {
			verify = "ok"
		}
		fmt.Fprintf(w, "  Verify:   %s (%s)\n", rep.VerifyCommand, verify)
	}
	if len(rep.ChangedFiles) > 0 {
		fmt.Fprintf(w, "  Changed:  %d file(s)\n", len(rep.ChangedFiles))
		for _, f := range rep.ChangedFiles {
			fmt.Fprintf(w, "    - %s\n", f)
		}
	}
	if rep.InputTokens > 0 || rep.OutputTokens > 0 {
		fmt.Fprintf(w, "  Tokens:   %d in, %d out\n", rep.InputTokens, rep.OutputTokens)
	}
	fmt.Fprintf(w, "  Duration: %s\n", time.Duration(rep.DurationMS)*time.Millisecond)
	if rep.Reason != "" {
		fmt.Fprintf(w, "  Reason:   %s\n", rep.Reason)
	}
}
