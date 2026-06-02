// Package session persists conversation threads.
package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// Session is one persisted conversation thread.
type Session struct {
	ID           string    `json:"id"`
	ProjectPath  string    `json:"project_path"`
	Title        string    `json:"title"`
	Model        string    `json:"model"` // Model ID (e.g. "deepseek-chat", "kimi-k2").
	Agent        string    `json:"agent"` // Named agent ("coder", "task", ...).
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	// OriginSessionID is the ID of the session this one was forked from, or
	// nil for sessions created directly. It is populated by Fork on the
	// returned session and may be read back with OriginOf.
	OriginSessionID *string `json:"origin_session_id,omitempty"`
}

// ListFilter narrows a Repo.List call.
// A zero ListFilter returns every session, newest first.
type ListFilter struct {
	ProjectPath string    // Exact-match filter; empty disables.
	Since       time.Time // UpdatedAt >= Since; zero disables.
	Limit       int       // 0 means no limit.
}

// Repo is the public handle for session storage. All methods take
// a context and return wrapped errors. Repo is safe for concurrent
// use by multiple goroutines.
type Repo struct {
	database *db.DB
	mu       sync.Mutex
}

// Sentinel errors returned by Repo methods.
var (
	ErrNotFound      = errors.New("session not found")
	ErrAlreadyExists = errors.New("session already exists")
)

// NewRepo constructs a Repo backed by the given SQLite handle.
func NewRepo(database *db.DB) *Repo {
	return &Repo{
		database: database,
	}
}

// Create inserts s. s.ID, s.CreatedAt, and s.UpdatedAt are populated by
// Create if zero. Returns ErrAlreadyExists on PK collision.
func (r *Repo) Create(ctx context.Context, s *Session) error {
	if s == nil {
		return fmt.Errorf("creating session: session is nil")
	}
	if s.ID == "" {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
		s.ID = id
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	if s.Title == "" {
		s.Title = "New session"
	}

	params := sqlc.CreateSessionParams{
		ID:           s.ID,
		ProjectPath:  s.ProjectPath,
		Title:        s.Title,
		Model:        s.Model,
		Agent:        s.Agent,
		CreatedAt:    s.CreatedAt.UTC().Unix(),
		UpdatedAt:    s.UpdatedAt.UTC().Unix(),
		MessageCount: int64(s.MessageCount),
	}

	_, err := r.database.Queries.CreateSession(ctx, params)
	if err != nil {
		// Unique constraint failure on the primary key ID is the only constraint
		// that can fail in sessions table since other fields are unconstrained.
		if strings.Contains(err.Error(), "constraint failed") || strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("creating session: %w", ErrAlreadyExists)
		}
		return fmt.Errorf("creating session in database: %w", err)
	}

	return nil
}

// Get fetches by ID. Returns ErrNotFound if absent.
func (r *Repo) Get(ctx context.Context, id string) (*Session, error) {
	row, err := r.database.Queries.GetSessionByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("getting session %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("getting session %s from database: %w", id, err)
	}

	s := &Session{
		ID:           row.ID,
		ProjectPath:  row.ProjectPath,
		Title:        row.Title,
		Model:        row.Model,
		Agent:        row.Agent,
		CreatedAt:    time.Unix(row.CreatedAt, 0).UTC(),
		UpdatedAt:    time.Unix(row.UpdatedAt, 0).UTC(),
		MessageCount: int(row.MessageCount),
	}
	return s, nil
}

