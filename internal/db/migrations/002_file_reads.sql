-- +goose Up
-- +goose StatementBegin
CREATE TABLE file_reads (
    session_id TEXT NOT NULL,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,        -- "" if file did not exist at read time.
    created_at INTEGER NOT NULL,     -- Unix seconds.
    PRIMARY KEY (session_id, path),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS file_reads;
-- +goose StatementEnd
