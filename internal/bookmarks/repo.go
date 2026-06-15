package bookmarks

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Add is idempotent on (user_id, message_id). userID empty = team-shared.
func (r *Repo) Add(ctx context.Context, messageID, kind, userID, note, createdBy string) (Bookmark, error) {
	if messageID == "" {
		return Bookmark{}, errors.New("bookmarks: message_id required")
	}
	if kind == "" {
		kind = KindEmail
	}
	var existing Bookmark
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND message_id = ?", userID, messageID).
		First(&existing).Error
	if err == nil {
		// Already bookmarked — update note if provided.
		if note != "" && note != existing.Note {
			_ = r.db.WithContext(ctx).Model(&existing).Update("note", note).Error
			existing.Note = note
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Bookmark{}, err
	}
	b := Bookmark{
		ID:        uuid.NewString(),
		MessageID: messageID,
		ItemKind:  kind,
		UserID:    userID,
		Note:      note,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&b).Error; err != nil {
		return Bookmark{}, err
	}
	return b, nil
}

func (r *Repo) Remove(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&Bookmark{}).Error
}

func (r *Repo) ListPersonal(ctx context.Context, userID string) ([]Bookmark, error) {
	if userID == "" {
		return nil, errors.New("bookmarks: user_id required for personal list")
	}
	var out []Bookmark
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&out).Error
	return out, err
}

func (r *Repo) ListShared(ctx context.Context) ([]Bookmark, error) {
	var out []Bookmark
	err := r.db.WithContext(ctx).
		Where("user_id = ?", SharedUserID).
		Order("created_at DESC").
		Find(&out).Error
	return out, err
}

// FindForThread returns the (personal, shared) bookmark rows for any
// message in the thread. Caller passes the message_ids of every
// message in the thread; rows with matching message_id are returned.
func (r *Repo) FindForThread(ctx context.Context, userID string, messageIDs []string) (personal []Bookmark, shared []Bookmark, err error) {
	if len(messageIDs) == 0 {
		return nil, nil, nil
	}
	err = r.db.WithContext(ctx).
		Where("user_id = ? AND message_id IN ?", userID, messageIDs).
		Find(&personal).Error
	if err != nil {
		return nil, nil, err
	}
	err = r.db.WithContext(ctx).
		Where("user_id = ? AND message_id IN ?", SharedUserID, messageIDs).
		Find(&shared).Error
	if err != nil {
		return nil, nil, err
	}
	return personal, shared, nil
}
