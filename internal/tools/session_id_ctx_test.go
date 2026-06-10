package tools

// Tests that the session-ID context plumbing introduced to fix the production
// bug (registry built once with empty deps.SessionID) works end-to-end: when
// deps.SessionID is empty but the run context carries a session id via
// WithSessionID, tools use that id for file-tracking and permission checks.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/stretchr/testify/require"
)

// newCtxSessionTracker creates a real filetracker backed by an in-memory
// sqlite database with the given session pre-created, mirroring
// newToolsTestTracker in view_test.go.
func newCtxSessionTracker(t *testing.T, sessionID string) *filetracker.Tracker {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "ctx_sess.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Queries.CreateSession(context.Background(), sqlc.CreateSessionParams{
		ID:          sessionID,
		ProjectPath: t.TempDir(),
		Title:       "ctx-session test",
		Model:       "test-model",
		Agent:       "test-agent",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)
	return filetracker.NewTracker(database, nil)
}

// TestViewRecordsReadUnderContextSessionID verifies that a ViewTool built with
// empty deps.SessionID records the read under the session id carried on the
// run context via WithSessionID, not under the empty string.
func TestViewRecordsReadUnderContextSessionID(t *testing.T) {
	const ctxSess = "ctx-view-sess"

	workDir := t.TempDir()
	path := filepath.Join(workDir, "target.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))

	tracker := newCtxSessionTracker(t, ctxSess)

	// Build the tool with an EMPTY SessionID — this is the production scenario.
	tool := newViewTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "", // intentionally empty, as in production
	})

	ctx := WithSessionID(context.Background(), ctxSess)
	result, err := tool.Run(ctx, json.RawMessage(`{"path":"target.go"}`))
	require.NoError(t, err)
	require.False(t, result.IsError, "view should succeed: %s", result.Content)

	// The read must be recorded under ctxSess, not under "".
	read, err := tracker.HasRead(ctx, ctxSess, path)
	require.NoError(t, err)
	require.True(t, read, "read should be recorded under the context session id")

	// No read should be recorded under the empty string.
	read0, err := tracker.HasRead(context.Background(), "", path)
	require.NoError(t, err)
	require.False(t, read0, "nothing should be recorded under the empty session id")
}

// TestEditPassesReadBeforeEditViaContextSessionID verifies that:
//  1. A ViewTool with empty deps.SessionID records the read under the ctx session.
//  2. An EditTool with empty deps.SessionID resolves the same ctx session and the
//     read-before-edit guard passes (the view recorded under the same session).
//  3. The write is recorded under the ctx session id.
func TestEditPassesReadBeforeEditViaContextSessionID(t *testing.T) {
	const ctxSess = "ctx-edit-sess"

	workDir := t.TempDir()
	path := filepath.Join(workDir, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello BharatCode\n"), 0o644))

	tracker := newCtxSessionTracker(t, ctxSess)

	// Both tools built with empty SessionID — production scenario.
	viewTool := newViewTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "",
	})
	editTool := newEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "",
	})

	ctx := WithSessionID(context.Background(), ctxSess)

	// Step 1: view the file to satisfy the read-before-edit guard.
	viewRes, err := viewTool.Run(ctx, json.RawMessage(`{"path":"hello.txt"}`))
	require.NoError(t, err)
	require.False(t, viewRes.IsError, "view failed: %s", viewRes.Content)

	// Step 2: edit the file — the guard should pass because the view was
	// recorded under the same ctx session.
	editRes, err := editTool.Run(ctx, json.RawMessage(`{
		"path":       "hello.txt",
		"old_string": "hello",
		"new_string": "namaste"
	}`))
	require.NoError(t, err)
	require.False(t, editRes.IsError,
		"edit should pass read-before-edit guard; got error: %s", editRes.Content)

	// The file must have been updated on disk.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "namaste BharatCode\n", string(got))

	// The write must be recorded under the ctx session.
	changes, err := tracker.ChangesForSession(ctx, ctxSess)
	require.NoError(t, err)
	require.NotEmpty(t, changes, "write should be recorded under the context session id")
}

