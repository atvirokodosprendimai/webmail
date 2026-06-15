package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// EnsureAccount inserts or updates the singleton mailbox_account row.
// Called once at boot from env config.
func (r *Repo) EnsureAccount(ctx context.Context, cfg AccountConfig) (string, error) {
	var existing Account
	err := r.db.WithContext(ctx).
		Where("host = ? AND username = ?", cfg.Host, cfg.Username).
		First(&existing).Error
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("mailbox: lookup account: %w", err)
	}
	a := Account{
		ID:        uuid.NewString(),
		Host:      cfg.Host,
		Port:      cfg.Port,
		Username:  cfg.Username,
		TLSMode:   cfg.TLSMode,
		CreatedAt: time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&a).Error; err != nil {
		return "", fmt.Errorf("mailbox: create account: %w", err)
	}
	return a.ID, nil
}

// UpsertFolder upserts a folder row and resets last_uid to 0 when the
// server's UIDVALIDITY no longer matches the persisted value (= the
// folder was renamed or recreated server-side; UIDs are unsafe to reuse).
func (r *Repo) UpsertFolder(ctx context.Context, accountID, name, role string, uidValidity uint32) (Folder, error) {
	var existing Folder
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND name = ?", accountID, name).
		First(&existing).Error
	now := time.Now().UTC()
	if err == nil {
		updates := map[string]any{
			"role":         role,
			"last_seen_at": now,
		}
		if existing.UIDValidity != uidValidity {
			updates["uid_validity"] = uidValidity
			updates["last_uid"] = 0
		}
		if err := r.db.WithContext(ctx).Model(&existing).Updates(updates).Error; err != nil {
			return Folder{}, fmt.Errorf("mailbox: update folder: %w", err)
		}
		existing.Role = role
		if existing.UIDValidity != uidValidity {
			existing.UIDValidity = uidValidity
			existing.LastUID = 0
		}
		existing.LastSeenAt = &now
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Folder{}, fmt.Errorf("mailbox: lookup folder: %w", err)
	}
	f := Folder{
		ID:          uuid.NewString(),
		AccountID:   accountID,
		Name:        name,
		Role:        role,
		UIDValidity: uidValidity,
		Enabled:     true,
		LastSeenAt:  &now,
	}
	if err := r.db.WithContext(ctx).Create(&f).Error; err != nil {
		return Folder{}, fmt.Errorf("mailbox: create folder: %w", err)
	}
	return f, nil
}

func (r *Repo) SetFolderLastUID(ctx context.Context, folderID string, uid uint32) error {
	return r.db.WithContext(ctx).
		Model(&Folder{}).
		Where("id = ?", folderID).
		Update("last_uid", uid).Error
}

func (r *Repo) ListFolders(ctx context.Context, accountID string) ([]Folder, error) {
	var out []Folder
	err := r.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("name ASC").
		Find(&out).Error
	return out, err
}

func (r *Repo) FindFolder(ctx context.Context, id string) (Folder, error) {
	var f Folder
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&f).Error
	return f, err
}

// IngestInsert is the input for InsertIngest. Separate from the Ingest
// row so caller code doesn't fiddle with id / fetched_at.
type IngestInsert struct {
	FolderID    string
	UID         uint32
	UIDValidity uint32
	MessageID   string
	InReplyTo   string
	References  string
	FromAddr    string
	FromName    string
	ToAddrs     string
	CcAddrs     string
	Subject     string
	BodyText    string
	BodyHTML    string
	Seen        bool
	Flagged     bool
	Answered    bool
	Draft       bool
	Deleted     bool
	ReceivedAt  time.Time
	Direction   string
}

// InsertIngest UPSERTs by MessageID. Returns (id, isNew) — isNew=false
// indicates a folder-move update (same Message-ID, new UID/folder).
func (r *Repo) InsertIngest(ctx context.Context, in IngestInsert) (string, bool, error) {
	if in.MessageID == "" {
		// Some misbehaving MTAs (or empty drafts) lack Message-ID; we
		// fabricate one anchored on UID+folder so dedup still works.
		in.MessageID = fmt.Sprintf("<no-mid-%s-%d@orbital.local>", in.FolderID, in.UID)
	}
	if in.Direction == "" {
		in.Direction = "in"
	}
	threadID := deriveThreadID(in.MessageID, in.InReplyTo, in.References)

	now := time.Now().UTC()
	row := Ingest{
		ID:          uuid.NewString(),
		FolderID:    in.FolderID,
		UID:         in.UID,
		UIDValidity: in.UIDValidity,
		MessageID:   in.MessageID,
		InReplyTo:   in.InReplyTo,
		References:  in.References,
		ThreadID:    threadID,
		Direction:   in.Direction,
		FromAddr:    in.FromAddr,
		FromName:    in.FromName,
		ToAddrs:     in.ToAddrs,
		CcAddrs:     in.CcAddrs,
		Subject:     in.Subject,
		BodyText:    in.BodyText,
		BodyHTML:    in.BodyHTML,
		Seen:        in.Seen,
		Flagged:     in.Flagged,
		Answered:    in.Answered,
		Draft:       in.Draft,
		Deleted:     in.Deleted,
		ReceivedAt:  in.ReceivedAt,
		FetchedAt:   now,
	}
	// First try insert; on UNIQUE conflict on message_id, update the
	// folder/uid/flag mirror in place (folder-move tracking).
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"folder_id", "uid", "uid_validity",
			"seen", "flagged", "answered", "draft", "deleted",
		}),
	}).Create(&row)
	if res.Error != nil {
		return "", false, fmt.Errorf("mailbox: insert ingest: %w", res.Error)
	}
	// gorm Clauses OnConflict returns RowsAffected=1 on insert AND on
	// update. To detect new-vs-update we re-query.
	var got Ingest
	if err := r.db.WithContext(ctx).Where("message_id = ?", in.MessageID).First(&got).Error; err != nil {
		return "", false, fmt.Errorf("mailbox: re-read ingest: %w", err)
	}
	isNew := got.ID == row.ID
	return got.ID, isNew, nil
}

