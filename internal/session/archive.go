package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// session_archive is a side table, owned by this package, that records which
// sessions are archived (soft-hidden from the default List). It is kept here
// rather than in a schema migration so the feature lives entirely within
// internal/session; a dedicated migration that adds a sessions.archived column
// (and an indexed query) would be the cleaner long-term home — see the package
// notes / followups. The table holds one row per archived session id.
const createArchiveTableSQL = `
CREATE TABLE IF NOT EXISTS session_archive (
    session_id TEXT PRIMARY KEY,
    archived_at INTEGER NOT NULL
)`

// ensureArchiveTable lazily creates the session_archive table the first time
// an archive operation runs. It is idempotent (CREATE TABLE IF NOT EXISTS) and
// runs the DDL at most once per Repo on success. A failed attempt (for example,
// a cancelled context) does not poison later calls: the table is only marked
// ready once creation succeeds, so the next call retries. Safe for concurrent
// use.
func (r *Repo) ensureArchiveTable(ctx context.Context) error {
	r.archiveMu.Lock()
	defer r.archiveMu.Unlock()
	if r.archiveReady {
		return nil
	}
	if _, err := r.database.ExecContext(ctx, createArchiveTableSQL); err != nil {
		return fmt.Errorf("creating session_archive table: %w", err)
	}
	r.archiveReady = true
	return nil
}

// archiveTableExists reports whether the session_archive side table has been
// created yet. It is a pure read against sqlite_master, letting read paths such
// as List/IsArchived treat a never-archived database as having an empty archive
// set without writing (creating) the table.
func (r *Repo) archiveTableExists(ctx context.Context) (bool, error) {
	r.archiveMu.Lock()
	ready := r.archiveReady
	r.archiveMu.Unlock()
	if ready {
		return true, nil
	}
	var name string
	err := r.database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_archive'`,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking session_archive table: %w", err)
	}
	return true, nil
}

// nowUnix returns the current time as Unix seconds in UTC, matching how the
// rest of the package stores timestamps.
func nowUnix() int64 {
	return time.Now().UTC().Unix()
}

// Archive soft-hides the session with the given id: it is removed from the
// default List results but remains fully retrievable via Get, via List with
// ListFilter.IncludeArchived, and as a fork origin. Archiving is idempotent —
// archiving an already-archived session is a no-op. Returns ErrNotFound if no
// session has that id.
func (r *Repo) Archive(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}
	if err := r.ensureArchiveTable(ctx); err != nil {
		return fmt.Errorf("archiving session %s: %w", id, err)
	}
	if _, err := r.database.ExecContext(
		ctx,
		`INSERT INTO session_archive (session_id, archived_at)
		 VALUES (?, ?)
		 ON CONFLICT (session_id) DO NOTHING`,
		id, nowUnix(),
	); err != nil {
		return fmt.Errorf("archiving session %s: %w", id, err)
	}
	return nil
}

// Unarchive reverses Archive, returning the session to the default List
// results. Unarchiving a session that is not archived is a no-op. Returns
// ErrNotFound if no session has that id.
func (r *Repo) Unarchive(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	if err := r.ensureArchiveTable(ctx); err != nil {
		return fmt.Errorf("unarchiving session %s: %w", id, err)
	}
	if _, err := r.database.ExecContext(
		ctx,
		`DELETE FROM session_archive WHERE session_id = ?`,
		id,
	); err != nil {
		return fmt.Errorf("unarchiving session %s: %w", id, err)
	}
	return nil
}

// IsArchived reports whether the session with the given id is currently
// archived. An unknown id reports false with no error. It is a pure read: if no
// session has ever been archived the side table does not exist yet and
// IsArchived reports false without creating it.
func (r *Repo) IsArchived(ctx context.Context, id string) (bool, error) {
	exists, err := r.archiveTableExists(ctx)
	if err != nil {
		return false, fmt.Errorf("checking archived state of session %s: %w", id, err)
	}
	if !exists {
		return false, nil
	}
	var n int
	if err := r.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM session_archive WHERE session_id = ?`,
		id,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("checking archived state of session %s: %w", id, err)
	}
	return n > 0, nil
}

// archivedSet returns the set of currently archived session ids. It is used by
// List to drop archived rows when ListFilter.IncludeArchived is false. It is a
// pure read: if no archive operation has ever run, the side table does not yet
// exist and archivedSet returns an empty set rather than creating it, so the
// read path never writes.
func (r *Repo) archivedSet(ctx context.Context) (map[string]struct{}, error) {
	exists, err := r.archiveTableExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}
	if !exists {
		return map[string]struct{}{}, nil
	}
	rows, err := r.database.QueryContext(ctx, `SELECT session_id FROM session_archive`)
	if err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}
	defer rows.Close()

	set := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading archived sessions: %w", err)
		}
		set[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}
	return set, nil
}