// TestEditRejectsBlindEditWhenNoViewUnderContextSession verifies that the
// read-before-edit guard fires when the file has not been viewed under the ctx
// session — even though deps.SessionID is empty.
func TestEditRejectsBlindEditWhenNoViewUnderContextSession(t *testing.T) {
	const ctxSess = "ctx-blind-sess"

	workDir := t.TempDir()
	path := filepath.Join(workDir, "blind.txt")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))

	tracker := newCtxSessionTracker(t, ctxSess)

	// Edit tool with empty SessionID — production scenario.
	editTool := newEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "",
	})

	// The file has NOT been viewed — the guard must refuse the edit.
	ctx := WithSessionID(context.Background(), ctxSess)
	res, err := editTool.Run(ctx, json.RawMessage(`{
		"path":       "blind.txt",
		"old_string": "original",
		"new_string": "modified"
	}`))
	require.NoError(t, err)
	require.True(t, res.IsError, "expected read-before-edit refusal")
	require.Contains(t, res.Content, "has not been read in this session")

	// File content must be unchanged.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original\n", string(got))
}

// TestPermissionCheckReceivesContextSessionID verifies that the permission
// Request.SessionID field is set to the session id from the run context (not
// the empty deps.SessionID). It uses SetAutoApproveSession to grant approval
// for the ctx session, then confirms the write tool proceeds (which it can only
// do if it routed the correct session id to the checker).
func TestPermissionCheckReceivesContextSessionID(t *testing.T) {
	const ctxSess = "ctx-perm-sess"

	workDir := t.TempDir()
	newFile := filepath.Join(workDir, "newfile.txt")

	// Build a real permission checker with no pubsub bus (so any non-approved
	// session would get DecisionDeny at the fallback step).
	checker := permission.New(nil, nil)
	// Grant auto-approval only for ctxSess.
	checker.SetAutoApproveSession(ctxSess, true)

	tracker := newCtxSessionTracker(t, ctxSess)

	// Write tool with empty SessionID — permission must route via ctx session.
	writeTool := newWriteTool(Dependencies{
		Permission:  checker,
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "", // empty, as in production
	})

	ctx := WithSessionID(context.Background(), ctxSess)
	res, err := writeTool.Run(ctx, json.RawMessage(`{
		"path":    "newfile.txt",
		"content": "namaste"
	}`))
	require.NoError(t, err)
	require.False(t, res.IsError,
		"write should succeed when ctx session is auto-approved; got: %s", res.Content)

	got, err := os.ReadFile(newFile)
	require.NoError(t, err)
	require.Equal(t, "namaste", string(got))

	// Confirm a different session would be denied: build another write tool
	// with a different ctx session (not auto-approved) and verify denial.
	const otherSess = "ctx-perm-other"
	// Create the other session in the DB so the tracker doesn't error.
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "deny.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.Queries.CreateSession(context.Background(), sqlc.CreateSessionParams{
		ID:          otherSess,
		ProjectPath: t.TempDir(),
		Title:       "deny test",
		Model:       "test-model",
		Agent:       "test-agent",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	otherFile := filepath.Join(workDir, "other.txt")
	writeToolOther := newWriteTool(Dependencies{
		Permission:  checker, // same checker, otherSess is NOT auto-approved
		FileTracker: filetracker.NewTracker(database, nil),
		WorkDir:     workDir,
		SessionID:   "", // empty
	})
	ctxOther := WithSessionID(context.Background(), otherSess)
	resOther, err := writeToolOther.Run(ctxOther, json.RawMessage(`{
		"path":    "other.txt",
		"content": "should be denied"
	}`))
	require.NoError(t, err)
	require.True(t, resOther.IsError, "write should be denied for non-approved session")
	// File must not have been created.
	_, statErr := os.Stat(otherFile)
	require.True(t, os.IsNotExist(statErr), "file should not exist after denied write")
}
