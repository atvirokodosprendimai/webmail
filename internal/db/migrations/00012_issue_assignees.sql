-- +goose Up
CREATE TABLE issue_assignees (
    issue_id TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX issue_assignees_user_idx ON issue_assignees(user_id);

-- +goose Down
DROP TABLE issue_assignees;
