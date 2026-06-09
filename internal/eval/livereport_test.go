package eval_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestSummarizeLiveReportsRollsUp verifies the roll-up math over a mixed run:
// pass/fail counts, total tokens and duration, and the distinct, sorted
// provider/model sets.
func TestSummarizeLiveReportsRollsUp(t *testing.T) {
	reports := []eval.LiveReport{
		{
			Provider: "deepseek", Model: "deepseek-chat",
			TaskID: "a", Passed: true, VerifyCommand: "go build ./...", VerifyPassed: true,
			InputTokens: 100, OutputTokens: 40, DurationMS: 1000,
			Timestamp: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC),
		},
		{
			Provider: "deepseek", Model: "deepseek-chat",
			TaskID: "b", Passed: false, Reason: "verify failed",
			VerifyCommand: "go build ./...", VerifyPassed: false,
			InputTokens: 200, OutputTokens: 60, DurationMS: 2500,
			Timestamp: time.Date(2026, 6, 9, 10, 5, 0, 0, time.UTC),
		},
		{
			Provider: "openrouter", Model: "kimi-k2",
			TaskID: "c", Passed: true, VerifyCommand: "go test ./...", VerifyPassed: true,
			InputTokens: 50, OutputTokens: 10, DurationMS: 500,
			Timestamp: time.Date(2026, 6, 9, 9, 55, 0, 0, time.UTC),
		},
	}

	sum := eval.SummarizeLiveReports(reports)

	require.Equal(t, 3, sum.TotalTasks)
	require.Equal(t, 2, sum.Passed)
	require.Equal(t, 1, sum.Failed)
	require.InDelta(t, 66.6667, sum.PassPercent, 0.01)
	require.Equal(t, 1, sum.VerifyFailed, "only the failed-verify task counts")

	require.Equal(t, 350, sum.TotalInputTokens)
	require.Equal(t, 110, sum.TotalOutputTokens)
	require.Equal(t, int64(4000), sum.TotalDurationMS)

	// Distinct provider/model names, sorted regardless of arrival order.
	require.Equal(t, []string{"deepseek", "openrouter"}, sum.Providers)
	require.Equal(t, []string{"deepseek-chat", "kimi-k2"}, sum.Models)

	// Time span spans the earliest and latest report timestamps, not arrival order.
	require.Equal(t, time.Date(2026, 6, 9, 9, 55, 0, 0, time.UTC), sum.FirstTimestamp)
	require.Equal(t, time.Date(2026, 6, 9, 10, 5, 0, 0, time.UTC), sum.LastTimestamp)

	require.Equal(t, 1, sum.SchemaVersion)
}

// TestSummarizeLiveReportsEmpty confirms an empty run rolls up cleanly to a
// zero-valued summary with no division by zero.
func TestSummarizeLiveReportsEmpty(t *testing.T) {
	sum := eval.SummarizeLiveReports(nil)
	require.Equal(t, 0, sum.TotalTasks)
	require.Equal(t, 0, sum.Passed)
	require.Equal(t, 0, sum.Failed)
	require.Zero(t, sum.PassPercent)
	require.Zero(t, sum.TotalDurationMS)
	require.Empty(t, sum.Providers)
	require.Empty(t, sum.Models)
}

// TestSummarizeLiveReportsDeterministic asserts the roll-up is a pure function:
// equal inputs yield byte-identical JSON, so a summary artifact is reproducible.
func TestSummarizeLiveReportsDeterministic(t *testing.T) {
	reports := []eval.LiveReport{
		{Provider: "z", Model: "m2", Passed: true, InputTokens: 1},
		{Provider: "a", Model: "m1", Passed: false, InputTokens: 2},
	}

	first, err := json.Marshal(eval.SummarizeLiveReports(reports))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		got, err := json.Marshal(eval.SummarizeLiveReports(reports))
		require.NoError(t, err)
		require.JSONEq(t, string(first), string(got), "summary must be deterministic")
	}
}

