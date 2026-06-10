package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// CompactionDetails carries the structured metadata stored inside an
// EntryCompaction node's Summary field (JSON-encoded). Keeping the text
// summary and the first-kept anchor together in one row avoids a separate
// side table while still letting CompactedContext reconstruct the effective
// context deterministically on load.
type CompactionDetails struct {
	// SummaryText is the condensed checkpoint produced at compaction time
	// (the text that replaced the dropped prefix). It is prepended verbatim
	// when reconstructing the context.
	SummaryText string `json:"summary_text"`
	// FirstKeptMsgID is the ID of the first message in the verbatim kept tail.
	// Messages from this ID onward (inclusive, in session order) form the tail
	// returned by CompactedContext.
	FirstKeptMsgID string `json:"first_kept_msg_id"`
}

// compactionSummaryKey is the JSON field used to detect whether a Summary
// column is a CompactionDetails blob or plain text in legacy/other entries.
// We check for it before unmarshalling so non-compaction entries with free
// text in Summary are never mis-parsed.
const compactionSummaryKey = `"summary_text"`

// RecordCompaction persists a compaction event as a durable EntryCompaction
// node in the session tree. parentID is the entry that immediately precedes
// the compaction (the tail of the current linear chain); pass nil to root the
// node at the session root (rare but valid for the very first compaction on
// a session with no earlier explicit entries). summary is the structured
// checkpoint text produced by the Compactor; firstKeptMsgID is the ID of the
// first message in the verbatim kept tail. The two values together let
// CompactedContext reconstruct the effective in-memory context on reload
// without re-running the summarizer.
//
// The returned entry carries the generated ID and timestamp.
func (r *Repo) RecordCompaction(ctx context.Context, sessionID string, parentID *string, summary string, firstKeptMsgID string) (*Entry, error) {
	details := CompactionDetails{
		SummaryText:    summary,
		FirstKeptMsgID: firstKeptMsgID,
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("recording compaction for session %s: marshalling details: %w", sessionID, err)
	}
	return r.AddEntry(ctx, &Entry{
		SessionID: sessionID,
		ParentID:  parentID,
		Type:      EntryCompaction,
		Summary:   string(encoded),
	})
}

// CompactedContext reconstructs the effective in-memory context for sessionID
// from its most recent EntryCompaction node, returning:
//
//   - summaryMsgs: a single synthetic message whose text is the stored
//     checkpoint summary (ready to be prepended to the provider history).
//   - keptMsgs: the verbatim tail of on-disk messages starting from (and
//     including) the first-kept message recorded at compaction time.
//
// When no EntryCompaction has been recorded for the session, both slices are
// nil and no error is returned — the caller should treat nil, nil as "no
// persisted compaction; load full history as normal".
//
// The reconstruction is deterministic: two callers on the same session state
// always produce identical output, making it safe to call after a restart.
func (r *Repo) CompactedContext(ctx context.Context, sessionID string) (summaryMsgs, keptMsgs []message.Message, err error) {
	entries, err := r.Entries(ctx, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconstructing compacted context for session %s: %w", sessionID, err)
	}

	// Find the most recent EntryCompaction entry (entries are oldest-first).
	var latest *Entry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == EntryCompaction {
			e := entries[i]
			latest = &e
			break
		}
	}
	if latest == nil {
		// No compaction recorded yet.
		return nil, nil, nil
	}

	// Decode the stored details. A Summary that does not contain the JSON key
	// is a legacy plain-text summary written before this feature existed; fall
	// back to treating the whole Summary as the summary text with no kept tail.
	var details CompactionDetails
	if strings.Contains(latest.Summary, compactionSummaryKey) {
		if err := json.Unmarshal([]byte(latest.Summary), &details); err != nil {
			return nil, nil, fmt.Errorf("reconstructing compacted context for session %s: decoding compaction details: %w", sessionID, err)
		}
	} else {
		// Legacy plain-text summary: reconstruct with summary only, no kept tail.
		details.SummaryText = latest.Summary
	}

	// Build the synthetic summary message.
	summaryMsg := message.Message{
		SessionID: sessionID,
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: details.SummaryText}},
		CreatedAt: latest.CreatedAt,
	}
	summaryMsgs = []message.Message{summaryMsg}

	// If no first-kept anchor was recorded (legacy or empty-tail case), return
	// only the summary with an empty kept tail.
	if details.FirstKeptMsgID == "" {
		return summaryMsgs, nil, nil
	}

	// Load the on-disk messages and find the kept tail starting at the anchor.
	allMsgs, err := r.Messages(ctx, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconstructing compacted context for session %s: loading messages: %w", sessionID, err)
	}
	anchorIdx := -1
	for i, m := range allMsgs {
		if m.ID == details.FirstKeptMsgID {
			anchorIdx = i
			break
		}
	}
	if anchorIdx < 0 {
		// The anchor message no longer exists (e.g. was deleted). Return the
		// summary only so the caller has something useful rather than an error.
		return summaryMsgs, nil, nil
	}

	keptMsgs = append([]message.Message(nil), allMsgs[anchorIdx:]...)
	return summaryMsgs, keptMsgs, nil
}

