-- +goose Up
CREATE TABLE mailbox_attachment (
    id                 TEXT PRIMARY KEY,
    ingest_id          TEXT NOT NULL,
    filename           TEXT NOT NULL DEFAULT '',
    mime               TEXT NOT NULL DEFAULT '',
    size_bytes         INTEGER NOT NULL DEFAULT 0,
    mime_part_id       TEXT NOT NULL DEFAULT '',
    transfer_encoding  TEXT NOT NULL DEFAULT '',
    content_id         TEXT NOT NULL DEFAULT '',
    inline             INTEGER NOT NULL DEFAULT 0,
    upload_sha256      TEXT NOT NULL DEFAULT '',
    upload_size        INTEGER NOT NULL DEFAULT 0,
    materialised_at    DATETIME,
    created_at         DATETIME NOT NULL
);
CREATE INDEX mailbox_attachment_ingest_idx ON mailbox_attachment(ingest_id);

-- +goose Down
DROP TABLE mailbox_attachment;
