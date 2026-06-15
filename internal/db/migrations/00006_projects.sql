-- +goose Up
CREATE TABLE projects (
    id           TEXT PRIMARY KEY,
    slug         TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);

-- +goose Down
DROP TABLE projects;