// EntryType classifies a node in a session's history tree. A session's flat
// message list is the spine of a conversation, but a session can branch: the
// user rewinds to an earlier point and explores a different direction, the
// transcript is compacted, or the model is switched mid-session. The entry tree
// records those structural events as typed nodes so a UI can show the shape of
// the exploration and Fork can carry a single lineage forward.
type EntryType string

const (
	// EntryMessage is a node standing in for one conversation message. Its RefID
	// points at the messages-table row it represents.
	EntryMessage EntryType = "message"
	// EntryCompaction marks where the transcript before it was summarized.
	EntryCompaction EntryType = "compaction"
	// EntryModelChange marks where the active model was switched; Summary holds
	// the new model id.
	EntryModelChange EntryType = "model-change"
	// EntryBranchSummary is a durable summary of an abandoned branch, recorded so
	// that rewinding and exploring elsewhere does not lose what the prior path
	// found. See ForkFromEntry.
	EntryBranchSummary EntryType = "branch-summary"
)

// validEntryTypes is the set of accepted EntryType values, kept as a package var
// so AddEntry can reject unknown types without a long switch.
var validEntryTypes = map[EntryType]bool{
	EntryMessage:       true,
	EntryCompaction:    true,
	EntryModelChange:   true,
	EntryBranchSummary: true,
}

// Entry is one node in a session's history tree. A nil ParentID marks a root
// (the first entry of a session, or the root copied into a fork). RefID is the
// optional id of the artefact the entry stands for: a message id for
// EntryMessage, or the source entry id for an EntryBranchSummary. Summary holds
// any free text payload (the branch summary, the new model id, ...).
type Entry struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	ParentID  *string   `json:"parent_id,omitempty"`
	Type      EntryType `json:"type"`
	RefID     *string   `json:"ref_id,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// session_entries is a side table, owned by this package, that records the
// history-tree nodes for each session. Like session_archive (see archive.go) it
// is kept here rather than in a schema migration so the feature lives entirely
// within internal/session; a dedicated migration that normalizes entries (and
// indexed queries) would be the cleaner long-term home — see the package notes.
// One row per entry; parent_id references another row's id within the same
// session (enforced in Go by AddEntry, not by a foreign key).
const createEntriesTableSQL = `
CREATE TABLE IF NOT EXISTS session_entries (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    parent_id   TEXT,
    entry_type  TEXT NOT NULL,
    ref_id      TEXT,
    summary     TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_entries_session_id ON session_entries (session_id);
CREATE INDEX IF NOT EXISTS idx_session_entries_parent_id  ON session_entries (parent_id);`

// ensureEntriesTable lazily creates the session_entries table the first time an
// entry write runs. It is idempotent (CREATE TABLE IF NOT EXISTS) and runs the
// DDL at most once per Repo on success. A failed attempt (for example, a
// cancelled context) does not poison later calls: the table is only marked ready
// once creation succeeds, so the next call retries. Safe for concurrent use.
func (r *Repo) ensureEntriesTable(ctx context.Context) error {
	r.entriesMu.Lock()
	defer r.entriesMu.Unlock()
	if r.entriesReady {
		return nil
	}
	if _, err := r.database.ExecContext(ctx, createEntriesTableSQL); err != nil {
		return fmt.Errorf("creating session_entries table: %w", err)
	}
	r.entriesReady = true
	return nil
}

// entriesTableExists reports whether the session_entries side table has been
// created yet. It is a pure read against sqlite_master, letting read paths such
// as Entries and GetPathToRoot treat a session with no recorded entries as
// empty without writing (creating) the table.
func (r *Repo) entriesTableExists(ctx context.Context) (bool, error) {
	r.entriesMu.Lock()
	ready := r.entriesReady
	r.entriesMu.Unlock()
	if ready {
		return true, nil
	}
	var name string
	err := r.database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_entries'`,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking session_entries table: %w", err)
	}
	return true, nil
}

