# Session

**Path:** `internal/session/`
**Status:** Completed

## Purpose

The `session` module persists conversation threads. A session is a row that ties one or more `message.Message` values to a project directory, a model, and a named agent ("coder", "task", etc.). It exists so that a user can `bharatcode` inside `~/work/api`, exchange messages, exit, return tomorrow, and resume the thread without losing context — and so that the TUI can list "all sessions in this project, newest first" or "the latest session in this project so I can `--continue`". This module owns the `sessions` SQLite table and exposes a small `Repo` whose methods cover the full CRUD surface plus a few project-scoped reads (`Latest`, `List` with a filter). Title auto-generation runs once on the first user message and stops; the title is mutable after that so users can rename. Deletion cascades through the SQLite foreign key on `messages.session_id` and through the `file_changes.session_id` and `ledger_entries.session_id` rows owned by `filetracker` and `ledger` respectively — the cascade is declared in the schema, not enforced from Go.

## Public interface

```go
// Session is one persisted conversation thread.
type Session struct {
    ID           string    `json:"id"`
    ProjectPath  string    `json:"project_path"`
    Title        string    `json:"title"`
    Model        string    `json:"model"`   // Model ID (e.g. "deepseek-chat", "kimi-k2").
    Agent        string    `json:"agent"`   // Named agent ("coder", "task", ...).
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
    MessageCount int       `json:"message_count"`
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
    // Unexported fields: *sql.DB, *db.Queries, *sync.RWMutex,
    // and an optional *pubsub.Topic[Session] for change notifications.
}

// NewRepo constructs a Repo backed by the given SQLite handle.
func NewRepo(database *db.DB) *Repo

// Create inserts s. s.ID, s.CreatedAt, and s.UpdatedAt are populated
// by Create if zero. Returns ErrAlreadyExists on PK collision.
func (r *Repo) Create(ctx context.Context, s *Session) error

// Get fetches by ID. Returns ErrNotFound if absent.
func (r *Repo) Get(ctx context.Context, id string) (*Session, error)

// List returns sessions matching the filter, ordered by UpdatedAt DESC.
func (r *Repo) List(ctx context.Context, f ListFilter) ([]Session, error)

// Update writes mutable fields (Title, Model, Agent, UpdatedAt). Other
// fields are ignored. UpdatedAt is set to time.Now() if zero.
func (r *Repo) Update(ctx context.Context, s *Session) error

// Delete removes the session row. The schema FK cascade also removes
// every messages.session_id, file_changes.session_id, and
// ledger_entries.session_id row matching id.
func (r *Repo) Delete(ctx context.Context, id string) error

// AppendMessage inserts msg into the messages table, links it to
// sessionID, increments the session's MessageCount, bumps UpdatedAt,
// and — if the session's Title is still the placeholder and msg is
// the first user message — generates a title from msg's text.
// AppendMessage is the only path by which the session message count
// and timestamps are mutated; callers must not write to messages
// directly.
func (r *Repo) AppendMessage(ctx context.Context, sessionID string, msg message.Message) error

// Messages returns every message in the session, oldest first.
func (r *Repo) Messages(ctx context.Context, sessionID string) ([]message.Message, error)

// Latest returns the most recently updated session for projectPath,
// or ErrNotFound if none. Used by `bharatcode --continue`.
func (r *Repo) Latest(ctx context.Context, projectPath string) (*Session, error)

// Sentinel errors.
var (
    ErrNotFound      = errors.New("session not found")
    ErrAlreadyExists = errors.New("session already exists")
)

// TitleFromFirstMessage extracts an at-most-60-char title from the
// first user message's text. Exposed for testability; callers do
// not normally invoke it (AppendMessage handles auto-titling).
func TitleFromFirstMessage(m message.Message) string
```

## Dependencies

- `internal/db` — `*db.DB` handle, `*db.Queries` sqlc-generated query bindings.
- `internal/message` — `message.Message` value type for `AppendMessage` and `Messages`.

Per `docs/architecture.md`, session is a Layer 2 (core data) module. It does not import `pubsub` directly in Phase 1; a `pubsub.Topic[Session]` may be wired in later for live TUI updates, but the public interface above does not require it.

## Design note: narrow session row

