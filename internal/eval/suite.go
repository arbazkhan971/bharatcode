// Package eval provides an offline task-suite evaluation harness for BharatCode.
// It spins up an agent with a stub provider, executes fixture tasks, and
// collects per-task metrics (pass/fail, steps, recovery events) that aggregate
// into a report. No real provider credentials are required.
package eval

// Suite groups a set of related evaluation tasks under a common name.
type Suite struct {
	// Name is the short identifier used on the command line and in reports.
	Name string
	// Description is a human-readable summary of what the suite tests.
	Description string
	// Tasks is the ordered list of tasks that make up the suite.
	Tasks []Task
}

// BuiltinSuites returns the standard suites shipped with BharatCode.
// Each suite is self-contained and offline-testable.
func BuiltinSuites() []Suite {
	return []Suite{
		GoFixSuite(),
		CodexParitySuite(),
	}
}

// GoFixSuite is the Go code-fix benchmark suite: tasks that exercise
// common single-file editing patterns (syntax errors, missing functions,
// failing tests).
func GoFixSuite() Suite {
	return Suite{
		Name:        "go-fix",
		Description: "Fix common Go code issues: syntax errors, missing stubs, failing tests.",
		Tasks: []Task{
			SyntaxErrorTask(),
			MissingFunctionTask(),
			UpdateCommentTask(),
			AddReturnValueTask(),
			FixOffByOneTask(),
		},
	}
}
