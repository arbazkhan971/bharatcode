package eval

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
)

// Task describes one evaluation scenario: a fixture repo the runner sets up,
// the agent prompt that drives the task, and a checker that decides pass/fail.
type Task struct {
	// ID is the stable short identifier used in reports (e.g. "syntax-fix").
	ID string
	// Name is a human-readable task title.
	Name string
	// Goal is the user prompt sent to the agent.
	Goal string
	// Fixture builds the initial repo state for this task in a temp directory.
	Fixture FixtureBuilder
	// Script is the scripted provider turn sequence the stub provider replays.
	// Each inner slice is one provider turn: a sequence of llm.Events that the
	// stub streams back before closing the channel.
	Script [][]llm.Event
	// Check decides whether the task passed given the outcome. When nil, the
	// task passes whenever the agent run completes without error.
	Check CheckFn
}

// FixtureBuilder writes the initial repo state into dir.
type FixtureBuilder func(dir string) error

// CheckFn inspects the post-run repo state and returns (passed, reason).
type CheckFn func(dir string, outcome Outcome) (passed bool, reason string)

// Outcome carries the observable results of one agent run.
type Outcome struct {
	// ToolCalls is the ordered list of tool calls the agent made.
	ToolCalls []ToolCall
	// FinalText is the last assistant text message the agent produced.
	FinalText string
	// Err is non-nil when the agent run itself returned an error.
	Err error
}

// ToolCall records one tool invocation observed during the run.
type ToolCall struct {
	Name  string
	Input json.RawMessage
}

// -------- built-in tasks --------

// SyntaxErrorTask asks the agent to fix a Go file with a missing import.
func SyntaxErrorTask() Task {
	return Task{
		ID:      "syntax-fix",
		Name:    "Fix Go syntax error (missing import)",
		Goal:    "The file main.go has a syntax error — fix it so it compiles.",
		Fixture: fixtureSyntaxError,
		Script: [][]llm.Event{
			// Turn 1: agent reads the file.
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: agent edits the file.
			{
				toolUseEnd("call-2", "edit", `{"path":"main.go","old_string":"","new_string":"import \"fmt\"\n"}`),
				endEvent(12, 6),
			},
			// Turn 3: agent reports success.
			{
				deltaText("The syntax error is fixed; main.go now imports fmt correctly."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			hasView := containsToolCall(out.ToolCalls, "view")
			hasEdit := containsToolCall(out.ToolCalls, "edit")
			if !hasView {
				return false, "agent never called the view tool"
			}
			if !hasEdit {
				return false, "agent never called the edit tool"
			}
			return true, "agent read then edited the file"
		},
	}
}

// MissingFunctionTask asks the agent to add a missing stub function.
func MissingFunctionTask() Task {
	return Task{
		ID:      "missing-func",
		Name:    "Add missing function stub",
		Goal:    "The file util.go calls helper() but the function is not defined — add a stub.",
		Fixture: fixtureMissingFunc,
		Script: [][]llm.Event{
			// Turn 1: agent reads util.go.
			{
				toolUseEnd("call-1", "view", `{"path":"util.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: agent writes the stub.
			{
				toolUseEnd("call-2", "write", `{"path":"helper.go","content":"package main\n\nfunc helper() {}\n"}`),
				endEvent(12, 6),
			},
			// Turn 3: agent confirms.
			{
				deltaText("Added helper() stub in helper.go."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "write") && !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent made no file write/edit"
			}
			return true, "agent wrote or edited a file"
		},
	}
}

// UpdateCommentTask asks the agent to update a stale comment.
func UpdateCommentTask() Task {
	return Task{
		ID:      "update-comment",
		Name:    "Update stale comment",
		Goal:    "The comment in README.go is outdated — update it to match the new function signature.",
		Fixture: fixtureUpdateComment,
		Script: [][]llm.Event{
			// Turn 1: read the file.
			{
				toolUseEnd("call-1", "view", `{"path":"README.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: edit the comment.
			{
				toolUseEnd("call-2", "edit", `{"path":"README.go","old_string":"// oldComment","new_string":"// newComment"}`),
				endEvent(12, 6),
			},
			// Turn 3: done.
			{
				deltaText("Comment updated."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called the edit tool"
			}
			return true, "agent called edit"
		},
	}
}

// AddReturnValueTask asks the agent to add a return value to a function.
func AddReturnValueTask() Task {
	return Task{
		ID:      "add-return",
		Name:    "Add return value to function",
		Goal:    "calc.go has a function that should return an int but currently returns nothing — fix it.",
		Fixture: fixtureAddReturn,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"calc.go"}`),
				endEvent(10, 5),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"calc.go","old_string":"func add(a, b int) {","new_string":"func add(a, b int) int {"}`),
				endEvent(12, 6),
			},
			{
				deltaText("Return type added to add()."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called edit"
			}
			return true, "agent edited the file"
		},
	}
}

// FixOffByOneTask asks the agent to correct an off-by-one index bug.
func FixOffByOneTask() Task {
	return Task{
		ID:      "off-by-one",
		Name:    "Fix off-by-one index error",
		Goal:    "slice.go accesses index n instead of n-1 — fix the off-by-one bug.",
		Fixture: fixtureOffByOne,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"slice.go"}`),
				endEvent(10, 5),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"slice.go","old_string":"s[n]","new_string":"s[n-1]"}`),
				endEvent(12, 6),
			},
			{
				deltaText("Off-by-one fixed."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called edit"
			}
			return true, "agent edited the file"
		},
	}
}