// TestReadLiveReportsRoundTrip writes per-task reports through the public
// appending writer and reads them back, exercising the append + read path end
// to end and then summarising the recovered reports.
func TestReadLiveReportsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	first := eval.LiveReport{
		Provider: "deepseek", Model: "deepseek-chat",
		TaskID: "a", Passed: true, VerifyCommand: "true", VerifyPassed: true,
		InputTokens: 10, OutputTokens: 5, DurationMS: 100,
	}
	second := eval.LiveReport{
		Provider: "deepseek", Model: "deepseek-chat",
		TaskID: "b", Passed: false, VerifyCommand: "false", VerifyPassed: false,
		InputTokens: 20, OutputTokens: 7, DurationMS: 300,
	}

	path, err := eval.WriteLiveReport(dir, "runX", first)
	require.NoError(t, err)
	path2, err := eval.WriteLiveReport(dir, "runX", second)
	require.NoError(t, err)
	require.Equal(t, path, path2, "same run shares one JSONL file")

	got, err := eval.ReadLiveReports(path)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].TaskID)
	require.Equal(t, "b", got[1].TaskID)
	require.Equal(t, 1, got[0].SchemaVersion, "writer-stamped fields survive the round trip")

	sum := eval.SummarizeLiveReports(got)
	require.Equal(t, 2, sum.TotalTasks)
	require.Equal(t, 1, sum.Passed)
	require.Equal(t, 1, sum.Failed)
	require.Equal(t, 30, sum.TotalInputTokens)
	require.Equal(t, int64(400), sum.TotalDurationMS)

	// The one-step file helper agrees with read-then-summarize.
	fileSum, err := eval.SummarizeLiveReportFile(path)
	require.NoError(t, err)
	require.Equal(t, sum, fileSum)
}

// TestReadLiveReportsMissingFile surfaces an actionable error rather than
// returning silently empty when the artifact does not exist.
func TestReadLiveReportsMissingFile(t *testing.T) {
	_, err := eval.ReadLiveReports(filepath.Join(t.TempDir(), "absent.jsonl"))
	require.Error(t, err)
}

// TestReadLiveReportsMalformedLine reports the offending 1-based line number so
// a corrupt artifact is debuggable.
func TestReadLiveReportsMalformedLine(t *testing.T) {
	dir := t.TempDir()

	good := eval.LiveReport{TaskID: "ok", Passed: true}
	path, err := eval.WriteLiveReport(dir, "bad", good)
	require.NoError(t, err)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("{not valid json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = eval.ReadLiveReports(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "line 2", "the malformed line number must be reported")
}

// TestWriteLiveSummary persists the roll-up next to its JSONL and reads it back
// to confirm the pairing and the on-disk shape.
func TestWriteLiveSummary(t *testing.T) {
	dir := t.TempDir()

	rep := eval.LiveReport{
		Provider: "deepseek", Model: "deepseek-chat",
		TaskID: "a", Passed: true, VerifyCommand: "true", VerifyPassed: true,
		InputTokens: 100, OutputTokens: 40, DurationMS: 1000,
	}
	jsonlPath, err := eval.WriteLiveReport(dir, "runS", rep)
	require.NoError(t, err)

	reports, err := eval.ReadLiveReports(jsonlPath)
	require.NoError(t, err)
	sum := eval.SummarizeLiveReports(reports)

	sumPath, err := eval.WriteLiveSummary(jsonlPath, sum)
	require.NoError(t, err)
	require.FileExists(t, sumPath)
	require.Equal(t, eval.LiveSummaryPath(jsonlPath), sumPath)
	require.Equal(t, filepath.Dir(jsonlPath), filepath.Dir(sumPath), "summary sits beside its JSONL")
	require.NotEqual(t, jsonlPath, sumPath)
	require.Equal(t, ".json", filepath.Ext(sumPath))

	data, err := os.ReadFile(sumPath)
	require.NoError(t, err)
	var back eval.LiveSummary
	require.NoError(t, json.Unmarshal(data, &back))
	require.Equal(t, sum, back, "summary round-trips through disk")
	require.Equal(t, 1, back.Passed)
	require.Equal(t, 1, back.TotalTasks)
}

// TestLiveSummaryWriteTo checks the human-readable roll-up renders the key
// numbers and never panics on the empty case.
func TestLiveSummaryWriteTo(t *testing.T) {
	sum := eval.SummarizeLiveReports([]eval.LiveReport{
		{Provider: "deepseek", Model: "deepseek-chat", Passed: true, InputTokens: 100, OutputTokens: 40, DurationMS: 1500},
		{Provider: "deepseek", Model: "deepseek-chat", Passed: false, VerifyCommand: "go build ./...", VerifyPassed: false, DurationMS: 500},
	})

	var buf bytes.Buffer
	n, err := sum.WriteTo(&buf)
	require.NoError(t, err)
	require.Equal(t, int64(buf.Len()), n, "reported byte count matches output")

	out := buf.String()
	require.Contains(t, out, "1/2 passed")
	require.Contains(t, out, "100 in, 40 out")
	require.Contains(t, out, "deepseek")
	require.Contains(t, out, "failed verification")

	// Empty summary renders without panicking.
	var empty bytes.Buffer
	_, err = eval.LiveSummary{}.WriteTo(&empty)
	require.NoError(t, err)
	require.Contains(t, empty.String(), "0/0 passed")
}
