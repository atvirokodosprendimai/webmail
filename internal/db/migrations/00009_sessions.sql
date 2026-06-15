-- +goose Up
CREATE TABLE sessions (
    token   TEXT PRIMARY KEY,
    data    BLOB NOT NULL,
    expiry  DATETIME NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expiry);

-- +goose Down
DROP TABLE sessions;
