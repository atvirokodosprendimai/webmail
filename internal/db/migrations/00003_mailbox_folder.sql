-- +goose Up
CREATE TABLE mailbox_folder (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL,
    name          TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT '',
    uid_validity  INTEGER NOT NULL DEFAULT 0,
    last_uid      INTEGER NOT NULL DEFAULT 0,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_seen_at  DATETIME,
    last_error    TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX mailbox_folder_account_name_uniq ON mailbox_folder(account_id, name);

-- +goose Down
DROP TABLE mailbox_folder;
