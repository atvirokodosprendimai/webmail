-- +goose Up
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash BLOB NOT NULL,
    role          TEXT NOT NULL DEFAULT 'member',
    created_at    DATETIME NOT NULL
);

-- +goose Down
DROP TABLE users;
