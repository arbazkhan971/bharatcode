-- name: CreateSession :one
INSERT INTO sessions (
    id, project_path, title, model, agent, created_at, updated_at, message_count
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions
WHERE id = ?;

-- name: ListSessions :many
SELECT * FROM sessions
ORDER BY updated_at DESC;

-- name: UpdateSession :one
UPDATE sessions
SET project_path = ?,
    title = ?,
    model = ?,
    agent = ?,
    updated_at = ?,
    message_count = ?
WHERE id = ?
RETURNING *;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: SetSessionOrigin :exec
UPDATE sessions
SET origin_session_id = ?
WHERE id = ?;

-- name: GetSessionOrigin :one
SELECT origin_session_id FROM sessions
WHERE id = ?;

-- name: CreateMessage :one
INSERT INTO messages (
    id, session_id, role, content_json, parent_id, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: ListMessagesBySession :many
-- The secondary sort on rowid gives a stable total order when several rows
-- share the same created_at second, matching ListMessagesBySessionPaged so
-- that paged traversal reproduces this full ordering exactly.
SELECT * FROM messages
WHERE session_id = ?
ORDER BY created_at ASC, rowid ASC;

-- name: ListMessagesBySessionPaged :many
-- Returns a single window of a session's messages, oldest first. The
-- secondary sort on rowid gives a stable total order when several rows
-- share the same created_at second, so successive (limit, offset) windows
-- neither overlap nor skip rows.
SELECT * FROM messages
WHERE session_id = ?
ORDER BY created_at ASC, rowid ASC
LIMIT ? OFFSET ?;

-- name: CountMessagesBySession :one
SELECT COUNT(*) FROM messages
WHERE session_id = ?;

-- name: RecordFileChange :one
INSERT INTO file_changes (
    id, session_id, path, operation, before_hash, after_hash, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: ListFileChangesBySession :many
SELECT * FROM file_changes
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: AppendLedgerEntry :one
INSERT INTO ledger_entries (
    id, session_id, provider, model, input_tokens, output_tokens, cost_usd, cost_inr, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: SumLedgerCostByPeriod :one
SELECT
    COALESCE(SUM(cost_usd), 0.0) AS total_usd,
    COALESCE(SUM(cost_inr), 0.0) AS total_inr
FROM ledger_entries
WHERE created_at >= ? AND created_at <= ?;

-- name: UpsertConfigKV :exec
INSERT INTO config_kv (
    key, value, scope
) VALUES (
    ?, ?, ?
)
ON CONFLICT (scope, key) DO UPDATE
SET value = excluded.value;

-- name: GetConfigKV :one
SELECT * FROM config_kv
WHERE scope = ? AND key = ?;

-- name: RecordFileRead :exec
INSERT INTO file_reads (
    session_id, path, hash, created_at
) VALUES (
    ?, ?, ?, ?
)
ON CONFLICT (session_id, path) DO UPDATE
SET hash = excluded.hash,
    created_at = excluded.created_at;

-- name: GetFileRead :one
SELECT * FROM file_reads
WHERE session_id = ? AND path = ?;

-- name: SumLedgerCostBySession :one
SELECT
    COALESCE(SUM(cost_usd), 0.0) AS total_usd,
    COALESCE(SUM(cost_inr), 0.0) AS total_inr,
    COALESCE(SUM(input_tokens), 0) AS total_input,
    COALESCE(SUM(output_tokens), 0) AS total_output,
    COUNT(*) AS call_count
FROM ledger_entries
WHERE session_id = ?;

-- name: SumLedgerCostAndTokensByPeriod :one
SELECT
    COALESCE(SUM(cost_usd), 0.0) AS total_usd,
    COALESCE(SUM(cost_inr), 0.0) AS total_inr,
    COALESCE(SUM(input_tokens), 0) AS total_input,
    COALESCE(SUM(output_tokens), 0) AS total_output,
    COUNT(*) AS call_count
FROM ledger_entries
WHERE created_at >= ? AND created_at <= ?;

-- name: GetLatestSessionByProjectPath :one
SELECT * FROM sessions
WHERE project_path = ?
ORDER BY updated_at DESC, created_at DESC, id DESC
LIMIT 1;

-- name: ListSessionsFiltered :many
SELECT * FROM sessions
WHERE (CAST(? AS TEXT) = '' OR project_path = CAST(? AS TEXT))
  AND (updated_at >= CAST(? AS INTEGER))
ORDER BY updated_at DESC, created_at DESC, id DESC
LIMIT CASE WHEN CAST(? AS INTEGER) <= 0 THEN -1 ELSE CAST(? AS INTEGER) END;

