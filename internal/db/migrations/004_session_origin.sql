-- +goose Up
-- +goose StatementBegin
-- origin_session_id links a forked session back to the session it was
-- branched from. NULL for sessions created directly (not via Fork).
ALTER TABLE sessions ADD COLUMN origin_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL;
CREATE INDEX idx_sessions_origin_session_id ON sessions (origin_session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite cannot easily DROP COLUMN on older versions; down migrations are not
-- run in production, so we drop only the index here.
DROP INDEX IF EXISTS idx_sessions_origin_session_id;
-- +goose StatementEnd
