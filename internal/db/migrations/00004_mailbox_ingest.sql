-- +goose Up
CREATE TABLE mailbox_ingest (
    id            TEXT PRIMARY KEY,
    folder_id     TEXT NOT NULL,
    uid           INTEGER NOT NULL,
    uid_validity  INTEGER NOT NULL,
    message_id    TEXT NOT NULL UNIQUE,
    in_reply_to   TEXT NOT NULL DEFAULT '',
    refs          TEXT NOT NULL DEFAULT '',
    thread_id     TEXT NOT NULL DEFAULT '',
    direction     TEXT NOT NULL DEFAULT 'in',
    from_addr     TEXT NOT NULL DEFAULT '',
    from_name     TEXT NOT NULL DEFAULT '',
    to_addrs      TEXT NOT NULL DEFAULT '',
    cc_addrs      TEXT NOT NULL DEFAULT '',
    subject       TEXT NOT NULL DEFAULT '',
    body_text     TEXT NOT NULL DEFAULT '',
    body_html     TEXT NOT NULL DEFAULT '',
    seen          INTEGER NOT NULL DEFAULT 0,
    flagged       INTEGER NOT NULL DEFAULT 0,
    answered      INTEGER NOT NULL DEFAULT 0,
    draft         INTEGER NOT NULL DEFAULT 0,
    deleted       INTEGER NOT NULL DEFAULT 0,
    received_at   DATETIME NOT NULL,
    fetched_at    DATETIME NOT NULL
);
CREATE INDEX mailbox_ingest_folder_idx     ON mailbox_ingest(folder_id);
CREATE INDEX mailbox_ingest_thread_idx     ON mailbox_ingest(thread_id);
CREATE INDEX mailbox_ingest_received_idx   ON mailbox_ingest(received_at DESC);
CREATE INDEX mailbox_ingest_from_idx       ON mailbox_ingest(from_addr);
CREATE INDEX mailbox_ingest_seen_idx       ON mailbox_ingest(seen);
CREATE INDEX mailbox_ingest_flagged_idx    ON mailbox_ingest(flagged);
CREATE INDEX mailbox_ingest_inreplyto_idx  ON mailbox_ingest(in_reply_to);

-- +goose Down
DROP TABLE mailbox_ingest;
