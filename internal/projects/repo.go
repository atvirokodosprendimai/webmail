package projects

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func makeSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project-" + uuid.NewString()[:8]
	}
	return s
}

func (r *Repo) Create(ctx context.Context, name, description, createdBy string) (Project, error) {
	if strings.TrimSpace(name) == "" {
		return Project{}, errors.New("projects: name required")
	}
	slug := makeSlug(name)
	// Suffix collisions: try -2, -3, …
	base := slug
	for n := 2; n < 100; n++ {
		var existing Project
		err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return Project{}, err
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	p := Project{
		ID:          uuid.NewString(),
		Slug:        slug,
		Name:        name,
		Description: description,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&p).Error; err != nil {
		return Project{}, err
	}
	return p, nil
}

func (r *Repo) List(ctx context.Context) ([]Project, error) {
	var out []Project
	err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error
	return out, err
}

func (r *Repo) FindBySlug(ctx context.Context, slug string) (Project, error) {
	var p Project
	err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&p).Error
	return p, err
}

func (r *Repo) Tag(ctx context.Context, projectID, messageID, kind, taggedBy string) (Item, error) {
	// Idempotent on (project_id, message_id) — uniq index in schema.
	var existing Item
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND message_id = ?", projectID, messageID).
		First(&existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Item{}, err
	}
	it := Item{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		MessageID: messageID,
		ItemKind:  kind,
		TaggedBy:  taggedBy,
		TaggedAt:  time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&it).Error; err != nil {
		return Item{}, err
	}
	return it, nil
}

func (r *Repo) Untag(ctx context.Context, projectID, messageID string) error {
	return r.db.WithContext(ctx).
		Where("project_id = ? AND message_id = ?", projectID, messageID).
		Delete(&Item{}).Error
}

func (r *Repo) ItemsForProject(ctx context.Context, projectID string) ([]Item, error) {
	var out []Item
	err := r.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("tagged_at DESC").
		Find(&out).Error
	return out, err
}

func (r *Repo) ProjectsForMessage(ctx context.Context, messageID string) ([]Project, error) {
	var out []Project
	err := r.db.WithContext(ctx).
		Table("projects").
		Joins("INNER JOIN project_items ON project_items.project_id = projects.id").
		Where("project_items.message_id = ?", messageID).
		Find(&out).Error
	return out, err
}
