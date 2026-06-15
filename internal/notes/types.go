// Package notes is the Apple-Notes-style markdown notepad backed by
// the IMAP_NOTES_FOLDER. Each note = one RFC 2822 message APPEND'd.
// X-Webmail-Note: v1 header identifies a note vs a regular email.
package notes

import "time"

const (
	HeaderNote        = "X-Webmail-Note"
	HeaderNoteVersion = "X-Webmail-Note-Version"
	HeaderNoteOrigMID = "X-Webmail-Note-Original-MID"

	KeywordPinned = "$Pinned"
	KeywordTagPfx = "$note_"
	NoteVersionV1 = "v1"

	// ContentTypeMD is the Content-Type we set on note messages.
	// Despite being markdown, we send text/plain so third-party IMAP
	// clients (Roundcube, Apple Mail, K-9) render the body inline
	// instead of treating it as a download-only attachment. The
	// content is still markdown; our webmail renders it via goldmark
	// from the cached BodyHTML.
	ContentTypeMD = "text/plain; charset=utf-8"
)

type Note struct {
	ID           string    `gorm:"primaryKey;column:id"`
	MessageID    string    `gorm:"column:message_id;uniqueIndex"`
	UID          uint32    `gorm:"column:uid"`
	AuthorID     string    `gorm:"column:author_id;index"`
	Title        string    `gorm:"column:title"`
	BodyMD       string    `gorm:"column:body_md"`
	BodyHTML     string    `gorm:"column:body_html"`
	Pinned       bool      `gorm:"column:pinned;index"`
	Tags         string    `gorm:"column:tags"`
	SupersededBy string    `gorm:"column:superseded_by"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (Note) TableName() string { return "notes" }
