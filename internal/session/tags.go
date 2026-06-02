package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// session_tags is a side table, owned by this package, that records the labels
// attached to each session. Like session_archive (see archive.go) it is kept
// here rather than in a schema migration so the feature lives entirely within
// internal/session; a dedicated migration that adds a normalized tags table
// (and indexed queries) would be the cleaner long-term home — see the package
// notes / followups. The table holds one row per (session_id, tag) pair, so a
// session may carry many tags and a tag may span many sessions.
const createTagsTableSQL = `
CREATE TABLE IF NOT EXISTS session_tags (
    session_id TEXT NOT NULL,
    tag        TEXT NOT NULL,
    PRIMARY KEY (session_id, tag)
)`

// ensureTagsTable lazily creates the session_tags table the first time a tag
// write runs. It is idempotent (CREATE TABLE IF NOT EXISTS) and runs the DDL at
// most once per Repo on success. A failed attempt (for example, a cancelled
// context) does not poison later calls: the table is only marked ready once
// creation succeeds, so the next call retries. Safe for concurrent use.
func (r *Repo) ensureTagsTable(ctx context.Context) error {
	r.tagsMu.Lock()
	defer r.tagsMu.Unlock()
	if r.tagsReady {
		return nil
	}
	if _, err := r.database.ExecContext(ctx, createTagsTableSQL); err != nil {
		return fmt.Errorf("creating session_tags table: %w", err)
	}
	r.tagsReady = true
	return nil
}

// tagsTableExists reports whether the session_tags side table has been created
// yet. It is a pure read against sqlite_master, letting read paths such as Tags
// and ListByTag treat a never-tagged database as having no tags without writing
// (creating) the table.
func (r *Repo) tagsTableExists(ctx context.Context) (bool, error) {
	r.tagsMu.Lock()
	ready := r.tagsReady
	r.tagsMu.Unlock()
	if ready {
		return true, nil
	}
	var name string
	err := r.database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_tags'`,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking session_tags table: %w", err)
	}
	return true, nil
}

// AddTag attaches tag to the session with the given id. Adding a tag a session
// already carries is a no-op (idempotent). Returns ErrNotFound if no session
// has that id.
func (r *Repo) AddTag(ctx context.Context, id, tag string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return fmt.Errorf("adding tag: %w", err)
	}
	if err := r.ensureTagsTable(ctx); err != nil {
		return fmt.Errorf("adding tag to session %s: %w", id, err)
	}
	if _, err := r.database.ExecContext(
		ctx,
		`INSERT INTO session_tags (session_id, tag)
		 VALUES (?, ?)
		 ON CONFLICT (session_id, tag) DO NOTHING`,
		id, tag,
	); err != nil {
		return fmt.Errorf("adding tag to session %s: %w", id, err)
	}
	return nil
}

// RemoveTag detaches tag from the session with the given id. Removing a tag the
// session does not carry is a no-op. Returns ErrNotFound if no session has that
// id.
func (r *Repo) RemoveTag(ctx context.Context, id, tag string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return fmt.Errorf("removing tag: %w", err)
	}
	if err := r.ensureTagsTable(ctx); err != nil {
		return fmt.Errorf("removing tag from session %s: %w", id, err)
	}
	if _, err := r.database.ExecContext(
		ctx,
		`DELETE FROM session_tags WHERE session_id = ? AND tag = ?`,
		id, tag,
	); err != nil {
		return fmt.Errorf("removing tag from session %s: %w", id, err)
	}
	return nil
}

// Tags returns the tags attached to the session with the given id, sorted
// alphabetically for a deterministic result. An unknown id (or a session with
// no tags) returns an empty, non-nil slice with no error. It is a pure read: if
// no tag has ever been written the side table does not exist yet and Tags
// returns no tags without creating it.
func (r *Repo) Tags(ctx context.Context, id string) ([]string, error) {
	exists, err := r.tagsTableExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading tags for session %s: %w", id, err)
	}
	if !exists {
		return []string{}, nil
	}
	rows, err := r.database.QueryContext(
		ctx,
		`SELECT tag FROM session_tags WHERE session_id = ? ORDER BY tag`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("reading tags for session %s: %w", id, err)
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("reading tags for session %s: %w", id, err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading tags for session %s: %w", id, err)
	}
	return tags, nil
}

// ListByTag returns every session carrying tag, ordered by UpdatedAt DESC to
// match List and Search. The query joins session_tags to sessions, so tag rows
// orphaned by a deleted session are skipped naturally and every returned value
// is a fully populated Session. Unlike List, ListByTag does not hide archived
// sessions — it returns every session bearing the tag; archived-tag filtering
// is left as a followup (see package notes). An unknown tag (or a database
// where nothing has been tagged) returns an empty, non-nil slice with no error.
// It is a pure read: if no tag has ever been written the side table does not
// exist yet and ListByTag returns no sessions without creating it.
func (r *Repo) ListByTag(ctx context.Context, tag string) ([]Session, error) {
	exists, err := r.tagsTableExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sessions by tag %q: %w", tag, err)
	}
	if !exists {
		return []Session{}, nil
	}
	rows, err := r.database.QueryContext(
		ctx,
		`SELECT s.id, s.project_path, s.title, s.model, s.agent,
		        s.created_at, s.updated_at
		   FROM session_tags t
		   JOIN sessions s ON s.id = t.session_id
		  WHERE t.tag = ?
		  ORDER BY s.updated_at DESC`,
		tag,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions by tag %q: %w", tag, err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var (
			s                    Session
			createdAt, updatedAt int64
		)
		if err := rows.Scan(
			&s.ID, &s.ProjectPath, &s.Title, &s.Model, &s.Agent,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("listing sessions by tag %q: %w", tag, err)
		}
		s.CreatedAt = time.Unix(createdAt, 0).UTC()
		s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing sessions by tag %q: %w", tag, err)
	}
	return sessions, nil
}
