package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/stretchr/testify/require"
)

// ---- suite / task structure tests ----

func TestGoFixSuiteHasFiveTasks(t *testing.T) {
	suite := eval.GoFixSuite()
	require.Equal(t, "go-fix", suite.Name)
	require.Len(t, suite.Tasks, 5)
}

func TestBuiltinSuitesReturnsAtLeastOne(t *testing.T) {
	suites := eval.BuiltinSuites()
	require.NotEmpty(t, suites)
}

func TestTaskIDsAreUnique(t *testing.T) {
	suite := eval.GoFixSuite()
	seen := map[string]bool{}
	for _, task := range suite.Tasks {
		require.False(t, seen[task.ID], "duplicate task ID: %s", task.ID)
		seen[task.ID] = true
	}
}

func TestTasksHaveGoalsAndScripts(t *testing.T) {
	suite := eval.GoFixSuite()
	for _, task := range suite.Tasks {
		require.NotEmpty(t, task.ID, "task missing ID")
		require.NotEmpty(t, task.Goal, "task %s missing Goal", task.ID)
		require.NotEmpty(t, task.Script, "task %s has empty script", task.ID)
	}
}

// ---- fixture tests ----

func TestFixtureBuildersSeedFiles(t *testing.T) {
	suite := eval.GoFixSuite()
	for _, task := range suite.Tasks {
		task := task
		t.Run(task.ID, func(t *testing.T) {
			dir := t.TempDir()
			require.NotNil(t, task.Fixture, "task %s must have a Fixture", task.ID)
			require.NoError(t, task.Fixture(dir), "fixture for %s must succeed", task.ID)
		})
	}
}

// ---- report aggregation ----

func TestReportAggregatesPassFail(t *testing.T) {
	r := eval.Report{
		SuiteName: "test",
		Tasks: []eval.TaskResult{
			{TaskID: "a", Passed: true, Steps: 3},
			{TaskID: "b", Passed: false, Steps: 5, Recoveries: 1},
			{TaskID: "c", Passed: true, Steps: 4},
		},
	}
	// Call unexported aggregate through an exported method — use RunSuite
	// with an empty suite so the report is valid, but test aggregate logic
	// by constructing a report directly and marshaling.
	// We expose aggregate via a thin exported shim only for testing here.
	// Instead, validate via RunSuite which calls aggregate() internally.
	// So we just call it on a real Report returned from RunSuite.
	// But because eval.Report.aggregate is unexported we use a minimal suite.
	r.ExportedAggregate()
	require.Equal(t, 3, r.TotalTasks)
	require.Equal(t, 2, r.Passed)
	require.Equal(t, 1, r.Failed)
	require.InDelta(t, 66.666, r.PassPercent, 0.1)
	require.InDelta(t, 4.0, r.AvgSteps, 0.01)
	require.Equal(t, 1, r.TotalRecovery)
}

// ---- runner integration: each built-in task runs offline ----

func TestRunnerRunsSyntaxFixTask(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	task := eval.SyntaxErrorTask()
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "syntax-fix task must pass; reason: %s", res.Reason)
	require.Empty(t, res.Err, "task must not error")
	require.Greater(t, res.Steps, 0, "at least one provider turn must have occurred")
}

func TestRunnerRunsMissingFuncTask(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	task := eval.MissingFunctionTask()
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "missing-func task must pass; reason: %s", res.Reason)
	require.Empty(t, res.Err)
}

func TestRunnerRunsUpdateCommentTask(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	task := eval.UpdateCommentTask()
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "update-comment task must pass; reason: %s", res.Reason)
	require.Empty(t, res.Err)
}

func TestRunnerRunsAddReturnTask(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	task := eval.AddReturnValueTask()
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "add-return task must pass; reason: %s", res.Reason)
	require.Empty(t, res.Err)
}

func TestRunnerRunsOffByOneTask(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	task := eval.FixOffByOneTask()
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "off-by-one task must pass; reason: %s", res.Reason)
	require.Empty(t, res.Err)
}

// ---- full suite run ----

func TestRunnerRunsGoFixSuite(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 10}
	suite := eval.GoFixSuite()
	report, err := runner.RunSuite(ctx, suite)
	require.NoError(t, err)
	require.Equal(t, "go-fix", report.SuiteName)
	require.Equal(t, 5, report.TotalTasks)
	// All built-in scripted tasks must pass offline.
	require.Equal(t, 5, report.Passed, "all 5 tasks must pass; report: %+v", report.Tasks)
	require.Equal(t, 0, report.Failed)
	require.Greater(t, report.AvgSteps, 0.0)
	// Verify JSON serialisability for --json output.
	b, err := json.Marshal(report)
	require.NoError(t, err)
	require.Contains(t, string(b), `"suite":"go-fix"`)
}

// ---- custom task: agent fails check, task marked failed ----

func TestRunnerMarksTaskFailedWhenCheckFails(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 5}
	// A task whose check always returns false.
	task := eval.Task{
		ID:      "always-fail",
		Name:    "Always failing check",
		Goal:    "do something",
		Fixture: func(dir string) error { return nil },
		Script: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "done"}, llm.EndEvent{}},
		},
		Check: func(dir string, out eval.Outcome) (bool, string) {
			return false, "intentional failure"
		},
	}
	res := runner.RunTask(ctx, task)
	require.False(t, res.Passed)
	require.Equal(t, "intentional failure", res.Reason)
}

// ---- custom task: no script produces default-pass ----

func TestRunnerDefaultPassWhenNoCheck(t *testing.T) {
	ctx := context.Background()
	runner := eval.Runner{MaxSteps: 5}
	task := eval.Task{
		ID:      "no-check",
		Name:    "No explicit check",
		Goal:    "just run",
		Fixture: func(dir string) error { return nil },
		Script: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "ok"}, llm.EndEvent{}},
		},
		// Check is nil — runner defaults to pass-if-no-error.
	}
	res := runner.RunTask(ctx, task)
	require.True(t, res.Passed, "task with no Check must pass when run succeeds")
	require.Empty(t, res.Err)
}
