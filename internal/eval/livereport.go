package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LiveSummary is the roll-up of a live-provider eval run: the per-task
// LiveReport lines written to a single JSONL file (see WriteLiveReport),
// aggregated into the few numbers an operator or CI gate actually checks —
// how many tasks passed, how many tokens and wall-clock time the run cost,
// and which provider/model combinations were exercised.
//
// It is computed from the persisted reports rather than tracked incrementally
// so that the same roll-up can be reproduced after the fact from an artifact
// alone, without replaying the run (which would spend real tokens).
type LiveSummary struct {
	// SchemaVersion lets future readers detect and migrate older artifacts.
	SchemaVersion int `json:"schema_version"`

	// TotalTasks is the number of reports rolled up.
	TotalTasks int `json:"total_tasks"`
	// Passed and Failed partition TotalTasks by LiveReport.Passed.
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	// PassPercent is Passed/TotalTasks*100, or 0 when no tasks ran.
	PassPercent float64 `json:"pass_percent"`
	// VerifyFailed counts tasks whose verification command did not exit zero.
	// It can exceed Failed only if a report is marked passed despite a failed
	// verify, which a healthy runner never produces; surfacing it makes such an
	// inconsistency visible instead of silently lost.
	VerifyFailed int `json:"verify_failed"`

	// TotalInputTokens and TotalOutputTokens sum the per-task token counts, so
	// the run's real spend is readable from the summary alone.
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`

	// TotalDurationMS sums the per-task durations.
	TotalDurationMS int64 `json:"total_duration_ms"`

	// Providers and Models list the distinct provider/model names exercised,
	// sorted, so a mixed run is self-describing. Empty names are skipped.
	Providers []string `json:"providers,omitempty"`
	Models    []string `json:"models,omitempty"`

	// FirstTimestamp and LastTimestamp bound the run in wall-clock time, taken
	// from the earliest and latest non-zero report timestamps.
	FirstTimestamp time.Time `json:"first_timestamp,omitempty"`
	LastTimestamp  time.Time `json:"last_timestamp,omitempty"`
}

// SummarizeLiveReports rolls a slice of per-task LiveReports up into a
// LiveSummary. It is pure and deterministic: the same input always yields the
// same output, and the result depends only on the reports (not on wall-clock
// time or map iteration order). An empty slice yields a zero-valued summary
// with TotalTasks 0, which callers can print without special-casing.
func SummarizeLiveReports(reports []LiveReport) LiveSummary {
	s := LiveSummary{
		SchemaVersion: 1,
		TotalTasks:    len(reports),
	}

	// Dedup provider/model names through sets so order of arrival never leaks
	// into the output; the slices are sorted before returning.
	providers := make(map[string]struct{})
	models := make(map[string]struct{})

	for _, r := range reports {
		if r.Passed {
			s.Passed++
		} else {
			s.Failed++
		}
		if r.VerifyCommand != "" && !r.VerifyPassed {
			s.VerifyFailed++
		}

		s.TotalInputTokens += r.InputTokens
		s.TotalOutputTokens += r.OutputTokens
		s.TotalDurationMS += r.DurationMS

		if r.Provider != "" {
			providers[r.Provider] = struct{}{}
		}
		if r.Model != "" {
			models[r.Model] = struct{}{}
		}

		if !r.Timestamp.IsZero() {
			if s.FirstTimestamp.IsZero() || r.Timestamp.Before(s.FirstTimestamp) {
				s.FirstTimestamp = r.Timestamp
			}
			if r.Timestamp.After(s.LastTimestamp) {
				s.LastTimestamp = r.Timestamp
			}
		}
	}

	if s.TotalTasks > 0 {
		s.PassPercent = float64(s.Passed) / float64(s.TotalTasks) * 100
	}
	s.Providers = sortedKeys(providers)
	s.Models = sortedKeys(models)
	return s
}

// sortedKeys returns the keys of set in ascending order, or nil when the set is
// empty (so the corresponding JSON field is omitted rather than written as []).
func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ReadLiveReports parses every JSONL line of a live-eval artifact at path back
// into LiveReports, skipping blank lines. It is the read side of
// WriteLiveReport: because reports are appended, the only faithful way to roll
// a finished (or partial) run up is to read the whole file back. A malformed
// line is reported with its 1-based number so a corrupt artifact is debuggable.
func ReadLiveReports(path string) ([]LiveReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening live eval artifact: %w", err)
	}
	defer f.Close()
	return decodeLiveReports(f)
}

// decodeLiveReports reads JSONL LiveReports from r. It is split out from
// ReadLiveReports so the parsing can be exercised against any reader.
func decodeLiveReports(r io.Reader) ([]LiveReport, error) {
	var out []LiveReport
	scan := bufio.NewScanner(r)
	// Live artifacts can carry trimmed verify output, so allow generously long
	// lines rather than failing on the scanner's default 64 KiB token cap.
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	line := 0
	for scan.Scan() {
		line++
		b := scan.Bytes()
		if len(b) == 0 {
			continue
		}
		var rep LiveReport
		if err := json.Unmarshal(b, &rep); err != nil {
			return nil, fmt.Errorf("live eval artifact line %d: %w", line, err)
		}
		out = append(out, rep)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("reading live eval artifact: %w", err)
	}
	return out, nil
}

// SummarizeLiveReportFile reads the JSONL artifact at path and rolls it up into
// a LiveSummary in one step — the common case when summarising a finished run
// from disk.
func SummarizeLiveReportFile(path string) (LiveSummary, error) {
	reports, err := ReadLiveReports(path)
	if err != nil {
		return LiveSummary{}, err
	}
	return SummarizeLiveReports(reports), nil
}

// LiveSummaryPath returns the path of the summary artifact that corresponds to
// the per-task JSONL file jsonlPath: the same name with a ".summary.json"
// suffix in place of ".jsonl", so the two sit side by side and are easy to
// pair. A path without the ".jsonl" suffix simply gains ".summary.json".
func LiveSummaryPath(jsonlPath string) string {
	base := jsonlPath
	if ext := filepath.Ext(base); ext == ".jsonl" {
		base = base[:len(base)-len(ext)]
	}
	return base + ".summary.json"
}

// WriteLiveSummary writes sum as indented JSON to the summary path that pairs
// with jsonlPath (see LiveSummaryPath), returning the path written. Unlike the
// per-task writer this truncates: a summary is a pure function of the current
// reports, so rewriting it in full is correct and keeps it consistent with the
// JSONL it derives from. The directory is created on demand.
func WriteLiveSummary(jsonlPath string, sum LiveSummary) (string, error) {
	if sum.SchemaVersion == 0 {
		sum.SchemaVersion = 1
	}
	path := LiveSummaryPath(jsonlPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating live eval summary dir: %w", err)
	}

	data, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling live summary: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing live summary: %w", err)
	}
	return path, nil
}

// WriteTo renders the summary to w in the compact, human-readable style used by
// printLiveReport, so a run can close with a one-glance roll-up. It satisfies
// io.WriterTo for convenience; the byte count is best-effort and primarily
// included to honour the interface.
func (s LiveSummary) WriteTo(w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	fmt.Fprintf(cw, "\nLive eval summary: %d/%d passed", s.Passed, s.TotalTasks)
	if s.TotalTasks > 0 {
		fmt.Fprintf(cw, " (%.0f%%)", s.PassPercent)
	}
	fmt.Fprintln(cw)
	if s.Failed > 0 {
		fmt.Fprintf(cw, "  Failed:    %d", s.Failed)
		if s.VerifyFailed > 0 {
			fmt.Fprintf(cw, " (%d failed verification)", s.VerifyFailed)
		}
		fmt.Fprintln(cw)
	}
	if s.TotalInputTokens > 0 || s.TotalOutputTokens > 0 {
		fmt.Fprintf(cw, "  Tokens:    %d in, %d out\n", s.TotalInputTokens, s.TotalOutputTokens)
	}
	fmt.Fprintf(cw, "  Duration:  %s\n", time.Duration(s.TotalDurationMS)*time.Millisecond)
	if len(s.Providers) > 0 || len(s.Models) > 0 {
		fmt.Fprintf(cw, "  Provider:  %s / %s\n",
			joinOrDash(s.Providers), joinOrDash(s.Models))
	}
	return cw.n, cw.err
}

// joinOrDash renders a sorted name set for the summary line, collapsing the
// empty case to a dash so the column is never blank.
func joinOrDash(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}

// countingWriter wraps an io.Writer to total the bytes written and latch the
// first error, so WriteTo can report a faithful count without a bytes.Buffer
// detour. Once an error is latched no further writes are attempted.
type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
	return n, err
}
