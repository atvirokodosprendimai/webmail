// Package issues implements the lightweight issue tracker layered on
// top of email threads. Each issue is a pointer to a thread (joined by
// RFC 2822 Message-ID) plus sidecar metadata: title, status, assignees,
// and internal markdown notes.
package issues

import "time"

const (
	StatusOpen   = "open"
	StatusClosed = "closed"
)

type Issue struct {
	ID         string     `gorm:"primaryKey;column:id"`
	Number     int        `gorm:"column:number;uniqueIndex"`
	MessageID  string     `gorm:"column:message_id;uniqueIndex"`
	Title      string     `gorm:"column:title"`
	NotesMD    string     `gorm:"column:notes_md"`
	NotesHTML  string     `gorm:"column:notes_html"`
	Status     string     `gorm:"column:status;index"`
	CreatedBy  string     `gorm:"column:created_by"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;index"`
	ClosedAt   *time.Time `gorm:"column:closed_at"`
}

func (Issue) TableName() string { return "issues" }

type Assignee struct {
	IssueID string `gorm:"primaryKey;column:issue_id"`
	UserID  string `gorm:"primaryKey;column:user_id;index"`
}

func (Assignee) TableName() string { return "issue_assignees" }

// Counter is the singleton row that mints monotonic issue numbers.
type Counter struct {
	ID     int `gorm:"primaryKey;column:id"`
	NextID int `gorm:"column:next_id"`
}

func (Counter) TableName() string { return "issue_counter" }
