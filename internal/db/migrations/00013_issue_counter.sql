-- +goose Up
CREATE TABLE issue_counter (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    next_id INTEGER NOT NULL
);
INSERT INTO issue_counter (id, next_id) VALUES (1, 1);

-- +goose Down
DROP TABLE issue_counter;
