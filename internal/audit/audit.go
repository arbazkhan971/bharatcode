// Package audit provides an append-only, tamper-evident SQLite audit log.
//
// Every significant event in BharatCode — an LLM call, a tool invocation, a
// file write, or a permission decision — can be recorded as one immutable row.
// Rows form a hash chain: each record's hash covers the previous record's hash,
// so any later edit, reordering, or deletion of a record breaks verification.
// The table is further protected by SQLite triggers that abort UPDATE and
// DELETE, making the log append-only at the storage layer as well.
//
// This is the storage half of BharatCode's sovereignty "proof" layer: it lets a
// user demonstrate exactly what the agent did on their machine, and prove the
// record was not altered after the fact.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"

	_ "modernc.org/sqlite"
)

// Event types recorded in the audit log. Callers should use these constants so
// the log stays queryable by a stable vocabulary.
const (
	// TypeLLM marks a completion request sent to a model provider.
	TypeLLM = "llm"
	// TypeTool marks an agent tool invocation.
	TypeTool = "tool"
	// TypeFileWrite marks a write to a file on disk.
	TypeFileWrite = "file_write"
	// TypePermission marks a permission decision.
	TypePermission = "permission"
)

// genesisHash seeds the hash chain for the very first record so that even a
// single-row log has a well-defined predecessor.
const genesisHash = "genesis"

// Event is one thing worth recording. Detail is optional structured context; it
// is stored as canonical JSON and contributes to the record hash.
type Event struct {
	// Type is one of the Type* constants (free-form strings are permitted but
	// discouraged).
	Type string
	// Actor identifies who/what triggered the event: a session ID, provider
	// name, or tool name. May be empty.
	Actor string
	// Summary is a short human-readable description.
	Summary string
	// Detail is optional structured context. Map keys are serialized in a stable
	// (sorted) order by encoding/json, keeping the record hash deterministic.
	Detail map[string]any
}

// Record is a persisted audit entry as read back from the log.
type Record struct {
	Seq       int64           `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Actor     string          `json:"actor,omitempty"`
	Summary   string          `json:"summary"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	PrevHash  string          `json:"prev_hash"`
	Hash      string          `json:"hash"`
}

// Store is an append-only audit log backed by SQLite.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu       sync.Mutex // serializes Append so the hash chain stays consistent
	lastHash string
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	ts         TEXT NOT NULL,
	event_type TEXT NOT NULL,
	actor      TEXT NOT NULL,
	summary    TEXT NOT NULL,
	detail     TEXT NOT NULL,
	prev_hash  TEXT NOT NULL,
	hash       TEXT NOT NULL
);

CREATE TRIGGER IF NOT EXISTS events_no_update
BEFORE UPDATE ON events
BEGIN
	SELECT RAISE(ABORT, 'audit log is append-only');
END;

CREATE TRIGGER IF NOT EXISTS events_no_delete
BEFORE DELETE ON events
BEGIN
	SELECT RAISE(ABORT, 'audit log is append-only');
