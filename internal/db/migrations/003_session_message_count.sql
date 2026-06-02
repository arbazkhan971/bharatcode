-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN message_count INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite does not easily support DROP COLUMN in older versions, but we can do a no-op or drop.
-- For standard goose down:
-- ALTER TABLE sessions DROP COLUMN message_count;
-- Since we usually don't run down migrations in production, keeping it simple is best.
-- +goose StatementEnd
