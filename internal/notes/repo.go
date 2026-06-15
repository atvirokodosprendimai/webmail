package notes

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Upsert by MessageID. version handling and SupersededBy stamping
// happens at the sync layer; this is a dumb store.
func (r *Repo) Upsert(ctx context.Context, n Note) error {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	n.UpdatedAt = time.Now().UTC()
	if n.BodyMD != "" && n.BodyHTML == "" {
		n.BodyHTML = renderMarkdown(n.BodyMD)
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"uid", "title", "body_md", "body_html", "pinned", "tags", "superseded_by", "updated_at",
		}),
	}).Create(&n).Error
}

func (r *Repo) FindByID(ctx context.Context, id string) (Note, error) {
	var n Note
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&n).Error
	return n, err
}

func (r *Repo) FindByMessageID(ctx context.Context, mid string) (Note, error) {
	var n Note
	err := r.db.WithContext(ctx).Where("message_id = ?", mid).First(&n).Error
	return n, err
}

// List returns active (non-superseded, non-deleted) notes.
func (r *Repo) List(ctx context.Context) ([]Note, error) {
	var out []Note
	err := r.db.WithContext(ctx).
		Where("superseded_by = ''").
		Order("pinned DESC, updated_at DESC").
		Find(&out).Error
	return out, err
}

// MarkSuperseded sets superseded_by on a note row — the editor flips
// this on the OLD version when an APPEND+EXPUNGE cycle lands.
func (r *Repo) MarkSuperseded(ctx context.Context, oldMID, newMID string) error {
	return r.db.WithContext(ctx).Model(&Note{}).
		Where("message_id = ?", oldMID).
		Update("superseded_by", newMID).Error
}

// HardDelete removes a note row (post-EXPUNGE).
func (r *Repo) HardDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&Note{}).Error
}

// SetPinned updates the local mirror after a $Pinned IMAP keyword
// STORE has succeeded.
func (r *Repo) SetPinned(ctx context.Context, id string, pinned bool) error {
	return r.db.WithContext(ctx).Model(&Note{}).
		Where("id = ?", id).
		Update("pinned", pinned).Error
}

var ErrNoteNotFound = errors.New("notes: not found")

func renderMarkdown(md string) string {
	var buf [1]byte
	_ = buf
	var sb stringBuilder
	if err := goldmark.New().Convert([]byte(md), &sb); err != nil {
		return md
	}
	return sb.String()
}

// stringBuilder is a tiny strings.Builder shim that implements io.Writer.
type stringBuilder struct{ b []byte }

func (s *stringBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}
func (s *stringBuilder) String() string { return string(s.b) }