END;
`

// DefaultPath returns the canonical audit log path, co-located with the main
// database under the user's data directory.
func DefaultPath() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		dataHome = "."
	}
	return filepath.Join(util.ExpandPath(dataHome), "bharatcode", "audit.db")
}

// Open opens (or creates) the audit log at path, applies the schema, and primes
// the in-memory chain head from the last persisted record. The parent directory
// is created with 0o755 if it does not exist.
func Open(ctx context.Context, path string) (*Store, error) {
	expanded := util.ExpandPath(path)
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return nil, fmt.Errorf("resolving audit path %q: %w", path, err)
	}
	if err := fsext.EnsureDir(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("ensuring audit directory: %w", err)
	}

	conn, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("opening audit db: %w", err)
	}
	// One connection keeps the hash chain serialized at the driver level in
	// addition to the Store mutex.
	conn.SetMaxOpenConns(1)
	if _, err := conn.ExecContext(ctx, schema); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("applying audit schema: %w", err)
	}

	s := &Store{db: conn, now: time.Now, lastHash: genesisHash}
	if err := s.loadHead(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// loadHead reads the hash of the most recent record so the next Append chains
// onto it.
func (s *Store) loadHead(ctx context.Context) error {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&hash)
	switch {
	case err == sql.ErrNoRows:
		s.lastHash = genesisHash
		return nil
	case err != nil:
		return fmt.Errorf("loading audit head: %w", err)
	default:
		s.lastHash = hash
		return nil
	}
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// computeHash derives the chained hash for a record. The field order and the
// newline separators are part of the on-disk contract: changing them
// invalidates every previously stored hash, so keep them stable.
func computeHash(prevHash string, ts, eventType, actor, summary, detail string) string {
	h := sha256.New()
	for _, field := range []string{prevHash, ts, eventType, actor, summary, detail} {
		h.Write([]byte(field))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalDetail renders Detail to a stable JSON string. A nil/empty map
// becomes "{}" so the hashed input is never empty.
func canonicalDetail(detail map[string]any) (string, error) {
	if len(detail) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("encoding audit detail: %w", err)
	}
	return string(b), nil
}

// Append records ev as the next immutable entry and returns the stored record.
// It is safe for concurrent use.
func (s *Store) Append(ctx context.Context, ev Event) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, fmt.Errorf("audit: store is not open")
	}

	detail, err := canonicalDetail(ev.Detail)
	if err != nil {
		return Record{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ts := s.now().UTC().Format(time.RFC3339Nano)
	prev := s.lastHash
	hash := computeHash(prev, ts, ev.Type, ev.Actor, ev.Summary, detail)

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO events (ts, event_type, actor, summary, detail, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts, ev.Type, ev.Actor, ev.Summary, detail, prev, hash,
	)
	if err != nil {
		return Record{}, fmt.Errorf("appending audit record: %w", err)
	}
	seq, _ := res.LastInsertId()
	s.lastHash = hash

	parsed, _ := time.Parse(time.RFC3339Nano, ts)
	return Record{
		Seq:       seq,
		Timestamp: parsed,
		Type:      ev.Type,
		Actor:     ev.Actor,
		Summary:   ev.Summary,
		Detail:    json.RawMessage(detail),
		PrevHash:  prev,
		Hash:      hash,
	}, nil
}

// Records returns every audit entry in insertion order.
func (s *Store) Records(ctx context.Context) ([]Record, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("audit: store is not open")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, ts, event_type, actor, summary, detail, prev_hash, hash
		 FROM events ORDER BY seq ASC`)
	if err != nil {
		return nil, fmt.Errorf("querying audit records: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var (
			rec    Record
			ts     string
			detail string
		)
		if err := rows.Scan(&rec.Seq, &ts, &rec.Type, &rec.Actor, &rec.Summary, &detail, &rec.PrevHash, &rec.Hash); err != nil {
			return nil, fmt.Errorf("scanning audit record: %w", err)
		}
		rec.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		rec.Detail = json.RawMessage(detail)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audit records: %w", err)
	}
	return out, nil
}

// Export writes every record to w as JSON Lines (one record per line), oldest
// first. The output is suitable for archival or piping into other tooling.
func (s *Store) Export(ctx context.Context, w io.Writer) error {
	records, err := s.Records(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("encoding audit record %d: %w", rec.Seq, err)
		}
	}
	return nil
}

// Verify walks the hash chain from the genesis seed and confirms every record's
// stored hash matches a freshly computed one and links to its predecessor. It
// returns the number of records verified and a non-nil error at the first
// inconsistency, which indicates tampering or corruption.
func (s *Store) Verify(ctx context.Context) (int, error) {
	records, err := s.Records(ctx)
	if err != nil {
		return 0, err
	}
	prev := genesisHash
	for i, rec := range records {
		if rec.PrevHash != prev {
			return i, fmt.Errorf("audit record %d: prev_hash mismatch (chain broken)", rec.Seq)
		}
		ts := rec.Timestamp.UTC().Format(time.RFC3339Nano)
		detail := string(rec.Detail)
		if detail == "" {
			detail = "{}"
		}
		want := computeHash(prev, ts, rec.Type, rec.Actor, rec.Summary, detail)
		if want != rec.Hash {
			return i, fmt.Errorf("audit record %d: hash mismatch (record altered)", rec.Seq)
		}
		prev = rec.Hash
	}
	return len(records), nil
}
