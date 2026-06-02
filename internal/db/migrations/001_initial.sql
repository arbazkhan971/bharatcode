-- +goose Up
-- +goose StatementBegin

CREATE TABLE sessions (
    id            TEXT    PRIMARY KEY,
    project_path  TEXT    NOT NULL,
    title         TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    agent         TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX idx_sessions_project_path ON sessions (project_path);
CREATE INDEX idx_sessions_updated_at   ON sessions (updated_at DESC);

CREATE TABLE messages (
    id            TEXT    PRIMARY KEY,
    session_id    TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role          TEXT    NOT NULL CHECK (role IN ('system','user','assistant','tool')),
    content_json  TEXT    NOT NULL,
    parent_id     TEXT             REFERENCES messages(id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_messages_session_id ON messages (session_id);
CREATE INDEX idx_messages_parent_id  ON messages (parent_id);

CREATE TABLE file_changes (
    id            TEXT    PRIMARY KEY,
    session_id    TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    path          TEXT    NOT NULL,
    operation     TEXT    NOT NULL CHECK (operation IN ('create','update','delete','rename')),
    before_hash   TEXT,
    after_hash    TEXT,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_file_changes_session_id ON file_changes (session_id);
CREATE INDEX idx_file_changes_path       ON file_changes (path);

CREATE TABLE ledger_entries (
    id             TEXT    PRIMARY KEY,
    session_id     TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider       TEXT    NOT NULL,
    model          TEXT    NOT NULL,
    input_tokens   INTEGER NOT NULL CHECK (input_tokens >= 0),
    output_tokens  INTEGER NOT NULL CHECK (output_tokens >= 0),
    cost_usd       REAL    NOT NULL CHECK (cost_usd >= 0),
    cost_inr       REAL    NOT NULL CHECK (cost_inr >= 0),
    created_at     INTEGER NOT NULL
);

CREATE INDEX idx_ledger_entries_session_id ON ledger_entries (session_id);
CREATE INDEX idx_ledger_entries_created_at ON ledger_entries (created_at);

-- The config_kv table is not the primary config store — that is the JSON files
-- owned by internal/config/. config_kv exists for runtime override flags
-- written by bharatcode set ... commands.
CREATE TABLE config_kv (
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,
    scope  TEXT NOT NULL CHECK (scope IN ('global','project')),
    PRIMARY KEY (scope, key)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS config_kv;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS file_changes;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
-- +goose StatementEnd