// -------- codex-parity suite --------
//
// The codex-parity suite tracks how close the agent feels to Codex CLI on
// recurring, end-to-end app-building and bug-fixing tasks. Unlike the go-fix
// tasks, each parity task is scripted to follow the Codex shape: read context,
// make the edit(s), then run a verification command (build/test) before
// claiming success. Per-task we capture pass/fail, the files the agent touched,
// whether it verified its work, scripted token usage, and elapsed wall time.

// CodexParitySuite returns the recurring end-to-end parity benchmark: small
// app builds (todo, calculator, notes, quiz) plus targeted bug fixes (Go, Node)
// and a frontend build that must be verified.
func CodexParitySuite() Suite {
	return Suite{
		Name:        "codex-parity",
		Description: "Recurring Codex-parity tasks: build small apps and fix bugs, each verified before claiming done.",
		Tasks: []Task{
			TodoAppTask(),
			CalculatorTask(),
			NotesAppTask(),
			QuizAppTask(),
			GoBugFixTask(),
			NodeTestFixTask(),
			FrontendBuildTask(),
		},
	}
}

// verifiedBuildCheck passes when the agent both edited at least one file and ran
// a verification command (build/test) — the Codex-parity bar of "don't claim
// done without verifying".
func verifiedBuildCheck(_ string, out Outcome) (bool, string) {
	if !wroteAnyFile(out.ToolCalls) {
		return false, "agent produced no file edits"
	}
	if !ranVerification(out.ToolCalls) {
		return false, "agent never verified its work (no build/test run)"
	}
	return true, "agent built then verified its work"
}