// AddEntry appends e to the history tree of e.SessionID and returns the stored
// entry (with any generated ID and CreatedAt filled in). The session must exist
// (else ErrNotFound), the type must be one of the EntryType constants (else an
// error), and a non-nil ParentID must name an entry already in the same session
// (else ErrNotFound) so the tree can never reference a missing or cross-session
// parent. A zero ID is assigned a fresh UUID; a zero CreatedAt is set to now.
func (r *Repo) AddEntry(ctx context.Context, e *Entry) (*Entry, error) {
	if e == nil {
		return nil, fmt.Errorf("adding entry: entry is nil")
	}
	if _, err := r.Get(ctx, e.SessionID); err != nil {
		return nil, fmt.Errorf("adding entry: %w", err)
	}
	if !validEntryTypes[e.Type] {
		return nil, fmt.Errorf("adding entry: unknown entry type %q", e.Type)
	}
	if err := r.ensureEntriesTable(ctx); err != nil {
		return nil, fmt.Errorf("adding entry: %w", err)
	}
	if e.ParentID != nil {
		if _, err := r.GetEntry(ctx, e.SessionID, *e.ParentID); err != nil {
			return nil, fmt.Errorf("adding entry: parent %s: %w", *e.ParentID, err)
		}
	}

	stored := *e
	if stored.ID == "" {
		id, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("adding entry: %w", err)
		}
		stored.ID = id
	}
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now().UTC()
	}

	if _, err := r.database.ExecContext(
		ctx,
		`INSERT INTO session_entries (id, session_id, parent_id, entry_type, ref_id, summary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		stored.ID, stored.SessionID, stored.ParentID, string(stored.Type), stored.RefID, stored.Summary,
		stored.CreatedAt.UTC().Unix(),
	); err != nil {
		return nil, fmt.Errorf("adding entry to session %s: %w", stored.SessionID, err)
	}
	return &stored, nil
}

// GetEntry fetches a single entry by id within sessionID. It returns ErrNotFound
// if the session has no such entry (including when no entry has ever been
// recorded, so the side table does not yet exist).
func (r *Repo) GetEntry(ctx context.Context, sessionID, entryID string) (*Entry, error) {
	exists, err := r.entriesTableExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting entry %s: %w", entryID, err)
	}
	if !exists {
		return nil, fmt.Errorf("getting entry %s: %w", entryID, ErrNotFound)
	}
	row := r.database.QueryRowContext(
		ctx,
		`SELECT id, session_id, parent_id, entry_type, ref_id, summary, created_at
		   FROM session_entries
		  WHERE session_id = ? AND id = ?`,
		sessionID, entryID,
	)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("getting entry %s: %w", entryID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting entry %s: %w", entryID, err)
	}
	return e, nil
}

// Entries returns every entry recorded for sessionID, oldest first, with a
// stable tie-break on rowid (insertion order) when two entries share the same
// created_at second, so a tree built within one second still lists in the order
// it was written. A
// session with no entries (or an unknown id) returns an empty, non-nil slice.
// It is a pure read: a session that has never recorded an entry returns no
// entries without creating the side table.
func (r *Repo) Entries(ctx context.Context, sessionID string) ([]Entry, error) {
	exists, err := r.entriesTableExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing entries for session %s: %w", sessionID, err)
	}
	if !exists {
		return []Entry{}, nil
	}
	rows, err := r.database.QueryContext(
		ctx,
		`SELECT id, session_id, parent_id, entry_type, ref_id, summary, created_at
		   FROM session_entries
		  WHERE session_id = ?
		  ORDER BY created_at ASC, rowid ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing entries for session %s: %w", sessionID, err)
	}
	defer rows.Close()

	entries := make([]Entry, 0)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("listing entries for session %s: %w", sessionID, err)
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing entries for session %s: %w", sessionID, err)
	}
	return entries, nil
}

