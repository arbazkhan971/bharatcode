package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Report is the aggregate result of running one Suite.
type Report struct {
	SuiteName  string       `json:"suite"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	Tasks      []TaskResult `json:"tasks"`

	// Aggregate fields, populated by aggregate() after all tasks complete.
	TotalTasks    int     `json:"total_tasks"`
	Passed        int     `json:"passed"`
	Failed        int     `json:"failed"`
	PassPercent   float64 `json:"pass_percent"`
	AvgSteps      float64 `json:"avg_steps"`
	TotalRecovery int     `json:"total_recoveries"`
}

// TaskResult is the outcome of a single Task.
type TaskResult struct {
	TaskID   string `json:"task"`
	TaskName string `json:"task_name"`
	Passed   bool   `json:"passed"`
	Reason   string `json:"reason,omitempty"`
	Steps    int    `json:"steps"`
	// Recoveries counts error tool results and repeated identical calls
	// observed during the run, indicating how often the agent had to recover
	// from a mistake.
	Recoveries int `json:"recoveries"`
	// Err is non-empty when the run itself failed (not just the check).
	Err string `json:"err,omitempty"`
}

// ExportedAggregate is the exported equivalent of aggregate, exposed so
// tests can verify aggregation logic without going through RunSuite.
func (r *Report) ExportedAggregate() {
	r.aggregate()
}

// aggregate fills the summary fields from the per-task results. It is called
// once after all tasks in a suite complete.
func (r *Report) aggregate() {
	r.TotalTasks = len(r.Tasks)
	totalSteps := 0
	for _, t := range r.Tasks {
		if t.Passed {
			r.Passed++
		} else {
			r.Failed++
		}
		totalSteps += t.Steps
		r.TotalRecovery += t.Recoveries
	}
	if r.TotalTasks > 0 {
		r.PassPercent = float64(r.Passed) / float64(r.TotalTasks) * 100
		r.AvgSteps = float64(totalSteps) / float64(r.TotalTasks)
	}
}

// LiveReport is the per-task result of a live-provider eval run, captured
// against the configured real provider rather than the deterministic stub.
// Unlike TaskResult it records enough provenance — provider, model, the exact
// verification command and its exit status, and the files the agent touched —
// to debug a failure without rerunning (which would cost real tokens). It is
// persisted as one JSONL line per task under .bharatcode/evals/live/.
type LiveReport struct {
	// SchemaVersion lets future readers detect and migrate older artifacts.
	SchemaVersion int       `json:"schema_version"`
	Timestamp     time.Time `json:"timestamp"`

	Provider string `json:"provider"`
	Model    string `json:"model"`
	TaskID   string `json:"task"`
	TaskName string `json:"task_name,omitempty"`

	Passed bool `json:"passed"`
	// Reason explains a failure (failed verification, agent error, etc.).
	Reason string `json:"reason,omitempty"`
	// ChangedFiles lists the files the agent created or modified, so a failed
	// run can be inspected without replaying the prompt.
	ChangedFiles []string `json:"changed_files,omitempty"`

	// VerifyCommand is the verification command that was run (e.g. "go build ./...").
	VerifyCommand string `json:"verify_command,omitempty"`
	// VerifyPassed reports whether VerifyCommand exited zero.
	VerifyPassed bool `json:"verify_passed"`
	// VerifyOutput captures the verification command's combined output, trimmed
	// to a debuggable size, so a CI failure is actionable from the artifact alone.
	VerifyOutput string `json:"verify_output,omitempty"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	DurationMS int64 `json:"duration_ms"`
}

// LiveReportDir returns the directory under projectDir where live-eval JSONL
// artifacts are written. It mirrors the .bharatcode/<area> layout used by the
// rest of the project (skills, recipes, memory).
func LiveReportDir(projectDir string) string {
	return filepath.Join(projectDir, ".bharatcode", "evals", "live")
}

// WriteLiveReport appends rep as one JSON line to a per-run JSONL file under
// LiveReportDir(projectDir). Reports from the same run land in the same file
// (keyed by the run's start day-time) so a multi-task live eval is reviewable
// as a single transcript. It returns the path written so callers can surface
// it. The directory is created on demand.
//
// Appending (rather than truncating) is deliberate: a failed task must never
// erase the artifacts of an earlier task in the same run, so partial-failure
// runs stay fully debuggable.
func WriteLiveReport(projectDir, runID string, rep LiveReport) (string, error) {
	if rep.SchemaVersion == 0 {
		rep.SchemaVersion = 1
	}
	if rep.Timestamp.IsZero() {
		rep.Timestamp = time.Now().UTC()
	}

	dir := LiveReportDir(projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating live eval dir: %w", err)
	}

	if runID == "" {
		runID = rep.Timestamp.Format("20060102-150405")
	}
	path := filepath.Join(dir, "live-"+runID+".jsonl")

	line, err := json.Marshal(rep)
	if err != nil {
		return "", fmt.Errorf("marshaling live report: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("opening live eval artifact: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("writing live eval artifact: %w", err)
	}
	return path, nil
}
