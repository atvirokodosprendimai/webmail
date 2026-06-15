// Package projects manages collaboration buckets — each project tags
// emails (and notes) by RFC 2822 Message-ID so server-side folder
// moves never break the link.
package projects

import "time"

const (
	KindEmail = "email"
	KindNote  = "note"
)

type Project struct {
	ID          string    `gorm:"primaryKey;column:id"`
	Slug        string    `gorm:"uniqueIndex;column:slug"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	CreatedBy   string    `gorm:"column:created_by"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (Project) TableName() string { return "projects" }

type Item struct {
	ID        string    `gorm:"primaryKey;column:id"`
	ProjectID string    `gorm:"column:project_id;index"`
	MessageID string    `gorm:"column:message_id;index"`
	ItemKind  string    `gorm:"column:item_kind"`
	Note      string    `gorm:"column:note"`
	TaggedBy  string    `gorm:"column:tagged_by"`
	TaggedAt  time.Time `gorm:"column:tagged_at"`
}

func (Item) TableName() string { return "project_items" }
