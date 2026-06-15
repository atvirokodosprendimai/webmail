-- +goose Up
CREATE TABLE issues (
    id         TEXT PRIMARY KEY,
    number     INTEGER NOT NULL UNIQUE,
    message_id TEXT NOT NULL,
    title      TEXT NOT NULL,
    notes_md   TEXT NOT NULL DEFAULT '',
    notes_html TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'open',
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    closed_at  DATETIME
);
CREATE UNIQUE INDEX issues_msg_idx     ON issues(message_id);
CREATE INDEX issues_status_idx          ON issues(status);
CREATE INDEX issues_updated_idx         ON issues(updated_at DESC);

-- +goose Down
DROP TABLE issues;
