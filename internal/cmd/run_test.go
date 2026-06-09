package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	dbsqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// TestFormatRunSummaryTokensOnly verifies that cost is omitted when CostINR is
// zero, which is the case for local / free models that carry no pricing config.
func TestFormatRunSummaryTokensOnly(t *testing.T) {
	sum := ledger.Summary{InputTokens: 1000, OutputTokens: 250, CallCount: 1}
	require.Equal(t, "Tokens: 1000 in, 250 out", formatRunSummary(sum))
}

// TestFormatRunSummaryWithIntegerCost verifies that a whole-rupee cost is
// formatted without decimal places (formatRupees rounds to integer when
// cost == float(int(cost))).
func TestFormatRunSummaryWithIntegerCost(t *testing.T) {
	sum := ledger.Summary{InputTokens: 500, OutputTokens: 100, CostINR: 2, CallCount: 1}
	require.Equal(t, "Tokens: 500 in, 100 out · Cost: ₹2", formatRunSummary(sum))
}

// TestFormatRunSummaryWithFractionalCost verifies two-decimal formatting for
// fractional rupee amounts.
func TestFormatRunSummaryWithFractionalCost(t *testing.T) {
	sum := ledger.Summary{InputTokens: 200, OutputTokens: 80, CostINR: 0.75, CallCount: 2}
	require.Equal(t, "Tokens: 200 in, 80 out · Cost: ₹0.75", formatRunSummary(sum))
}

// TestPrintRunSummaryNilLedger verifies that a nil ledger produces no output
// (printRunSummary is a no-op when no cost tracking is configured).
func TestPrintRunSummaryNilLedger(t *testing.T) {
	var buf bytes.Buffer
	printRunSummary(context.Background(), &buf, nil, "s1")
	require.Empty(t, buf.String())
}

// TestPrintRunSummaryNoData verifies that a session with no ledger entries
// produces no output (e.g. when the run errored before the first provider call).
func TestPrintRunSummaryNoData(t *testing.T) {
	l, _ := newTestLedger(t)
	var buf bytes.Buffer
	printRunSummary(context.Background(), &buf, l, "no-such-session")
	require.Empty(t, buf.String())
}

// TestPrintRunSummaryPrintsOnData verifies that a session with at least one
// recorded entry produces a one-line summary on the writer.
func TestPrintRunSummaryPrintsOnData(t *testing.T) {
	const sid = "run-summary-session"
	l, database := newTestLedger(t)
	createTestSession(t, database, sid)
	seedLedgerEntry(t, l, sid, 800, 200)

	var buf bytes.Buffer
	printRunSummary(context.Background(), &buf, l, sid)

	line := strings.TrimRight(buf.String(), "\n")
	require.Contains(t, line, "Tokens: 800 in, 200 out")
}