// GetPathToRoot returns the chain of entries from the session root down to (and
// including) entryID, in root-first order. It is the lineage Fork carries
// forward and the context a UI shows for a selected node. Returns ErrNotFound if
// entryID is not in the session. A parent reference that does not resolve (a
// dangling link) terminates the walk rather than erroring, and a cycle is
// guarded against so a corrupt tree cannot spin forever.
//
// When no explicit entries have been recorded for sessionID, GetPathToRoot falls
// back to a synthetic linear chain derived from the session's messages so it
// works for sessions that pre-date or bypass explicit AddEntry calls.
func (r *Repo) GetPathToRoot(ctx context.Context, sessionID, entryID string) ([]Entry, error) {
	all, err := r.entriesOrImplicit(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting path to root: %w", err)
	}
	byID := make(map[string]Entry, len(all))
	for _, e := range all {
		byID[e.ID] = e
	}
	cur, ok := byID[entryID]
	if !ok {
		return nil, fmt.Errorf("getting path to root: entry %s: %w", entryID, ErrNotFound)
	}

	var rev []Entry
	visited := make(map[string]struct{}, len(all))
	for {
		if _, seen := visited[cur.ID]; seen {
			break // cycle guard: a corrupt tree must not loop forever.
		}
		visited[cur.ID] = struct{}{}
		rev = append(rev, cur)
		if cur.ParentID == nil {
			break
		}
		parent, ok := byID[*cur.ParentID]
		if !ok {
			break // dangling parent: stop at the highest reachable ancestor.
		}
		cur = parent
	}

	// rev is leaf-first; reverse into root-first order.
	path := make([]Entry, len(rev))
	for i, e := range rev {
		path[len(rev)-1-i] = e
	}
	return path, nil
}

// GetBranch returns the subtree rooted at entryID — that entry followed by all
// of its descendants — in a deterministic order (breadth-first, siblings in the
// insertion order Entries returns). It is the set of nodes a "delete this branch" or
// "summarize this branch" operation acts on. Returns ErrNotFound if entryID is
// not in the session.
//
// When no explicit entries have been recorded for sessionID, GetBranch falls
// back to a synthetic linear chain derived from the session's messages so it
// works for sessions that pre-date or bypass explicit AddEntry calls.
func (r *Repo) GetBranch(ctx context.Context, sessionID, entryID string) ([]Entry, error) {
	all, err := r.entriesOrImplicit(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting branch: %w", err)
	}
	root := -1
	children := make(map[string][]Entry, len(all))
	for i, e := range all {
		if e.ID == entryID {
			root = i
		}
		if e.ParentID != nil {
			children[*e.ParentID] = append(children[*e.ParentID], e)
		}
	}
	if root == -1 {
		return nil, fmt.Errorf("getting branch: entry %s: %w", entryID, ErrNotFound)
	}

	// Entries is already sorted by (created_at, rowid), so each children slice
	// inherits that order; no per-slice re-sort is needed.
	branch := []Entry{all[root]}
	visited := map[string]struct{}{all[root].ID: {}}
	for i := 0; i < len(branch); i++ {
		for _, child := range children[branch[i].ID] {
			if _, seen := visited[child.ID]; seen {
				continue // cycle guard.
			}
			visited[child.ID] = struct{}{}
			branch = append(branch, child)
		}
	}
	return branch, nil
}

