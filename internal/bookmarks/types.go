// Package bookmarks lets users mark a thread for later attention.
// Per-user (user_id set) or team-shared (user_id empty) — same row
// shape, different scope. Same Message-ID-keyed join as projects so
// the bookmark survives server-side folder moves.
package bookmarks

import "time"

const (
	KindEmail = "email"
	KindNote  = "note"

	// Sentinel value for team-shared bookmarks. We use empty string
	// rather than NULL so the (user_id, message_id) uniq index has a
	// usable key for both scopes.
	SharedUserID = ""
)

type Bookmark struct {
	ID        string    `gorm:"primaryKey;column:id"`
	MessageID string    `gorm:"column:message_id;index"`
	ItemKind  string    `gorm:"column:item_kind"`
	UserID    string    `gorm:"column:user_id;index"`
	Note      string    `gorm:"column:note"`
	CreatedBy string    `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Bookmark) TableName() string { return "bookmarks" }