func TestPrintChangedFilesPrintsAbsoluteUniquePaths(t *testing.T) {
	const sid = "changed-files-session"
	_, database := newTestLedger(t)
	createTestSession(t, database, sid)
	tracker := filetracker.NewTracker(database, nil)
	path := filepath.Join(t.TempDir(), "index.html")

	_, err := tracker.RecordWrite(context.Background(), sid, path, nil, []byte("<html></html>"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(context.Background(), sid, path, []byte("<html></html>"), []byte("<!doctype html>"))
	require.NoError(t, err)

	var buf bytes.Buffer
	printChangedFiles(context.Background(), &buf, tracker, sid, nil)

	out := buf.String()
	require.Contains(t, out, "Changed files:")
	require.Contains(t, out, "- "+path)
	require.Equal(t, 1, strings.Count(out, path), "repeated writes to the same file should be listed once")
	// A file created then edited within the run is still reported as created.
	require.Contains(t, out, "- "+path+" (created)")
}

// TestPrintChangedFilesLabelsOperations verifies that each path is tagged with
// the operation label that matches its net effect on the run: a fresh write is
// "created", an in-place edit of a pre-existing file is "modified", and a
// removal is "deleted".
func TestPrintChangedFilesLabelsOperations(t *testing.T) {
	const sid = "changed-files-ops-session"
	_, database := newTestLedger(t)
	createTestSession(t, database, sid)
	tracker := filetracker.NewTracker(database, nil)
	ctx := context.Background()
	dir := t.TempDir()

	created := filepath.Join(dir, "created.txt")
	modified := filepath.Join(dir, "modified.txt")
	deleted := filepath.Join(dir, "deleted.txt")

	// created: nil old content -> create.
	_, err := tracker.RecordWrite(ctx, sid, created, nil, []byte("new"))
	require.NoError(t, err)
	// modified: non-nil old + non-nil new -> edit of a pre-existing file.
	_, err = tracker.RecordWrite(ctx, sid, modified, []byte("before"), []byte("after"))
	require.NoError(t, err)
	// deleted: non-nil old + nil new -> delete.
	_, err = tracker.RecordWrite(ctx, sid, deleted, []byte("gone"), nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	printChangedFiles(ctx, &buf, tracker, sid, nil)

	out := buf.String()
	require.Contains(t, out, "- "+created+" (created)")
	require.Contains(t, out, "- "+modified+" (modified)")
	require.Contains(t, out, "- "+deleted+" (deleted)")
}

// TestPrintChangedFilesCreateThenDeleteNetsDeleted verifies that a file created
// and later removed within the same run collapses to a single "deleted" line.
func TestPrintChangedFilesCreateThenDeleteNetsDeleted(t *testing.T) {
	const sid = "changed-files-create-delete-session"
	_, database := newTestLedger(t)
	createTestSession(t, database, sid)
	tracker := filetracker.NewTracker(database, nil)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scratch.tmp")

	_, err := tracker.RecordWrite(ctx, sid, path, nil, []byte("temp"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sid, path, []byte("temp"), nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	printChangedFiles(ctx, &buf, tracker, sid, nil)

	out := buf.String()
	require.Contains(t, out, "- "+path+" (deleted)")
	require.Equal(t, 1, strings.Count(out, path), "a created-then-deleted file should be listed once")
}

func TestDiffWorkspaceDetectsUntrackedShellCreatedFile(t *testing.T) {
	dir := t.TempDir()
	before := snapshotWorkspace(dir)
	path := filepath.Join(dir, "smoke.txt")
	require.NoError(t, os.WriteFile(path, []byte("ok"), 0o644))

	changes := diffWorkspace(dir, before)
	require.Equal(t, []fileChange{{path: path, label: "created"}}, changes)

	var buf bytes.Buffer
	printChangedFiles(context.Background(), &buf, nil, "session", changes)
	out := buf.String()
	require.Contains(t, out, "Changed files:")
	require.Contains(t, out, "- "+path+" (created)")
}

func TestRunIdentityQuestionAnswersLocally(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "run", "--quiet", "who are you?")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "BharatCode")
	require.Contains(t, stdout, "terminal-based AI coding agent")
	require.NotContains(t, stdout, "I am ChatGPT")
}

func TestRunIdentityQuestionWritesOutputLastMessage(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "last.txt")
	stdout, stderr, err := executeRoot(t, "run", "--quiet", "--output-last-message", outFile, "are you ChatGPT?")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "BharatCode")
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "BharatCode")
	require.NotContains(t, string(data), "I am ChatGPT")
}

// TestRunQuietFlagSuppressesSummary verifies that --quiet prevents any summary
// output on stderr. The fake app has a nil ledger so the summary is already a
// no-op, but this proves the flag is parsed and wired to the guard.
func TestRunQuietFlagSuppressesSummary(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 3}},
		},
	}}
	restore := installFakeApp(t, provider)
	defer restore()

	_, stderr, err := executeRoot(t, "run", "--quiet", "hello")
	require.NoError(t, err)
	require.Empty(t, stderr)
}

// newTestLedger opens a fresh in-memory SQLite database and returns a Ledger
// and the underlying *db.DB so callers can seed session rows. The ledger
// includes pricing for "stub/stub-model" so seedLedgerEntry calls succeed.
func newTestLedger(t *testing.T) (*ledger.Ledger, *db.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	database, err := db.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	cfg := &config.LedgerConfig{Currency: "INR", UsdInrRate: 83.50}
	models := []config.Model{
		{
			ID:                    "stub-model",
			Provider:              "stub",
			InputPricePerMTokUSD:  1.0,
			OutputPricePerMTokUSD: 3.0,
		},
	}
	l := ledger.New(database, cfg, models, nil)
	return l, database
}