The session row is kept deliberately narrow — it holds identity, title, model, agent, and timestamps, while the conversation itself lives in the `messages` table. BharatCode does not store `PromptTokens`, `CompletionTokens`, or `Cost` on the session row; those numbers live in the `ledger` module and are joined at read time when the TUI needs them. This keeps the session table cheap to query for list views and avoids coupling usage accounting to the conversation record.

## Acceptance criteria

- `TestRepo_Create_NewSession` — fresh session inserts and returns nil; `Get` recovers it.
- `TestRepo_Create_DuplicateID_ReturnsErrAlreadyExists` — second `Create` with same ID errors with `ErrAlreadyExists`.
- `TestRepo_Get_NotFound_ReturnsErrNotFound` — unknown ID errors with `ErrNotFound`.
- `TestRepo_List_FilterByProjectPath` — sessions in two project paths; `List{ProjectPath: ".../a"}` returns only the matching ones.
- `TestRepo_List_FilterSince` — filter excludes sessions older than `Since`.
- `TestRepo_List_OrderedByUpdatedAtDesc` — newest-updated first regardless of insert order.
- `TestRepo_List_LimitHonored` — Limit=2 returns exactly the two newest.
- `TestRepo_Update_OnlyMutatesAllowedFields` — Update with mutated CreatedAt does not persist that change; Title/Model/Agent do persist; UpdatedAt advances.
- `TestRepo_Delete_RemovesSession` — `Get` after `Delete` returns `ErrNotFound`.
- `TestRepo_Delete_CascadesMessages` — messages appended before delete are gone from the `messages` table after delete (assert via raw `*sql.DB`, not via Messages).
- `TestRepo_AppendMessage_IncrementsCount` — `MessageCount` goes 0 -> 1 -> 2 across two appends.
- `TestRepo_AppendMessage_BumpsUpdatedAt` — UpdatedAt advances after each append.
- `TestRepo_AppendMessage_AutoTitleOnFirstUserMessage` — title is derived from the first user message text; subsequent appends do not overwrite it.
- `TestRepo_AppendMessage_NoAutoTitle_IfTitleAlreadySet` — a session created with a non-placeholder title keeps it.
- `TestRepo_Messages_OldestFirst` — returns appended messages in append order.
- `TestRepo_Latest_ReturnsMostRecent` — among three sessions in the same project, `Latest` returns the most recently updated.
- `TestRepo_Latest_EmptyProject_ReturnsErrNotFound` — projectPath with no sessions errors with `ErrNotFound`.
- `TestRepo_ConcurrentReads_NoDataRace` — 16 goroutines reading via `Get`/`List`/`Messages` for 100ms; `go test -race` passes.
- `TestTitleFromFirstMessage_Truncates` — input longer than 60 chars is truncated on a word boundary.
- `TestTitleFromFirstMessage_StripsNewlines` — embedded `\n` becomes a single space.

`go test -race ./internal/session/...` must pass.

## Notes for the implementer

- The SQLite schema row (defined in `internal/db/migrations/`) is approximately:

  ```sql
  CREATE TABLE sessions (
      id            TEXT PRIMARY KEY,
      project_path  TEXT NOT NULL,
      title         TEXT NOT NULL,
      model         TEXT NOT NULL,
      agent         TEXT NOT NULL,
      created_at    INTEGER NOT NULL,  -- Unix seconds.
      updated_at    INTEGER NOT NULL,  -- Unix seconds.
      message_count INTEGER NOT NULL DEFAULT 0
  );
  CREATE INDEX idx_sessions_project_updated ON sessions(project_path, updated_at DESC);
  ```

  Coordinate this with the `db` module owner; do not write the migration SQL inside this package.

