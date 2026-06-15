-- +goose Up
CREATE TABLE mailbox_account (
    id            TEXT PRIMARY KEY,
    host          TEXT NOT NULL,
    port          INTEGER NOT NULL,
    username      TEXT NOT NULL,
    tls_mode      TEXT NOT NULL,
    last_poll_at  DATETIME,
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL
);

-- +goose Down
DROP TABLE mailbox_account;
