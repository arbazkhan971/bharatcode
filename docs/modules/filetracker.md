# Filetracker

**Path:** `internal/filetracker/`
**Status:** Completed

## Purpose

The `filetracker` module records every file the agent touches during a session — when the agent reads a file, when it writes one, what the file looked like before and after — so that BharatCode can (a) render the "files changed in this session" diff view in the TUI, (b) summarize the session's filesystem footprint at end of run, and (c) detect external conflicts before a write clobbers a file the user modified while the agent was thinking. The conflict-detection path is the load-bearing one: tools like `edit` and `write` call `HasConflict(ctx, sessionID, path)` before they apply changes, and the permission layer escalates if the on-disk hash no longer matches the hash the tracker recorded at read time. The tracker also publishes every `Change` to a typed pubsub topic so the TUI's diff panel can re-render incrementally without polling the database. SHA-256 (lowercase hex) is the hash algorithm; nothing else is supported in Phase 1.

## Public interface

```go
// Operation classifies a filesystem mutation.
type Operation string

const (
    OpCreate Operation = "create"
    OpEdit   Operation = "edit"
    OpDelete Operation = "delete"
)

// Change is one recorded mutation of a file by an agent.
// BeforeHash and AfterHash are lowercase hex SHA-256 strings of the
// file contents. BeforeHash is empty for OpCreate, AfterHash is
// empty for OpDelete.
type Change struct {
    SessionID  string    `json:"session_id"`
    Path       string    `json:"path"`        // Absolute path on disk.
    Op         Operation `json:"op"`
    BeforeHash string    `json:"before_hash"`
    AfterHash  string    `json:"after_hash"`
    At         time.Time `json:"at"`
}

// Tracker is the public handle for file-change tracking. All methods
// take a context and return wrapped errors. Tracker is safe for
// concurrent use.
type Tracker struct {
    // Unexported: *db.DB, *db.Queries, *pubsub.Topic[Change],
    // and a small per-session in-memory map of last-read hashes.
}

// NewTracker constructs a Tracker. bus may be nil, in which case
// Changes are persisted but not published.
func NewTracker(database *db.DB, bus *pubsub.Topic[Change]) *Tracker

// RecordRead captures the current SHA-256 of path and stores it as
// the "last read" hash for (sessionID, path). The hash is the
// baseline HasConflict compares against on the next write. RecordRead
// is idempotent for the same (sessionID, path, hash) tuple.
//
// If path does not exist, RecordRead stores an empty hash (so a
// subsequent OpCreate has a sensible baseline) and returns nil.
func (t *Tracker) RecordRead(ctx context.Context, sessionID, path string) error

// RecordWrite persists a Change derived from oldContent and newContent
// and publishes it on the bus (if non-nil). The Operation is inferred:
//   - oldContent nil  + newContent non-nil -> OpCreate
//   - both non-nil                         -> OpEdit
//   - oldContent non-nil + newContent nil  -> OpDelete
// The persisted Change is returned so callers can correlate.
func (t *Tracker) RecordWrite(ctx context.Context, sessionID, path string, oldContent, newContent []byte) (Change, error)

// ChangesForSession returns every Change recorded for sessionID,
// oldest first.
func (t *Tracker) ChangesForSession(ctx context.Context, sessionID string) ([]Change, error)

// HasConflict returns true if the current SHA-256 of path differs
// from the hash captured by the most recent RecordRead for
// (sessionID, path). Returns false (and nil error) if no read was
// ever recorded for this path in this session — callers that require
// a prior read must check that explicitly.
//
// If path no longer exists but a non-empty read hash was recorded,
// HasConflict returns true.
func (t *Tracker) HasConflict(ctx context.Context, sessionID, path string) (bool, error)

// Sentinel errors.
var (
    ErrHashMismatch = errors.New("file hash mismatch")
)
```

The `pubsub.Topic[Change]` contract this module relies on is the standard typed-bus shape: `Publish(Change)` is non-blocking and best-effort; `Subscribe() <-chan Change` returns a buffered channel each subscriber owns. Filetracker only calls `Publish`; subscription is the consumer's problem.

## Dependencies

- `internal/util` — path normalization (`util.AbsPath`) and small hex helpers if any.
- `internal/db` — `*db.DB` handle and `*db.Queries` sqlc bindings for the `file_changes` and `file_reads` tables.
- `internal/pubsub` — `*pubsub.Topic[Change]` for publishing.

Per `docs/architecture.md`, filetracker is a Layer 2 (core data) module, parallel-safe with `message`. It is consumed by `internal/tools` (every read/edit/write tool plumbs through here) and indirectly by `internal/tui` (which subscribes to the bus).

## Acceptance criteria

