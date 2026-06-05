// Package filetracker tracks file reads and writes per session.
package filetracker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// Operation classifies a filesystem mutation.
type Operation string

const (
	// OpCreate indicates that a file was created.
	OpCreate Operation = "create"
	// OpEdit indicates that an existing file was edited.
	OpEdit Operation = "edit"
	// OpDelete indicates that a file was deleted.
	OpDelete Operation = "delete"
)

// Change is one recorded mutation of a file by an agent.
// BeforeHash and AfterHash are lowercase hex SHA-256 strings of the
// file contents. BeforeHash is empty for OpCreate, AfterHash is
// empty for OpDelete.
type Change struct {
	SessionID  string    `json:"session_id"`
	Path       string    `json:"path"` // Absolute path on disk.
	Op         Operation `json:"op"`
	BeforeHash string    `json:"before_hash"`
	AfterHash  string    `json:"after_hash"`
	At         time.Time `json:"at"`
}

// Tracker is the public handle for file-change tracking. All methods
// take a context and return wrapped errors. Tracker is safe for
// concurrent use.
type Tracker struct {
	database *db.DB
	queries  *sqlc.Queries
	bus      *pubsub.Topic[Change]

	mu            sync.RWMutex
	lastReadCache map[string]map[string]string // sessionID -> path -> hash
}

// NewTracker constructs a Tracker. bus may be nil, in which case
// Changes are persisted but not published.
func NewTracker(database *db.DB, bus *pubsub.Topic[Change]) *Tracker {
	return &Tracker{
		database:      database,
		queries:       database.Queries,
		bus:           bus,
		lastReadCache: make(map[string]map[string]string),
	}
}

// Sentinel errors.
var (
	// ErrHashMismatch is returned when a file's hash does not match.
	ErrHashMismatch = errors.New("file hash mismatch")
)

var (
	uuidRandomReaderMu sync.Mutex
	uuidRandomReader   io.Reader = rand.Reader
)