// TodoAppTask asks the agent to build a small todo app and verify it builds.
func TodoAppTask() Task {
	return Task{
		ID:      "todo-app",
		Name:    "Build a todo CLI app",
		Goal:    "Build a small todo CLI app in main.go (add/list/done), then make sure it builds.",
		Fixture: fixtureTodoApp,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(40, 8),
			},
			{
				toolUseEnd("call-2", "write", `{"path":"main.go","content":"package main\n\nfunc main() { /* todo app */ }\n"}`),
				endEvent(60, 120),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"go build ./..."}`),
				endEvent(20, 10),
			},
			{
				deltaText("Built the todo app; go build passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// CalculatorTask asks the agent to build a calculator and verify it builds.
func CalculatorTask() Task {
	return Task{
		ID:      "calculator",
		Name:    "Build a calculator",
		Goal:    "Build a calculator in main.go supporting + - * /, then make sure it builds.",
		Fixture: fixtureCalculator,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(40, 8),
			},
			{
				toolUseEnd("call-2", "write", `{"path":"main.go","content":"package main\n\nfunc main() { /* calculator */ }\n"}`),
				endEvent(55, 110),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"go build ./..."}`),
				endEvent(20, 10),
			},
			{
				deltaText("Calculator implemented; go build passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// NotesAppTask asks the agent to build a notes app and verify it builds.
func NotesAppTask() Task {
	return Task{
		ID:      "notes-app",
		Name:    "Build a notes app",
		Goal:    "Build a notes app in main.go (create/list/delete notes), then make sure it builds.",
		Fixture: fixtureNotesApp,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(40, 8),
			},
			{
				toolUseEnd("call-2", "write", `{"path":"main.go","content":"package main\n\nfunc main() { /* notes app */ }\n"}`),
				endEvent(58, 115),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"go build ./..."}`),
				endEvent(20, 10),
			},
			{
				deltaText("Notes app implemented; go build passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// QuizAppTask asks the agent to build a quiz app and verify it builds.
func QuizAppTask() Task {
	return Task{
		ID:      "quiz-app",
		Name:    "Build a quiz app",
		Goal:    "Build a quiz app in main.go (ask questions, score answers), then make sure it builds.",
		Fixture: fixtureQuizApp,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(40, 8),
			},
			{
				toolUseEnd("call-2", "write", `{"path":"main.go","content":"package main\n\nfunc main() { /* quiz app */ }\n"}`),
				endEvent(57, 118),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"go build ./..."}`),
				endEvent(20, 10),
			},
			{
				deltaText("Quiz app implemented; go build passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// GoBugFixTask asks the agent to fix a failing Go helper and verify with tests.
func GoBugFixTask() Task {
	return Task{
		ID:      "go-bug-fix",
		Name:    "Fix a Go bug and run tests",
		Goal:    "sum() in sum.go returns the wrong result and TestSum fails — fix it and run the tests.",
		Fixture: fixtureGoBug,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"sum.go"}`),
				endEvent(45, 8),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"sum.go","old_string":"return a - b","new_string":"return a + b"}`),
				endEvent(35, 20),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"go test ./..."}`),
				endEvent(22, 10),
			},
			{
				deltaText("Fixed the operator in sum(); go test passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// NodeTestFixTask asks the agent to fix a failing Node test and verify it.
func NodeTestFixTask() Task {
	return Task{
		ID:      "node-test-fix",
		Name:    "Fix a Node test failure",
		Goal:    "The Node test for sum() fails — fix sum.js and run npm test to confirm.",
		Fixture: fixtureNodeBug,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"sum.js"}`),
				endEvent(42, 8),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"sum.js","old_string":"return a - b;","new_string":"return a + b;"}`),
				endEvent(33, 18),
			},
			{
				toolUseEnd("call-3", "bash", `{"command":"npm test"}`),
				endEvent(24, 10),
			},
			{
				deltaText("Fixed sum.js; npm test passes."),
				endEvent(15, 12),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// FrontendBuildTask asks the agent to fix a broken frontend build and verify it
// by running the build command.
func FrontendBuildTask() Task {
	return Task{
		ID:      "frontend-build",
		Name:    "Fix and verify a frontend build",
		Goal:    "src/main.js imports a missing module and the build fails — fix it and run npm run build.",
		Fixture: fixtureFrontendBuild,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"src/main.js"}`),
				endEvent(48, 8),
			},
			{
				toolUseEnd("call-2", "write", `{"path":"src/greeting.js","content":"export const greeting = \"hi\";\n"}`),
				endEvent(40, 30),
			},
			{
				toolUseEnd("call-3", "edit", `{"path":"src/main.js","old_string":"./missing","new_string":"./greeting"}`),
				endEvent(30, 18),
			},
			{
				toolUseEnd("call-4", "bash", `{"command":"npm run build"}`),
				endEvent(25, 12),
			},
			{
				deltaText("Added greeting.js and fixed the import; npm run build passes."),
				endEvent(15, 14),
			},
		},
		Check: verifiedBuildCheck,
	}
}

// -------- codex-parity report --------

