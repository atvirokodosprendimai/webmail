-- +goose Up
CREATE TABLE bookmarks (
    id          TEXT PRIMARY KEY,
    message_id  TEXT NOT NULL,
    item_kind   TEXT NOT NULL DEFAULT 'email',
    user_id     TEXT NOT NULL DEFAULT '',  -- '' means team-shared
    note        TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL,
    created_at  DATETIME NOT NULL
);
CREATE UNIQUE INDEX bookmarks_uniq_idx ON bookmarks(user_id, message_id);
CREATE INDEX bookmarks_user_idx ON bookmarks(user_id);
CREATE INDEX bookmarks_message_idx ON bookmarks(message_id);

-- +goose Down
DROP TABLE bookmarks;