// List returns sessions matching the filter, ordered by UpdatedAt DESC.
func (r *Repo) List(ctx context.Context, f ListFilter) ([]Session, error) {
	var sinceUnix int64
	if !f.Since.IsZero() {
		sinceUnix = f.Since.UTC().Unix()
	}

	params := sqlc.ListSessionsFilteredParams{
		Column1: f.ProjectPath,
		Column2: f.ProjectPath,
		Column3: sinceUnix,
		Column4: int64(f.Limit),
		Column5: int64(f.Limit),
	}

	rows, err := r.database.Queries.ListSessionsFiltered(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	sessions := make([]Session, len(rows))
	for i, row := range rows {
		sessions[i] = Session{
			ID:           row.ID,
			ProjectPath:  row.ProjectPath,
			Title:        row.Title,
			Model:        row.Model,
			Agent:        row.Agent,
			CreatedAt:    time.Unix(row.CreatedAt, 0).UTC(),
			UpdatedAt:    time.Unix(row.UpdatedAt, 0).UTC(),
			MessageCount: int(row.MessageCount),
		}
	}
	return sessions, nil
}

// Update writes mutable fields (Title, Model, Agent, UpdatedAt). Other
// fields are ignored. UpdatedAt is set to time.Now() if zero.
func (r *Repo) Update(ctx context.Context, s *Session) error {
	if s == nil {
		return fmt.Errorf("updating session: session is nil")
	}
	existing, err := r.Get(ctx, s.ID)
	if err != nil {
		return fmt.Errorf("updating session: %w", err)
	}

	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}

	// ProjectPath and MessageCount are ignored for updates, so we use the
	// existing database values.
	s.ProjectPath = existing.ProjectPath
	s.MessageCount = existing.MessageCount

	params := sqlc.UpdateSessionParams{
		ID:           s.ID,
		ProjectPath:  existing.ProjectPath,
		Title:        s.Title,
		Model:        s.Model,
		Agent:        s.Agent,
		UpdatedAt:    s.UpdatedAt.UTC().Unix(),
		MessageCount: int64(existing.MessageCount),
	}

	_, err = r.database.Queries.UpdateSession(ctx, params)
	if err != nil {
		return fmt.Errorf("updating session in database: %w", err)
	}

	return nil
}

// Delete removes the session row. The schema FK cascade also removes
// every messages.session_id, file_changes.session_id, and
// ledger_entries.session_id row matching id.
func (r *Repo) Delete(ctx context.Context, id string) error {
	err := r.database.Queries.DeleteSession(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting session %s: %w", id, err)
	}
	return nil
}

// AppendMessage inserts msg into the messages table, links it to
// sessionID, increments the session's MessageCount, bumps UpdatedAt,
// and — if the session's Title is still the placeholder and msg is
// the first user message — generates a title from msg's text.
// AppendMessage is the only path by which the session message count
// and timestamps are mutated; callers must not write to messages
// directly.
func (r *Repo) AppendMessage(ctx context.Context, sessionID string, msg message.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, err := r.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("appending message: %w", err)
	}

	if s.Title == "New session" && msg.Role == message.RoleUser {
		newTitle := TitleFromFirstMessage(msg)
		s.Title = newTitle
		slog.Debug("Auto-titling session", "session_id", s.ID, "title", newTitle)
	}

	s.MessageCount++
	s.UpdatedAt = time.Now().UTC()

	params := sqlc.UpdateSessionParams{
		ID:           s.ID,
		ProjectPath:  s.ProjectPath,
		Title:        s.Title,
		Model:        s.Model,
		Agent:        s.Agent,
		UpdatedAt:    s.UpdatedAt.UTC().Unix(),
		MessageCount: int64(s.MessageCount),
	}
	_, err = r.database.Queries.UpdateSession(ctx, params)
	if err != nil {
		return fmt.Errorf("updating session message count/title: %w", err)
	}

	contentBytes, err := json.Marshal(msg.Content)
	if err != nil {
		return fmt.Errorf("marshalling message content: %w", err)
	}

	// If the message has no ID, we generate a UUID for it.
	if msg.ID == "" {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("generating message ID: %w", err)
		}
		msg.ID = id
	}

	msgCreatedAt := msg.CreatedAt
	if msgCreatedAt.IsZero() {
		msgCreatedAt = time.Now().UTC()
	}

	msgParams := sqlc.CreateMessageParams{
		ID:          msg.ID,
		SessionID:   sessionID,
		Role:        string(msg.Role),
		ContentJson: string(contentBytes),
		ParentID:    msg.ParentID,
		CreatedAt:   msgCreatedAt.UTC().Unix(),
	}

	_, err = r.database.Queries.CreateMessage(ctx, msgParams)
	if err != nil {
		return fmt.Errorf("inserting message: %w", err)
	}

	return nil
}

