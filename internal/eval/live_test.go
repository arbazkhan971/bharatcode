package eval_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/stretchr/testify/require"
)

// liveTask is a tiny helper that builds a LiveTask whose embedded Task carries
// only the fields the live path uses (ID/Name/Goal/Fixture).
func liveTask(id string) eval.LiveTask {
	return eval.LiveTask{
		Task: eval.Task{
			ID:      id,
			Name:    id,
			Goal:    "do " + id,
			Fixture: func(string) error { return nil },
		},
		VerifyCommand: "true",
	}
}

// ---- gate: refuses to run unless BHARATCODE_LIVE_EVAL=1 ----

func TestRunLiveProviderEvalGated(t *testing.T) {
	// Ensure the gate is closed regardless of the host environment.
	t.Setenv(eval.LiveEvalEnvVar, "")
	require.NoError(t, os.Unsetenv(eval.LiveEvalEnvVar))

	var ran atomic.Int32
	cfg := eval.LiveEvalConfig{
		Tasks:      []eval.LiveTask{liveTask("a")},
		ProjectDir: t.TempDir(),
		Run: func(context.Context, eval.LiveTask) (eval.LiveReport, error) {
			ran.Add(1)
			return eval.LiveReport{Passed: true}, nil
		},
	}

	_, err := eval.RunLiveProviderEval(context.Background(), io.Discard, cfg)
	require.Error(t, err, "live eval must refuse to run without the gate set")
	require.Contains(t, err.Error(), eval.LiveEvalEnvVar)
	require.Equal(t, int32(0), ran.Load(), "no task may run when the gate is closed")
}

// ---- cap: only --max-tasks tasks run, never more ----

func TestRunLiveProviderEvalCapsTasks(t *testing.T) {
	t.Setenv(eval.LiveEvalEnvVar, "1")

	var ran atomic.Int32
	cfg := eval.LiveEvalConfig{
		// Three candidates, but MaxTasks=2 must stop after two.
		Tasks:      []eval.LiveTask{liveTask("a"), liveTask("b"), liveTask("c")},
		MaxTasks:   2,
		ProjectDir: t.TempDir(),
		Run: func(_ context.Context, task eval.LiveTask) (eval.LiveReport, error) {
			ran.Add(1)
			return eval.LiveReport{TaskID: task.Task.ID, Passed: true}, nil
		},
	}

	res, err := eval.RunLiveProviderEval(context.Background(), io.Discard, cfg)
	require.NoError(t, err)
	require.Equal(t, int32(2), ran.Load(), "exactly MaxTasks tasks must run")
	require.Len(t, res.Reports, 2)
}

// The zero-value cap falls back to a single task even when more are offered.
func TestRunLiveProviderEvalDefaultsToOneTask(t *testing.T) {
	t.Setenv(eval.LiveEvalEnvVar, "1")

	var ran atomic.Int32
	cfg := eval.LiveEvalConfig{
		Tasks:      []eval.LiveTask{liveTask("a"), liveTask("b")},
		ProjectDir: t.TempDir(),
		Run: func(_ context.Context, task eval.LiveTask) (eval.LiveReport, error) {
			ran.Add(1)
			return eval.LiveReport{TaskID: task.Task.ID, Passed: true}, nil
		},
	}

	_, err := eval.RunLiveProviderEval(context.Background(), io.Discard, cfg)
	require.NoError(t, err)
	require.Equal(t, int32(1), ran.Load(), "default cap must run a single task")
}

// ---- timeout: an already-elapsed budget exits cleanly without erroring ----

func TestRunLiveProviderEvalExitsCleanlyOnTimeout(t *testing.T) {
	t.Setenv(eval.LiveEvalEnvVar, "1")

	cfg := eval.LiveEvalConfig{
		Tasks:      []eval.LiveTask{liveTask("a"), liveTask("b")},
		MaxTasks:   2,
		WallClock:  time.Nanosecond, // expires before the first task starts
		ProjectDir: t.TempDir(),
		Run: func(_ context.Context, task eval.LiveTask) (eval.LiveReport, error) {
			return eval.LiveReport{TaskID: task.Task.ID, Passed: true}, nil
		},
	}

	res, err := eval.RunLiveProviderEval(context.Background(), io.Discard, cfg)
	require.NoError(t, err, "a timed-out run exits cleanly, not as an error")
	require.True(t, res.TimedOut, "result must record the timeout")
}

// ---- JSONL writer (T6): a failed live eval persists a debuggable artifact ----

func TestWriteLiveReportAppendsDebuggableJSONL(t *testing.T) {
	dir := t.TempDir()

	failed := eval.LiveReport{
		Provider:      "deepseek",
		Model:         "deepseek-chat",
		TaskID:        "syntax-fix",
		TaskName:      "Fix Go syntax error",
		Passed:        false,
		Reason:        "verification failed: go build ./...",
		ChangedFiles:  []string{"main.go"},
		VerifyCommand: "go build ./...",
		VerifyPassed:  false,
		VerifyOutput:  "main.go:4: undefined: fmt",
		InputTokens:   120,
		OutputTokens:  45,
		DurationMS:    1234,
	}

	path, err := eval.WriteLiveReport(dir, "run1", failed)
	require.NoError(t, err)
	require.FileExists(t, path)
	require.Equal(t, eval.LiveReportDir(dir), filepath.Dir(path), "artifact must land under .bharatcode/evals/live/")

	// A second report in the same run appends rather than truncates, so a
	// partial-failure run stays fully debuggable.
	passed := eval.LiveReport{TaskID: "missing-func", Passed: true, VerifyPassed: true}
	path2, err := eval.WriteLiveReport(dir, "run1", passed)
	require.NoError(t, err)
	require.Equal(t, path, path2, "same run must share one JSONL file")

	reports := readLiveJSONL(t, path)
	require.Len(t, reports, 2, "both tasks must be persisted")

	// The failed report must retain every field needed to debug without rerunning.
	first := reports[0]
	require.Equal(t, "syntax-fix", first.TaskID)
	require.False(t, first.Passed)
	require.Equal(t, "deepseek-chat", first.Model)
	require.Equal(t, []string{"main.go"}, first.ChangedFiles)
	require.Equal(t, "go build ./...", first.VerifyCommand)
	require.False(t, first.VerifyPassed)
	require.Contains(t, first.VerifyOutput, "undefined: fmt")
	require.Equal(t, 120, first.InputTokens)
	require.Equal(t, int64(1234), first.DurationMS)
	require.Equal(t, 1, first.SchemaVersion, "writer must stamp a schema version")
	require.False(t, first.Timestamp.IsZero(), "writer must stamp a timestamp")
}

// readLiveJSONL parses every line of a live-eval JSONL artifact.
func readLiveJSONL(t *testing.T, path string) []eval.LiveReport {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var out []eval.LiveReport
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var rep eval.LiveReport
		require.NoError(t, json.Unmarshal(line, &rep), "each line must be valid JSON: %s", line)
		out = append(out, rep)
	}
	require.NoError(t, scan.Err())
	return out
}