- `TestNewTracker_NilBus_Allowed` — constructing with bus=nil does not panic.
- `TestRecordRead_StoresHash` — after `RecordRead` of a file with known contents, a follow-up `HasConflict` with unchanged contents returns false.
- `TestRecordRead_MissingFile_StoresEmptyHash` — `RecordRead` of a nonexistent path returns nil and stores an empty hash; later `OpCreate` write succeeds.
- `TestRecordRead_Idempotent` — calling `RecordRead` twice with the same on-disk contents stores a single read row (or overwrites in place); behavior is observably idempotent.
- `TestRecordWrite_InfersCreate` — oldContent=nil, newContent non-nil yields `Op == OpCreate`, `BeforeHash == ""`.
- `TestRecordWrite_InfersEdit` — both non-nil yields `Op == OpEdit` with both hashes populated.
- `TestRecordWrite_InfersDelete` — newContent=nil yields `Op == OpDelete`, `AfterHash == ""`.
- `TestRecordWrite_PublishesOnBus` — subscribing to the topic before the write yields exactly one `Change` on the channel.
- `TestRecordWrite_NilBus_NoPanic` — same scenario with bus=nil persists without panic and ChangesForSession finds the row.
- `TestChangesForSession_OldestFirst` — three writes return in append order.
- `TestChangesForSession_OnlyOwnSession` — writes in session B do not appear in session A's list.
- `TestHasConflict_ExternalEdit_BetweenReadAndWrite` — write a file to TempDir, `RecordRead`, mutate the file out-of-band, `HasConflict` returns true. **This is the core acceptance test.**
- `TestHasConflict_NoReadRecorded_ReturnsFalse` — never called `RecordRead`, `HasConflict` returns (false, nil).
- `TestHasConflict_FileDeletedExternally` — file removed from disk after a `RecordRead` that captured non-empty hash; `HasConflict` returns true.
- `TestHasConflict_UnchangedFile` — `RecordRead` then immediate `HasConflict` returns false.
- `TestConcurrentRecordWrite_NoDataRace` — 16 goroutines writing different paths in the same session; `go test -race` passes.
- All tests use `t.TempDir()` for the test file root and `db.Open(t.TempDir())` for the SQLite handle. No test reads or writes outside `t.TempDir()`.

`go test -race ./internal/filetracker/...` must pass.

## Notes for the implementer

- Hashing is `crypto/sha256` from stdlib, lowercase hex via `hex.EncodeToString(h.Sum(nil))`. Do not use any third-party hashing library. The empty hash for missing-file / OpDelete is the empty string `""`, not the sha256 of empty input.
- The SQLite schema rows (defined in `internal/db/migrations/`) are approximately:

  ```sql
  CREATE TABLE file_reads (
      session_id TEXT NOT NULL,
      path       TEXT NOT NULL,
      hash       TEXT NOT NULL,        -- "" if file did not exist at read time.
      at         INTEGER NOT NULL,     -- Unix seconds.
      PRIMARY KEY (session_id, path),
      FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
  );

  CREATE TABLE file_changes (
      id          INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id  TEXT NOT NULL,
      path        TEXT NOT NULL,
      op          TEXT NOT NULL,       -- create|edit|delete
      before_hash TEXT NOT NULL,
      after_hash  TEXT NOT NULL,
      at          INTEGER NOT NULL,
      FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
  );
  CREATE INDEX idx_file_changes_session ON file_changes(session_id, at);
  ```

  Coordinate the migration with the `db` module owner; do not write `CREATE TABLE` SQL inside this package.

- Path normalization: store absolute, `filepath.Clean`-ed paths. Reject paths that cannot be made absolute with a wrapped error (`fmt.Errorf("normalizing path %q: %w", p, err)`). Symlink resolution is the caller's problem — we record what we are asked about.
- Use sqlc-generated queries through `*db.Queries`. If a needed query is missing, add it to `internal/db/queries/filetracker.sql` and regenerate; do not embed raw SQL.
- `pubsub.Topic[Change].Publish` must be non-blocking — if the bus has a slow subscriber, dropping is acceptable (this is observability, not durability). The `db` row is the source of truth.
- Conflict semantics edge case: if `RecordRead` captured an empty hash (file missing at read time) and the file still does not exist at conflict-check time, `HasConflict` returns false. If the file now exists (someone created it externally), `HasConflict` returns true.
- Use `log/slog` for diagnostics. Capitalized messages, no trailing period: `slog.Debug("Recorded write", "session_id", sid, "path", p, "op", op)`.
- Errors are wrapped: `fmt.Errorf("recording write for %s: %w", path, err)`. Define and export `ErrHashMismatch` for any future API that wants to return a typed conflict; the current `HasConflict` returns `(bool, error)` and does not use the sentinel, but tools layered above may.
- `context.Context` is the first parameter on every method; pass it through to every `db.Queries` and any file-IO that supports it.
- Tests: `testify/require`, `t.TempDir()`, table-driven for the `RecordWrite` operation-inference cases. No network, no real filesystem outside `t.TempDir()`.
- Run `gofumpt -w .` and `golangci-lint run` before declaring the module done. Append an `## Implementation status` section to this file listing what was built and any deliberate deviations.

## Implementation status

- **Status:** Completed
- **Files created/modified:**
  - `internal/filetracker/filetracker.go`
  - `internal/filetracker/filetracker_test.go`
- **Total lines of code:** ~1000 lines (including tests)
- **Test Pass Count:** 25 passing test cases (20 tests/subtests plus 5 additional coverage tests).
- **Statement Coverage:** 96.5% statement coverage for `filetracker.go`.
- **Deviations:**
  - In `hashFile`, removed the early `IsDir` check to let `os.ReadFile` naturally trigger `EISDIR`, which provides full test coverage on read errors.
  - In `HasConflict` and `RecordWrite`, added early check for directories to return a directory error when no read was previously recorded or when writing.
  - Injected a package-local UUID random reader in `TestRecordWrite_UUIDError` to test UUID generation failure without mutating global `crypto/rand` state.
  - Handled test permission errors under `root` environment via path prefixes that are regular files (`ENOTDIR`).