// ForkFromEntryOptions configures a ForkFromEntry call. The zero value forks the
// lineage up to the chosen entry and derives the title from the source.
type ForkFromEntryOptions struct {
	// Title overrides the forked session's title. When empty, the title is
	// "<source title> (fork)".
	Title string
	// BranchSummary, when non-empty, records a branch-summary entry at the fork
	// point in the new session, capturing the gist of the source branch being
	// stepped away from so an abandoned exploration is not lost. Its RefID points
	// back at the source entry the fork was taken from.
	BranchSummary string
}

// ForkFromEntry creates a new session that carries forward only the lineage from
// the session root down to fromEntry, letting the user rewind to an earlier
// point and explore a different direction without disturbing the source. The
// fork receives a fresh ID, a title like "<source title> (fork)", and an
// OriginSessionID pointing at fromSession. The entries on the path are copied
// with fresh IDs (parent links remapped within the fork); for each message entry
// the underlying message is copied too, so the fork is an independent, working
// session. If opts.BranchSummary is set, a branch-summary entry is appended at
// the fork point so the abandoned source branch persists as a durable node.
//
// The entire fork — session row, message copies, entry copies, and optional
// branch-summary — is executed inside a single DB transaction so a mid-way
// failure leaves no partial session.
//
// Returns ErrNotFound if fromSession or fromEntry does not exist.
func (r *Repo) ForkFromEntry(ctx context.Context, fromSession, fromEntry string, opts ForkFromEntryOptions) (*Session, error) {
	src, err := r.Get(ctx, fromSession)
	if err != nil {
		return nil, fmt.Errorf("forking from entry: %w", err)
	}
	path, err := r.GetPathToRoot(ctx, fromSession, fromEntry)
	if err != nil {
		return nil, fmt.Errorf("forking from entry: %w", err)
	}

	// Load the source messages once so message entries can be copied by value.
	srcMsgs, err := r.Messages(ctx, fromSession)
	if err != nil {
		return nil, fmt.Errorf("forking from entry: %w", err)
	}
	msgByID := make(map[string]message.Message, len(srcMsgs))
	for _, m := range srcMsgs {
		msgByID[m.ID] = m
	}

	// Pre-plan the message copies (deduping shared references) so the fork's
	// MessageCount is known before the session row is written.
	msgRemap := make(map[string]string)
	for _, e := range path {
		if e.Type != EntryMessage || e.RefID == nil {
			continue
		}
		if _, ok := msgByID[*e.RefID]; !ok {
			continue
		}
		if _, planned := msgRemap[*e.RefID]; planned {
			continue
		}
		newID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("forking from entry: %w", err)
		}
		msgRemap[*e.RefID] = newID
	}

	// Build the ordered list of messages to copy: sort by (created_at, original
	// position in srcMsgs) so the fork preserves source order even when two
	// messages share the same second.
	type msgCopy struct {
		srcID    string
		srcIndex int
		m        message.Message
	}
	orderedMsgs := make([]msgCopy, 0, len(msgRemap))
	for i, m := range srcMsgs {
		if _, ok := msgRemap[m.ID]; ok {
			orderedMsgs = append(orderedMsgs, msgCopy{srcID: m.ID, srcIndex: i, m: m})
		}
	}
	sort.Slice(orderedMsgs, func(i, j int) bool {
		ti, tj := orderedMsgs[i].m.CreatedAt, orderedMsgs[j].m.CreatedAt
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return orderedMsgs[i].srcIndex < orderedMsgs[j].srcIndex
	})

	title := opts.Title
	if title == "" {
		title = src.Title + " (fork)"
	}
	forkID, err := newUUID()
	if err != nil {
		return nil, fmt.Errorf("forking from entry: %w", err)
	}
	now := time.Now().UTC()
	fork := &Session{
		ID:              forkID,
		ProjectPath:     src.ProjectPath,
		Title:           title,
		Model:           src.Model,
		Agent:           src.Agent,
		CreatedAt:       now,
		UpdatedAt:       now,
		MessageCount:    len(msgRemap),
		OriginSessionID: &fromSession,
	}

	// Ensure the entries side table exists before opening the transaction so
	// the DDL (which cannot run inside a deferred-rollback tx on SQLite) has
	// already committed. Idempotent: a no-op if the table was created earlier.
	if err := r.ensureEntriesTable(ctx); err != nil {
		return nil, fmt.Errorf("forking from entry: %w", err)
	}

	// Pre-generate all new UUIDs before the transaction so failures inside the
	// transaction body are limited to DB writes, not UUID generation.
	entRemap := make(map[string]string, len(path))
	for _, e := range path {
		newID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("forking from entry: %w", err)
		}
		entRemap[e.ID] = newID
	}
	var summaryID string
	if opts.BranchSummary != "" {
		summaryID, err = newUUID()
		if err != nil {
			return nil, fmt.Errorf("forking from entry: %w", err)
		}
	}

	// --- BEGIN TRANSACTION ---
	// Everything from here is atomic: a failure at any step rolls back to leave
	// no partial session in the database.
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("forking from entry: beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txq := r.database.Queries.WithTx(tx)

	if _, err := txq.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:           fork.ID,
		ProjectPath:  fork.ProjectPath,
		Title:        fork.Title,
		Model:        fork.Model,
		Agent:        fork.Agent,
		CreatedAt:    fork.CreatedAt.Unix(),
		UpdatedAt:    fork.UpdatedAt.Unix(),
		MessageCount: int64(fork.MessageCount),
	}); err != nil {
		return nil, fmt.Errorf("forking from entry: creating fork session: %w", err)
	}
	if err := txq.SetSessionOrigin(ctx, sqlc.SetSessionOriginParams{
		OriginSessionID: &fromSession,
		ID:              fork.ID,
	}); err != nil {
		return nil, fmt.Errorf("forking from entry: recording origin: %w", err)
	}

	// Copy the referenced messages in source order (sorted above) so the fork
	// preserves the original conversation sequence.
	for _, mc := range orderedMsgs {
		m := mc.m
		newMsgID := msgRemap[mc.srcID]
		contentBytes, err := json.Marshal(m.Content)
		if err != nil {
			return nil, fmt.Errorf("forking from entry: marshalling message content: %w", err)
		}
		if _, err := txq.CreateMessage(ctx, sqlc.CreateMessageParams{
			ID:          newMsgID,
			SessionID:   fork.ID,
			Role:        string(m.Role),
			ContentJson: string(contentBytes),
			ParentID:    nil,
			CreatedAt:   m.CreatedAt.UTC().Unix(),
		}); err != nil {
			return nil, fmt.Errorf("forking from entry: copying message: %w", err)
		}
	}

	// Copy entries in root-first order (path is already root-first from
	// GetPathToRoot). Each entry's parent is remapped within the fork; a
	// message entry's RefID is repointed at the copied message.
	for _, e := range path {
		var parent *string
		if e.ParentID != nil {
			if mapped, ok := entRemap[*e.ParentID]; ok {
				parent = &mapped
			}
		}
		ref := e.RefID
		if e.Type == EntryMessage && e.RefID != nil {
			if mapped, ok := msgRemap[*e.RefID]; ok {
				ref = &mapped
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO session_entries (id, session_id, parent_id, entry_type, ref_id, summary, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			entRemap[e.ID], fork.ID, parent, string(e.Type), ref, e.Summary, e.CreatedAt.UTC().Unix(),
		); err != nil {
			return nil, fmt.Errorf("forking from entry: copying entry: %w", err)
		}
	}

	// Record the optional branch summary at the fork point so the source branch
	// stepped away from is not lost.
	if opts.BranchSummary != "" {
		forkPoint := entRemap[fromEntry]
		sourceRef := fromEntry
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO session_entries (id, session_id, parent_id, entry_type, ref_id, summary, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			summaryID, fork.ID, &forkPoint, string(EntryBranchSummary), &sourceRef, opts.BranchSummary, now.Unix(),
		); err != nil {
			return nil, fmt.Errorf("forking from entry: recording branch summary: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("forking from entry: committing transaction: %w", err)
	}
	// --- END TRANSACTION ---

	return fork, nil
}

// implicitEntryID returns the deterministic synthetic entry ID used when
// building an implicit entry from a message: "msg:" + messageID. Using the
// message ID as a suffix lets callers that know only the message ID derive the
// implicit entry ID without round-tripping through the DB.
func implicitEntryID(msgID string) string {
	return "msg:" + msgID
}

// ImplicitEntryID is the exported form of implicitEntryID, exposed so tests
// and callers outside the package can derive the synthetic entry ID for a
// message without reading the entries table.
func ImplicitEntryID(msgID string) string { return implicitEntryID(msgID) }

// implicitEntriesFromMessages builds a synthetic, linear chain of
// EntryMessage entries from msgs, one per message in source order (oldest
// first). It is used as a fallback when no explicit tree entries have been
// recorded for a session, so that GetPathToRoot, GetBranch, and ForkFromEntry
// work correctly even for sessions that pre-date or bypass AddEntry. The
// returned entries are in-memory only; they are never written to the database.
func implicitEntriesFromMessages(sessionID string, msgs []message.Message) []Entry {
	entries := make([]Entry, 0, len(msgs))
	var prevID *string
	for _, m := range msgs {
		id := implicitEntryID(m.ID)
		msgID := m.ID
		e := Entry{
			ID:        id,
			SessionID: sessionID,
			ParentID:  prevID,
			Type:      EntryMessage,
			RefID:     &msgID,
			CreatedAt: m.CreatedAt,
		}
		entries = append(entries, e)
		idCopy := id
		prevID = &idCopy
	}
	return entries
}

// entriesOrImplicit returns the explicit entries for sessionID when at least
// one has been recorded, otherwise it synthesizes a linear chain of
// EntryMessage entries from the session's messages so callers always see a
// non-empty tree for a session that has messages. It is the single read gate
// that GetPathToRoot, GetBranch, and ForkFromEntry should use.
func (r *Repo) entriesOrImplicit(ctx context.Context, sessionID string) ([]Entry, error) {
	explicit, err := r.Entries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(explicit) > 0 {
		return explicit, nil
	}
	msgs, err := r.Messages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("building implicit entries for session %s: %w", sessionID, err)
	}
	return implicitEntriesFromMessages(sessionID, msgs), nil
}

// rowScanner is the read surface shared by *sql.Row and *sql.Rows so scanEntry
// can serve both GetEntry (single row) and the list/walk queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanEntry decodes one session_entries row into an Entry, converting the stored
// Unix-second created_at back to a UTC time.
func scanEntry(s rowScanner) (*Entry, error) {
	var (
		e         Entry
		entryType string
		createdAt int64
	)
	if err := s.Scan(&e.ID, &e.SessionID, &e.ParentID, &entryType, &e.RefID, &e.Summary, &createdAt); err != nil {
		return nil, err
	}
	e.Type = EntryType(entryType)
	e.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &e, nil
}