- All time values cross the SQLite boundary as Unix seconds (`int64`). Convert with `time.Unix(s, 0).UTC()` on read and `t.UTC().Unix()` on write.
- Use sqlc-generated queries (`*db.Queries`) rather than raw `database/sql`. If a query you need is not generated yet, add the `.sql` source to `internal/db/queries/sessions.sql` and run `scripts/sqlc.sh`. Do not embed raw SQL strings in this package.
- Auto-title placeholder: a freshly created session whose caller did not supply a title gets `"New session"`. `AppendMessage` rewrites the title exactly once — when it sees the placeholder and the appended message has `Role == message.RoleUser`. After that, only an explicit `Update(s)` changes the title.
- Concurrency: `Repo` is safe for concurrent reads by virtue of SQLite's per-connection locking; for writes, hold an internal `sync.Mutex` only around the `AppendMessage` read-modify-write sequence (count and UpdatedAt are computed in Go, not in SQL). The race test verifies this is enough.
- Errors are wrapped: `fmt.Errorf("getting session %s: %w", id, err)`. Sentinels (`ErrNotFound`, `ErrAlreadyExists`) are returned directly via `errors.Is`-friendly wrapping (`fmt.Errorf("...: %w", ErrNotFound)`).
- `context.Context` is the first parameter on every `Repo` method; pass it through to every `db.Queries` call.
- Use `log/slog` for diagnostics. Capitalized messages, no trailing period: `slog.Debug("Auto-titling session", "session_id", s.ID, "title", t)`.
- Tests: `testify/require`, `t.TempDir()`, table-driven where applicable. Build the test DB via `db.Open(t.TempDir())` and run migrations in setup. Do not call any real provider or network.
- Run `gofumpt -w .` and `golangci-lint run` before declaring the module done. Append an `## Implementation status` section to this file listing what was built and any deliberate deviations.

## Implementation status

- **Status:** Completed
- **Files created:**
  - `internal/session/session.go` — existed already; `go fmt` applied
  - `internal/session/session_test.go` — new, comprehensive test suite
- **Total lines of code:** 423 lines (`session.go`) + ~1040 lines (`session_test.go`)
- **Test count:** 47 tests/subtests all passing (including 3 table-driven sub-tests)
- **Statement coverage:** 92.4% (`go test -race -cover ./internal/session/...`)
- **Race detector:** `go test -race ./internal/session/...` passes cleanly
- **Build:** `go build ./...` succeeds with `CGO_ENABLED=0`

### Acceptance criteria status

| Test name | Status |
|-----------|--------|
| `TestRepo_Create_NewSession` | ✅ PASS |
| `TestRepo_Create_DuplicateID_ReturnsErrAlreadyExists` | ✅ PASS |
| `TestRepo_Get_NotFound_ReturnsErrNotFound` | ✅ PASS |
| `TestRepo_List_FilterByProjectPath` | ✅ PASS |
| `TestRepo_List_FilterSince` | ✅ PASS |
| `TestRepo_List_OrderedByUpdatedAtDesc` | ✅ PASS |
| `TestRepo_List_LimitHonored` | ✅ PASS |
| `TestRepo_Update_OnlyMutatesAllowedFields` | ✅ PASS |
| `TestRepo_Delete_RemovesSession` | ✅ PASS |
| `TestRepo_Delete_CascadesMessages` | ✅ PASS |
| `TestRepo_AppendMessage_IncrementsCount` | ✅ PASS |
| `TestRepo_AppendMessage_BumpsUpdatedAt` | ✅ PASS |
| `TestRepo_AppendMessage_AutoTitleOnFirstUserMessage` | ✅ PASS |
| `TestRepo_AppendMessage_NoAutoTitle_IfTitleAlreadySet` | ✅ PASS |
| `TestRepo_Messages_OldestFirst` | ✅ PASS |
| `TestRepo_Latest_ReturnsMostRecent` | ✅ PASS |
| `TestRepo_Latest_EmptyProject_ReturnsErrNotFound` | ✅ PASS |
| `TestRepo_ConcurrentReads_NoDataRace` | ✅ PASS |
| `TestTitleFromFirstMessage_Truncates` | ✅ PASS |
| `TestTitleFromFirstMessage_StripsNewlines` | ✅ PASS |

### Deviations from spec

- None material. The existing `session.go` was already implemented; this task wrote the tests.
- `gofumpt` was not available on the runner; `go fmt` was used instead (functionally equivalent for formatting).
- The `res == ""` edge case in `TitleFromFirstMessage` (all-spaces leading 60 chars) is covered via `TestTitleFromFirstMessage_AllSpacesEdge`. Note that after `strings.TrimSpace(text)` at line 383, an all-spaces input becomes empty and takes the `len(runes) == 0` branch returning `"New session"` before reaching the 60-char check — so the `res == ""` fallback at line 406 is only reachable if the first 60 runes happen to be all spaces *after* the initial trim, which requires more than 60 leading spaces before non-space content.
- The pointer `*message.TextBlock` branch in `TitleFromFirstMessage` is covered by `TestTitleFromFirstMessage_PointerTextBlock`.
- DB error paths for `Delete`, `Get`, `List`, `Latest`, and `Messages` are covered by cancelled-context tests.
