package eval

import "time"

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
