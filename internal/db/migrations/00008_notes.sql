-- +goose Up
CREATE TABLE notes (
    id             TEXT PRIMARY KEY,
    message_id     TEXT NOT NULL UNIQUE,
    uid            INTEGER NOT NULL DEFAULT 0,
    author_id      TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    body_md        TEXT NOT NULL DEFAULT '',
    body_html      TEXT NOT NULL DEFAULT '',
    pinned         INTEGER NOT NULL DEFAULT 0,
    tags           TEXT NOT NULL DEFAULT '',
    superseded_by  TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);
CREATE INDEX notes_pinned_idx     ON notes(pinned);
CREATE INDEX notes_updated_idx    ON notes(updated_at DESC);
CREATE INDEX notes_author_idx     ON notes(author_id);

-- +goose Down
DROP TABLE notes;