// Messages returns every message in the session, oldest first.
func (r *Repo) Messages(ctx context.Context, sessionID string) ([]message.Message, error) {
	rows, err := r.database.Queries.ListMessagesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing messages for session %s: %w", sessionID, err)
	}

	messages := make([]message.Message, len(rows))
	for i, row := range rows {
		type msgEnvelope struct {
			ID        string          `json:"id"`
			SessionID string          `json:"session_id"`
			Role      message.Role    `json:"role"`
			Content   json.RawMessage `json:"content"`
			ParentID  *string         `json:"parent_id,omitempty"`
			CreatedAt time.Time       `json:"created_at"`
		}

		// We marshal the row's values into a helper struct that matches the
		// JSON structure message.Message expects, then deserialize using
		// message.Message's custom UnmarshalJSON. This ensures all content
		// blocks are parsed correctly using the message package's logic.
		env := msgEnvelope{
			ID:        row.ID,
			SessionID: row.SessionID,
			Role:      message.Role(row.Role),
			Content:   json.RawMessage(row.ContentJson),
			ParentID:  row.ParentID,
			CreatedAt: time.Unix(row.CreatedAt, 0).UTC(),
		}

		envBytes, err := json.Marshal(env)
		if err != nil {
			return nil, fmt.Errorf("serializing message envelope: %w", err)
		}

		var msg message.Message
		if err := json.Unmarshal(envBytes, &msg); err != nil {
			return nil, fmt.Errorf("deserializing message %s: %w", row.ID, err)
		}

		messages[i] = msg
	}
	return messages, nil
}

// Latest returns the most recently updated session for projectPath,
// or ErrNotFound if none. Used by bharatcode --continue.
func (r *Repo) Latest(ctx context.Context, projectPath string) (*Session, error) {
	row, err := r.database.Queries.GetLatestSessionByProjectPath(ctx, projectPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("getting latest session for project %s: %w", projectPath, ErrNotFound)
		}
		return nil, fmt.Errorf("getting latest session for project %s from database: %w", projectPath, err)
	}

	s := &Session{
		ID:           row.ID,
		ProjectPath:  row.ProjectPath,
		Title:        row.Title,
		Model:        row.Model,
		Agent:        row.Agent,
		CreatedAt:    time.Unix(row.CreatedAt, 0).UTC(),
		UpdatedAt:    time.Unix(row.UpdatedAt, 0).UTC(),
		MessageCount: int(row.MessageCount),
	}
	return s, nil
}

// ForkOptions configures a Fork call. The zero value forks the entire
// source session, copying every message, and derives the title from the
// source.
type ForkOptions struct {
	// CutoffMessageID, when non-nil, limits the copy to messages up to and
	// including the message with this ID (in oldest-first order). Messages
	// after it are not copied. If the ID is not found in the source session,
	// Fork returns ErrNotFound. Nil copies every message.
	CutoffMessageID *string
	// Title overrides the forked session's title. When empty, the title is
	// "<source title> (fork)".
	Title string
}

