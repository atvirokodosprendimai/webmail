package issues

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"gorm.io/gorm"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// CreateFromThread mints a new issue rooted at the given thread MID.
// Idempotent: if an issue already exists for messageID, returns it
// unchanged. Number is allocated atomically via issue_counter.
func (r *Repo) CreateFromThread(ctx context.Context, messageID, title, createdBy string) (Issue, bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return Issue{}, false, errors.New("issues: empty message_id")
	}
	// Idempotency check: pre-existing issue → return it.
	var existing Issue
	err := r.db.WithContext(ctx).Where("message_id = ?", messageID).First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Issue{}, false, err
	}
	// Transaction: mint a number from the counter, insert the row.
	var created Issue
	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c Counter
		if err := tx.Where("id = ?", 1).First(&c).Error; err != nil {
			return err
		}
		num := c.NextID
		if err := tx.Model(&Counter{}).Where("id = ?", 1).
			Update("next_id", num+1).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		issue := Issue{
			ID:        uuid.NewString(),
			Number:    num,
			MessageID: messageID,
			Title:     strings.TrimSpace(title),
			Status:    StatusOpen,
			CreatedBy: createdBy,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if issue.Title == "" {
			issue.Title = "(untitled)"
		}
		if err := tx.Create(&issue).Error; err != nil {
			return err
		}
		created = issue
		return nil
	})
	if txErr != nil {
		return Issue{}, false, txErr
	}
	return created, true, nil
}

// SetStatus toggles open/closed and stamps closed_at when closing.
func (r *Repo) SetStatus(ctx context.Context, id, status string) error {
	if status != StatusOpen && status != StatusClosed {
		return errors.New("issues: bad status")
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status":     status,
		"updated_at": now,
	}
	if status == StatusClosed {
		updates["closed_at"] = now
	} else {
		updates["closed_at"] = nil
	}
	return r.db.WithContext(ctx).Model(&Issue{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *Repo) SetTitle(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("issues: title required")
	}
	return r.db.WithContext(ctx).Model(&Issue{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"title":      title,
			"updated_at": time.Now().UTC(),
		}).Error
}

// SetNotes saves markdown notes + cached rendered HTML.
func (r *Repo) SetNotes(ctx context.Context, id, notesMD string) error {
	return r.db.WithContext(ctx).Model(&Issue{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"notes_md":   notesMD,
			"notes_html": renderMarkdown(notesMD),
			"updated_at": time.Now().UTC(),
		}).Error
}

func (r *Repo) FindByID(ctx context.Context, id string) (Issue, error) {
	var i Issue
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&i).Error
	return i, err
}

func (r *Repo) FindByMessageID(ctx context.Context, mid string) (Issue, error) {
	var i Issue
	err := r.db.WithContext(ctx).Where("message_id = ?", mid).First(&i).Error
	return i, err
}

// ListOptions narrows the index query.
type ListOptions struct {
	Status       string // "" = any, "open", "closed"
	AssignedTo   string // user_id; "" = any
	Search       string // LIKE on title + notes_md
	Limit        int
}

func (r *Repo) List(ctx context.Context, opts ListOptions) ([]Issue, error) {
	q := r.db.WithContext(ctx).Model(&Issue{})
	if opts.Status != "" {
		q = q.Where("status = ?", opts.Status)
	}
	if opts.AssignedTo != "" {
		q = q.Where("id IN (SELECT issue_id FROM issue_assignees WHERE user_id = ?)", opts.AssignedTo)
	}
	if strings.TrimSpace(opts.Search) != "" {
		like := "%" + strings.ToLower(opts.Search) + "%"
		q = q.Where("LOWER(title) LIKE ? OR LOWER(notes_md) LIKE ?", like, like)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	var out []Issue
	if err := q.Order("updated_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) CountOpen(ctx context.Context) (int, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Issue{}).
		Where("status = ?", StatusOpen).
		Count(&n).Error
	return int(n), err
}

// Assignees

func (r *Repo) Assign(ctx context.Context, issueID, userID string) error {
	return r.db.WithContext(ctx).Clauses().Create(&Assignee{
		IssueID: issueID,
		UserID:  userID,
	}).Error
}

func (r *Repo) Unassign(ctx context.Context, issueID, userID string) error {
	return r.db.WithContext(ctx).
		Where("issue_id = ? AND user_id = ?", issueID, userID).
		Delete(&Assignee{}).Error
}

func (r *Repo) AssigneesFor(ctx context.Context, issueID string) ([]string, error) {
	var out []Assignee
	if err := r.db.WithContext(ctx).Where("issue_id = ?", issueID).Find(&out).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, a := range out {
		ids = append(ids, a.UserID)
	}
	return ids, nil
}

func renderMarkdown(md string) string {
	var sb strings.Builder
	if err := goldmark.New().Convert([]byte(md), &sb); err != nil {
		return md
	}
	return sb.String()
}