// InsertAttachments writes the parsed attachment metadata for an ingest.
// Bytes NOT downloaded — only resolved via lazy materialise on tag.
func (r *Repo) InsertAttachments(ctx context.Context, ingestID string, parts []ParsedPart) error {
	if len(parts) == 0 {
		return nil
	}
	rows := make([]Attachment, 0, len(parts))
	now := time.Now().UTC()
	for _, p := range parts {
		rows = append(rows, Attachment{
			ID:               uuid.NewString(),
			IngestID:         ingestID,
			Filename:         p.Filename,
			MIME:             p.MIME,
			SizeBytes:        p.SizeBytes,
			MIMEPartID:       p.MIMEPartID,
			TransferEncoding: p.Encoding,
			ContentID:        p.ContentID,
			Inline:           p.Inline,
			CreatedAt:        now,
		})
	}
	if err := r.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("mailbox: insert attachments: %w", err)
	}
	return nil
}

// SetFlags writes the local mirror after a successful server STORE.
func (r *Repo) SetFlags(ctx context.Context, ingestID string, flags map[string]bool) error {
	updates := map[string]any{}
	for k, v := range flags {
		switch k {
		case "seen", "flagged", "answered", "draft", "deleted":
			updates[k] = v
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&Ingest{}).Where("id = ?", ingestID).Updates(updates).Error
}

// FindIngestByMessageID — used by the materialise path to look up the
// canonical row regardless of folder.
func (r *Repo) FindIngestByMessageID(ctx context.Context, messageID string) (Ingest, error) {
	var i Ingest
	err := r.db.WithContext(ctx).Where("message_id = ?", messageID).First(&i).Error
	return i, err
}

func (r *Repo) FindIngest(ctx context.Context, id string) (Ingest, error) {
	var i Ingest
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&i).Error
	return i, err
}

func (r *Repo) ThreadIngests(ctx context.Context, threadID string) ([]Ingest, error) {
	var out []Ingest
	err := r.db.WithContext(ctx).
		Where("thread_id = ?", threadID).
		Order("received_at ASC").
		Find(&out).Error
	return out, err
}

func (r *Repo) AttachmentsForIngest(ctx context.Context, ingestID string) ([]Attachment, error) {
	var out []Attachment
	err := r.db.WithContext(ctx).
		Where("ingest_id = ?", ingestID).
		Order("filename ASC").
		Find(&out).Error
	return out, err
}

func (r *Repo) UpdateAttachmentUpload(ctx context.Context, attID, sha256 string, size int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&Attachment{}).
		Where("id = ?", attID).
		Updates(map[string]any{
			"upload_sha256":   sha256,
			"upload_size":     size,
			"materialised_at": now,
		}).Error
}

// HardDeleteIngest removes the row entirely (post-EXPUNGE).
func (r *Repo) HardDeleteIngest(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&Ingest{}).Error
}

// UpdateFolderForIngest is used by Move to update the local mirror.
func (r *Repo) UpdateFolderForIngest(ctx context.Context, ingestID, newFolderID string) error {
	return r.db.WithContext(ctx).Model(&Ingest{}).
		Where("id = ?", ingestID).
		Update("folder_id", newFolderID).Error
}

// deriveThreadID assigns a stable thread bucket using the first
// reference (root of the conversation) when available; otherwise the
// message's own Message-ID. The poll loop calls this on insert.
func deriveThreadID(messageID, inReplyTo, references string) string {
	refs := strings.Fields(references)
	if len(refs) > 0 {
		return refs[0]
	}
	if strings.TrimSpace(inReplyTo) != "" {
		return strings.TrimSpace(inReplyTo)
	}
	return messageID
}