// Fork creates a new session whose messages are copied from sourceSessionID
// up to opts.CutoffMessageID (default: all messages), letting the user branch
// an exploration without mutating the original. The new session receives a
// fresh ID, a title like "<source title> (fork)", its own CreatedAt and
// UpdatedAt, and an OriginSessionID pointing back at the source. Copied
// messages get fresh IDs; intra-fork ParentID references are remapped to the
// new IDs so the branch's message graph stays internally consistent. The
// source session and its messages are left unchanged.
func (r *Repo) Fork(ctx context.Context, sourceSessionID string, opts ForkOptions) (*Session, error) {
	src, err := r.Get(ctx, sourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
	}

	msgs, err := r.Messages(ctx, sourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
	}

	// Apply the cutoff: keep messages up to and including the cutoff message.
	if opts.CutoffMessageID != nil {
		cutoff := *opts.CutoffMessageID
		idx := -1
		for i, m := range msgs {
			if m.ID == cutoff {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, fmt.Errorf("forking session: cutoff message %s: %w", cutoff, ErrNotFound)
		}
		msgs = msgs[:idx+1]
	}

	title := opts.Title
	if title == "" {
		title = src.Title + " (fork)"
	}

	forkID, err := newUUID()
	if err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
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
		MessageCount:    len(msgs),
		OriginSessionID: &sourceSessionID,
	}

	createParams := sqlc.CreateSessionParams{
		ID:           fork.ID,
		ProjectPath:  fork.ProjectPath,
		Title:        fork.Title,
		Model:        fork.Model,
		Agent:        fork.Agent,
		CreatedAt:    fork.CreatedAt.Unix(),
		UpdatedAt:    fork.UpdatedAt.Unix(),
		MessageCount: int64(fork.MessageCount),
	}
	if _, err := r.database.Queries.CreateSession(ctx, createParams); err != nil {
		return nil, fmt.Errorf("forking session: creating fork session: %w", err)
	}

	if err := r.database.Queries.SetSessionOrigin(ctx, sqlc.SetSessionOriginParams{
		OriginSessionID: &sourceSessionID,
		ID:              fork.ID,
	}); err != nil {
		return nil, fmt.Errorf("forking session: recording origin: %w", err)
	}

	// idRemap maps each source message ID to its freshly generated fork ID so
	// that ParentID references between copied messages point within the fork.
	idRemap := make(map[string]string, len(msgs))
	for _, m := range msgs {
		newID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("forking session: %w", err)
		}
		idRemap[m.ID] = newID
	}

	for _, m := range msgs {
		contentBytes, err := json.Marshal(m.Content)
		if err != nil {
			return nil, fmt.Errorf("forking session: marshalling message content: %w", err)
		}

		var parentID *string
		if m.ParentID != nil {
			if remapped, ok := idRemap[*m.ParentID]; ok {
				parentID = &remapped
			}
			// A parent outside the copied range is dropped (left nil) so the
			// fork never references a message it does not contain.
		}

		msgParams := sqlc.CreateMessageParams{
			ID:          idRemap[m.ID],
			SessionID:   fork.ID,
			Role:        string(m.Role),
			ContentJson: string(contentBytes),
			ParentID:    parentID,
			CreatedAt:   m.CreatedAt.UTC().Unix(),
		}
		if _, err := r.database.Queries.CreateMessage(ctx, msgParams); err != nil {
			return nil, fmt.Errorf("forking session: copying message: %w", err)
		}
	}

	return fork, nil
}

// OriginOf returns the ID of the session that id was forked from, or nil if
// id was created directly. Returns ErrNotFound if id does not exist.
func (r *Repo) OriginOf(ctx context.Context, id string) (*string, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, fmt.Errorf("getting origin: %w", err)
	}
	origin, err := r.database.Queries.GetSessionOrigin(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting origin of session %s: %w", id, err)
	}
	return origin, nil
}

// TitleFromFirstMessage extracts an at-most-60-char title from the
// first user message's text. Exposed for testability; callers do
// not normally invoke it (AppendMessage handles auto-titling).
func TitleFromFirstMessage(m message.Message) string {
	var text string
	// Find the first text block to use as the title source.
	for _, block := range m.Content {
		if textBlock, ok := block.(message.TextBlock); ok {
			text = textBlock.Text
			break
		}
		if textBlock, ok := block.(*message.TextBlock); ok {
			text = textBlock.Text
			break
		}
	}

	// Replace all newlines with spaces.
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)

	runes := []rune(text)
	if len(runes) <= 60 {
		if len(runes) == 0 {
			return "New session"
		}
		return text
	}

	// Look backwards for a space to truncate on a word boundary.
	truncateIdx := 60
	for i := 60; i >= 0; i-- {
		if runes[i] == ' ' {
			truncateIdx = i
			break
		}
	}

	// If we found a space, we truncate at that space.
	res := string(runes[:truncateIdx])
	res = strings.TrimSpace(res)
	// If we ended up with an empty string, just fallback to first 60 runes.
	if res == "" {
		return string(runes[:60])
	}
	return res
}

// newUUID generates a random UUID v4 using stdlib crypto/rand.
func newUUID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("reading random bytes for UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