// createTestSession inserts a minimal session row into the database so that
// subsequent ledger Record calls satisfy the foreign-key constraint.
func createTestSession(t *testing.T, database *db.DB, sessionID string) {
	t.Helper()
	_, err := database.Queries.CreateSession(context.Background(), dbsqlc.CreateSessionParams{
		ID:          sessionID,
		ProjectPath: "/tmp",
		Title:       "test session",
		Model:       "stub-model",
		Agent:       "coder",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)
}

// seedLedgerEntry records one entry for sessionID via the given Ledger.
func seedLedgerEntry(t *testing.T, l *ledger.Ledger, sessionID string, inputTokens, outputTokens int) {
	t.Helper()
	err := l.Record(context.Background(), ledger.Entry{
		SessionID:    sessionID,
		Provider:     "stub",
		Model:        "stub-model",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		At:           time.Now().UTC(),
	})
	require.NoError(t, err)
}

// newTestApp builds a minimal *app.App backed by an in-memory SQLite database.
// It is suitable for testing functions that need app.Sessions but no LLM.
func newTestApp(t *testing.T) (*app.App, *session.Repo) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "app.db")
	database, err := db.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	repo := session.NewRepo(database)
	return &app.App{DB: database, Sessions: repo}, repo
}

// TestResolveRunSession_NewSession verifies that with no flags a fresh session
// is created using the supplied agent and model names.
func TestResolveRunSession_NewSession(t *testing.T) {
	application, _ := newTestApp(t)
	s, err := resolveRunSession(context.Background(), application,
		"/proj", "", "gpt-4", "coder", "hello", false)
	require.NoError(t, err)
	require.NotEmpty(t, s.ID, "session ID must be populated")
	require.Equal(t, "/proj", s.ProjectPath)
	require.Equal(t, "gpt-4", s.Model)
	require.Equal(t, "coder", s.Agent)
}

// TestResolveRunSession_ByID verifies that --session <id> loads the named
// session and returns it unchanged.
func TestResolveRunSession_ByID(t *testing.T) {
	application, repo := newTestApp(t)
	existing := &session.Session{
		ID:          "my-session-id",
		ProjectPath: "/proj",
		Title:       "existing",
		Agent:       "planner",
		Model:       "claude-3",
	}
	require.NoError(t, repo.Create(context.Background(), existing))

	s, err := resolveRunSession(context.Background(), application,
		"/proj", "my-session-id", "", "", "follow up", false)
	require.NoError(t, err)
	require.Equal(t, "my-session-id", s.ID)
	require.Equal(t, "planner", s.Agent)
}

// TestResolveRunSession_ByID_Missing verifies that a non-existent session ID
// returns an error rather than silently creating a new session.
func TestResolveRunSession_ByID_Missing(t *testing.T) {
	application, _ := newTestApp(t)
	_, err := resolveRunSession(context.Background(), application,
		"/proj", "no-such-id", "", "", "hello", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no-such-id")
}

// TestResolveRunSession_Continue_WithExisting verifies that --continue reuses
// the most recent session for the project when one exists.
func TestResolveRunSession_Continue_WithExisting(t *testing.T) {
	application, repo := newTestApp(t)
	prior := &session.Session{
		ID:          "prior-session",
		ProjectPath: "/myproj",
		Title:       "first run",
		Agent:       "coder",
		Model:       "gpt-4",
	}
	require.NoError(t, repo.Create(context.Background(), prior))

	s, err := resolveRunSession(context.Background(), application,
		"/myproj", "", "", "", "next question", true)
	require.NoError(t, err)
	require.Equal(t, "prior-session", s.ID, "--continue must reuse the existing session")
}

// TestResolveRunSession_Continue_NoExisting verifies that --continue falls
// back to creating a new session when no session exists for the project.
func TestResolveRunSession_Continue_NoExisting(t *testing.T) {
	application, _ := newTestApp(t)
	s, err := resolveRunSession(context.Background(), application,
		"/brand-new-project", "", "model", "coder", "first prompt", true)
	require.NoError(t, err)
	require.NotEmpty(t, s.ID, "a new session must be created when none exists")
	require.Equal(t, "/brand-new-project", s.ProjectPath)
}

// TestRunContinueAndSessionMutuallyExclusive verifies that passing both
// --continue and --session together is rejected by cobra flag validation.
func TestRunContinueAndSessionMutuallyExclusive(t *testing.T) {
	provider := &scriptedProvider{}
	restore := installFakeApp(t, provider)
	defer restore()

	_, _, err := executeRoot(t, "run", "--continue", "--session", "some-id", "hi")
	require.Error(t, err, "--continue and --session together must be rejected")
}

// TestRunContinueFlagCreatesNewSessionWhenNoneExists verifies the full
// command path: --continue on a fresh database creates a new session and
// completes without error.
func TestRunContinueFlagCreatesNewSessionWhenNoneExists(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Hi there."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}},
		},
	}}
	restore := installFakeApp(t, provider)
	defer restore()

	stdout, _, err := executeRoot(t, "run", "--quiet", "--continue", "hello")
	require.NoError(t, err)
	require.Contains(t, stdout, "Hi there.")
}