// normalizePath converts path to absolute and cleans it.
func normalizePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("normalizing path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

// hashFile returns the lowercase hex SHA-256 hash of a file if it exists.
// If the file does not exist, it returns ("", false, nil).
// If the file is a directory or has other read issues, it returns an error.
func hashFile(path string) (string, bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stating file %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("reading file %s: %w", path, err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), true, nil
}

// newUUID generates a random UUID v4 using stdlib crypto/rand.
func newUUID() (string, error) {
	b := make([]byte, 16)
	uuidRandomReaderMu.Lock()
	defer uuidRandomReaderMu.Unlock()
	_, err := io.ReadFull(uuidRandomReader, b)
	if err != nil {
		return "", fmt.Errorf("reading random bytes for UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func setUUIDRandomReader(reader io.Reader) func() {
	uuidRandomReaderMu.Lock()
	oldReader := uuidRandomReader
	uuidRandomReader = reader
	uuidRandomReaderMu.Unlock()

	return func() {
		uuidRandomReaderMu.Lock()
		uuidRandomReader = oldReader
		uuidRandomReaderMu.Unlock()
	}
}

// RecordRead captures the current SHA-256 of path and stores it as
// the "last read" hash for (sessionID, path). The hash is the
// baseline HasConflict compares against on the next write. RecordRead
// is idempotent for the same (sessionID, path, hash) tuple.
//
// If path does not exist, RecordRead stores an empty hash (so a
// subsequent OpCreate has a sensible baseline) and returns nil.
func (t *Tracker) RecordRead(ctx context.Context, sessionID, path string) error {
	normPath, err := normalizePath(path)
	if err != nil {
		return err
	}

	hashVal, exists, err := hashFile(normPath)
	if err != nil {
		return fmt.Errorf("recording read for %s: %w", normPath, err)
	}

	var hashToRecord string
	if exists {
		hashToRecord = hashVal
	}

	// Persist to DB.
	nowMs := time.Now().UnixMilli()
	params := sqlc.RecordFileReadParams{
		SessionID: sessionID,
		Path:      normPath,
		Hash:      hashToRecord,
		CreatedAt: nowMs,
	}

	if err := t.queries.RecordFileRead(ctx, params); err != nil {
		return fmt.Errorf("recording read for %s: %w", normPath, err)
	}

	// Update in-memory cache.
	t.mu.Lock()
	if t.lastReadCache == nil {
		t.lastReadCache = make(map[string]map[string]string)
	}
	if _, ok := t.lastReadCache[sessionID]; !ok {
		t.lastReadCache[sessionID] = make(map[string]string)
	}
	t.lastReadCache[sessionID][normPath] = hashToRecord
	t.mu.Unlock()

	return nil
}

// RecordWrite persists a Change derived from oldContent and newContent
// and publishes it on the bus (if non-nil). The Operation is inferred:
//   - oldContent nil  + newContent non-nil -> OpCreate
//   - both non-nil                         -> OpEdit
//   - oldContent non-nil + newContent nil  -> OpDelete
//
// The persisted Change is returned so callers can correlate.
func (t *Tracker) RecordWrite(ctx context.Context, sessionID, path string, oldContent, newContent []byte) (Change, error) {
	normPath, err := normalizePath(path)
	if err != nil {
		return Change{}, err
	}

	info, statErr := os.Stat(normPath)
	if statErr == nil && info.IsDir() {
		return Change{}, fmt.Errorf("recording write for %s: path is a directory", normPath)
	}

	var op Operation
	var beforeHash, afterHash string

	if oldContent == nil && newContent != nil {
		op = OpCreate
		h := sha256.Sum256(newContent)
		afterHash = hex.EncodeToString(h[:])
	} else if oldContent != nil && newContent != nil {
		op = OpEdit
		hBefore := sha256.Sum256(oldContent)
		beforeHash = hex.EncodeToString(hBefore[:])
		hAfter := sha256.Sum256(newContent)
		afterHash = hex.EncodeToString(hAfter[:])
	} else if oldContent != nil && newContent == nil {
		op = OpDelete
		h := sha256.Sum256(oldContent)
		beforeHash = hex.EncodeToString(h[:])
	} else {
		return Change{}, fmt.Errorf("recording write for %s: both oldContent and newContent cannot be nil", normPath)
	}

	// Map Operation to DB schema.
	var dbOp string
	switch op {
	case OpCreate:
		dbOp = "create"
	case OpEdit:
		dbOp = "update"
	case OpDelete:
		dbOp = "delete"
	}

	id, err := newUUID()
	if err != nil {
		return Change{}, fmt.Errorf("recording write for %s: %w", normPath, err)
	}

	nowMs := time.Now().UnixMilli()
	params := sqlc.RecordFileChangeParams{
		ID:        id,
		SessionID: sessionID,
		Path:      normPath,
		Operation: dbOp,
		CreatedAt: nowMs,
	}

	if beforeHash != "" {
		params.BeforeHash = &beforeHash
	}
	if afterHash != "" {
		params.AfterHash = &afterHash
	}

	fc, err := t.queries.RecordFileChange(ctx, params)
	if err != nil {
		return Change{}, fmt.Errorf("recording write for %s: %w", normPath, err)
	}

	slog.Debug("Recorded write", "session_id", sessionID, "path", normPath, "op", op)

	// Refresh the session's read baseline to the just-written content. After a
	// write the session's "last known" state of the file is what it wrote, so a
	// subsequent edit must compare against that rather than the pre-write read
	// hash. Without this, a legitimate view→edit→edit (or write→edit) sequence
	// would trip the stale-read guard on the second mutation even though no
	// external process touched the file. A genuine out-of-band modification
	// after the write still differs from afterHash and is correctly flagged.
	// Best-effort: the change is already committed, so a refresh failure only
	// risks a spurious (recoverable) conflict on the next edit, never data loss.
	if err := t.refreshReadBaseline(ctx, sessionID, normPath, afterHash); err != nil {
		slog.Warn("Refreshing read baseline after write failed", "session_id", sessionID, "path", normPath, "err", err)
	}

	change := Change{
		SessionID:  fc.SessionID,
		Path:       fc.Path,
		Op:         op,
		BeforeHash: beforeHash,
		AfterHash:  afterHash,
		At:         time.UnixMilli(fc.CreatedAt),
	}

	if t.bus != nil {
		t.bus.Publish(ctx, change)
	}

	return change, nil
}

// ChangesForSession returns every Change recorded for sessionID,
// oldest first.
func (t *Tracker) ChangesForSession(ctx context.Context, sessionID string) ([]Change, error) {
	fcs, err := t.queries.ListFileChangesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing changes for session %s: %w", sessionID, err)
	}

	changes := make([]Change, len(fcs))
	for i, fc := range fcs {
		var op Operation
		switch fc.Operation {
		case "create":
			op = OpCreate
		case "update":
			op = OpEdit
		case "delete":
			op = OpDelete
		default:
			op = Operation(fc.Operation)
		}

		var beforeHash, afterHash string
		if fc.BeforeHash != nil {
			beforeHash = *fc.BeforeHash
		}
		if fc.AfterHash != nil {
			afterHash = *fc.AfterHash
		}

		changes[i] = Change{
			SessionID:  fc.SessionID,
			Path:       fc.Path,
			Op:         op,
			BeforeHash: beforeHash,
			AfterHash:  afterHash,
			At:         time.UnixMilli(fc.CreatedAt),
		}
	}

	return changes, nil
}

// ChangedFiles returns the sorted, deduplicated set of absolute file
// paths created or edited by sessionID. A path is included if it has
// at least one OpCreate or OpEdit Change recorded for the session,
// regardless of how many times it was written. OpDelete operations do
// not, on their own, add a path: a path that was only ever deleted is
// excluded, but a path that was created (or edited) and later deleted
// remains in the set because it was created/edited during the session.
//
// The result is suitable for a /diff or status view; an empty session
// yields an empty (non-nil) slice.
func (t *Tracker) ChangedFiles(ctx context.Context, sessionID string) ([]string, error) {
	changes, err := t.ChangesForSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("collecting changed files for session %s: %w", sessionID, err)
	}

	seen := make(map[string]struct{}, len(changes))
	for _, c := range changes {
		if c.Op == OpCreate || c.Op == OpEdit {
			seen[c.Path] = struct{}{}
		}
	}

	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	return paths, nil
}

// HasConflict returns true if the current SHA-256 of path differs
// from the hash captured by the most recent RecordRead for
// (sessionID, path). Returns false (and nil error) if no read was
// ever recorded for this path in this session — callers that require
// a prior read must check that explicitly.
//
// If path no longer exists but a non-empty read hash was recorded,
// HasConflict returns true.
func (t *Tracker) HasConflict(ctx context.Context, sessionID, path string) (bool, error) {
	normPath, err := normalizePath(path)
	if err != nil {
		return false, err
	}

	info, statErr := os.Stat(normPath)
	if statErr == nil && info.IsDir() {
		return false, fmt.Errorf("checking conflict for %s: path is a directory", normPath)
	}

	// Check in-memory cache first.
	t.mu.RLock()
	var recordedHash string
	var found bool
	if t.lastReadCache != nil {
		if sessionMap, ok := t.lastReadCache[sessionID]; ok {
			recordedHash, found = sessionMap[normPath]
		}
	}
	t.mu.RUnlock()

	if !found {
		// Fall back to database.
		read, err := t.queries.GetFileRead(ctx, sqlc.GetFileReadParams{
			SessionID: sessionID,
			Path:      normPath,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, fmt.Errorf("checking conflict for %s: %w", normPath, err)
		}
		recordedHash = read.Hash

		// Populate cache.
		t.mu.Lock()
		if t.lastReadCache == nil {
			t.lastReadCache = make(map[string]map[string]string)
		}
		if _, ok := t.lastReadCache[sessionID]; !ok {
			t.lastReadCache[sessionID] = make(map[string]string)
		}
		t.lastReadCache[sessionID][normPath] = recordedHash
		t.mu.Unlock()
	}

	currentHash, exists, err := hashFile(normPath)
	if err != nil {
		return false, fmt.Errorf("checking conflict for %s: %w", normPath, err)
	}

	if !exists {
		if recordedHash != "" {
			return true, nil
		}
		return false, nil
	}

	return currentHash != recordedHash, nil
}

// HasRead reports whether a read has been recorded for (sessionID, path) in
// this session — i.e. whether the file was viewed (or last written) before an
// edit is attempted. Unlike the in-memory view tracker used by the write tool,
// HasRead is backed by the durable read log, so it stays correct across process
// restarts and resumed sessions. It returns false (nil error) when no read was
// ever recorded. Callers use it to enforce the read-before-edit invariant.
func (t *Tracker) HasRead(ctx context.Context, sessionID, path string) (bool, error) {
	normPath, err := normalizePath(path)
	if err != nil {
		return false, err
	}

	t.mu.RLock()
	if t.lastReadCache != nil {
		if sessionMap, ok := t.lastReadCache[sessionID]; ok {
			if _, found := sessionMap[normPath]; found {
				t.mu.RUnlock()
				return true, nil
			}
		}
	}
	t.mu.RUnlock()

	if _, err := t.queries.GetFileRead(ctx, sqlc.GetFileReadParams{
		SessionID: sessionID,
		Path:      normPath,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("checking read for %s: %w", normPath, err)
	}
	return true, nil
}

// refreshReadBaseline records hash as the session's latest known content for
// normPath, updating both the durable read log and the in-memory cache. It is
// the shared write-side counterpart of RecordRead, called after a successful
// write so HasConflict and HasRead reflect the just-written state.
func (t *Tracker) refreshReadBaseline(ctx context.Context, sessionID, normPath, hash string) error {
	if err := t.queries.RecordFileRead(ctx, sqlc.RecordFileReadParams{
		SessionID: sessionID,
		Path:      normPath,
		Hash:      hash,
		CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("recording read baseline for %s: %w", normPath, err)
	}

	t.mu.Lock()
	if t.lastReadCache == nil {
		t.lastReadCache = make(map[string]map[string]string)
	}
	if _, ok := t.lastReadCache[sessionID]; !ok {
		t.lastReadCache[sessionID] = make(map[string]string)
	}
	t.lastReadCache[sessionID][normPath] = hash
	t.mu.Unlock()
	return nil
}
