-- +goose Up
CREATE TABLE project_items (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    message_id  TEXT NOT NULL,
    item_kind   TEXT NOT NULL DEFAULT 'email',
    note        TEXT NOT NULL DEFAULT '',
    tagged_by   TEXT NOT NULL,
    tagged_at   DATETIME NOT NULL
);
CREATE INDEX project_items_project_idx ON project_items(project_id);
CREATE INDEX project_items_message_idx ON project_items(message_id);
CREATE UNIQUE INDEX project_items_uniq  ON project_items(project_id, message_id);

-- +goose Down
DROP TABLE project_items;