// ParityMetrics captures the per-task Codex-parity signal: success/fail, the
// files the agent changed, whether it verified its work, scripted token usage,
// and elapsed wall time. It is derived from the task script and run outcome so
// the signal is fully deterministic and reproducible offline.
type ParityMetrics struct {
	TaskID       string        `json:"task"`
	TaskName     string        `json:"task_name"`
	Passed       bool          `json:"passed"`
	Reason       string        `json:"reason,omitempty"`
	ChangedFiles []string      `json:"changed_files"`
	Verified     bool          `json:"verified"`
	Verification string        `json:"verification,omitempty"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	TotalTokens  int           `json:"total_tokens"`
	Steps        int           `json:"steps"`
	Elapsed      time.Duration `json:"elapsed_ns"`
	Err          string        `json:"err,omitempty"`
}

// ParityReport is the aggregate Codex-parity quality signal for the suite.
type ParityReport struct {
	SuiteName    string          `json:"suite"`
	Tasks        []ParityMetrics `json:"tasks"`
	TotalTasks   int             `json:"total_tasks"`
	Passed       int             `json:"passed"`
	Failed       int             `json:"failed"`
	PassPercent  float64         `json:"pass_percent"`
	Verified     int             `json:"verified"`
	TotalTokens  int             `json:"total_tokens"`
	TotalElapsed time.Duration   `json:"total_elapsed_ns"`
}

// RunCodexParity runs the codex-parity suite and returns the per-task parity
// metrics alongside the underlying suite Report. It is the callable entry point
// for the codex-parity signal today (no CLI command emits it yet — see
// docs/evals/codex-parity.md): it yields a stable quality signal whose report
// lists task, success, changed files, verification, tokens, and elapsed.
func (r Runner) RunCodexParity(ctx context.Context) (ParityReport, error) {
	suite := CodexParitySuite()
	report := ParityReport{SuiteName: suite.Name}
	for _, task := range suite.Tasks {
		started := time.Now()
		res := r.RunTask(ctx, task)
		elapsed := time.Since(started)

		in, out := scriptTokens(task.Script)
		m := ParityMetrics{
			TaskID:       res.TaskID,
			TaskName:     res.TaskName,
			Passed:       res.Passed,
			Reason:       res.Reason,
			ChangedFiles: changedFiles(task.Script),
			Verification: verificationCommand(task.Script),
			InputTokens:  in,
			OutputTokens: out,
			TotalTokens:  in + out,
			Steps:        res.Steps,
			Elapsed:      elapsed,
			Err:          res.Err,
		}
		m.Verified = m.Verification != ""
		report.Tasks = append(report.Tasks, m)
	}
	report.aggregateParity()
	return report, nil
}

// aggregateParity fills the ParityReport summary fields from its task metrics.
func (r *ParityReport) aggregateParity() {
	r.TotalTasks = len(r.Tasks)
	for _, t := range r.Tasks {
		if t.Passed {
			r.Passed++
		} else {
			r.Failed++
		}
		if t.Verified {
			r.Verified++
		}
		r.TotalTokens += t.TotalTokens
		r.TotalElapsed += t.Elapsed
	}
	if r.TotalTasks > 0 {
		r.PassPercent = float64(r.Passed) / float64(r.TotalTasks) * 100
	}
}

// -------- helpers --------

// fileWritingTools are the tools whose input carries a "path" the agent edited.
var fileWritingTools = map[string]bool{
	"edit": true, "write": true, "multiedit": true,
}

// wroteAnyFile reports whether any tool call edited or wrote a file.
func wroteAnyFile(calls []ToolCall) bool {
	for _, c := range calls {
		if fileWritingTools[c.Name] {
			return true
		}
	}
	return false
}

// ranVerification reports whether the agent ran a build/test verification via
// the bash tool.
func ranVerification(calls []ToolCall) bool {
	for _, c := range calls {
		if c.Name != "bash" {
			continue
		}
		if isVerificationCommand(bashCommand(c.Input)) {
			return true
		}
	}
	return false
}

// changedFiles extracts, in order and de-duplicated, the file paths the scripted
// run wrote or edited.
func changedFiles(script [][]llm.Event) []string {
	var files []string
	seen := map[string]bool{}
	for _, turn := range script {
		for _, ev := range turn {
			tce, ok := ev.(llm.ToolUseEndEvent)
			if !ok || !fileWritingTools[tce.Name] {
				continue
			}
			var args struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(tce.Input, &args) != nil || args.Path == "" {
				continue
			}
			if !seen[args.Path] {
				seen[args.Path] = true
				files = append(files, args.Path)
			}
		}
	}
	return files
}

// verificationCommand returns the first build/test command the scripted run ran,
// or "" when the run did not verify its work.
func verificationCommand(script [][]llm.Event) string {
	for _, turn := range script {
		for _, ev := range turn {
			tce, ok := ev.(llm.ToolUseEndEvent)
			if !ok || tce.Name != "bash" {
				continue
			}
			if cmd := bashCommand(tce.Input); isVerificationCommand(cmd) {
				return cmd
			}
		}
	}
	return ""
}

// scriptTokens sums the input and output tokens reported by the script's
// EndEvents.
func scriptTokens(script [][]llm.Event) (in, out int) {
	for _, turn := range script {
		for _, ev := range turn {
			if ee, ok := ev.(llm.EndEvent); ok {
				in += ee.Usage.InputTokens
				out += ee.Usage.OutputTokens
			}
		}
	}
	return in, out
}

// bashCommand pulls the "command" field out of a bash tool input.
func bashCommand(input json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	return args.Command
}

// isVerificationCommand reports whether cmd looks like a build or test run.
func isVerificationCommand(cmd string) bool {
	for _, marker := range []string{"go build", "go test", "go vet", "npm test", "npm run build", "yarn build", "pnpm build", "make test"} {
		if strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

func containsToolCall(calls []ToolCall, name string) bool {
	for _, c := range calls {
		if c.Name == name {
			return true
		}
	}
	return false
}

func toolUseEnd(id, name, input string) llm.Event {
	return llm.ToolUseEndEvent{ID: id, Name: name, Input: json.RawMessage(input)}
}

func deltaText(text string) llm.Event {
	return llm.DeltaTextEvent{Text: text}
}

func endEvent(in, out int) llm.Event {
	return llm.EndEvent{Usage: llm.Usage{InputTokens: in, OutputTokens: out}}
}
