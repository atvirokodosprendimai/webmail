package notes

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/atvirokodosprendimai/webmail/internal/mailbox"
)

// Sink adapts the notes repo to the mailbox.NotesSink interface so the
// poll worker can hand any message in IMAP_NOTES_FOLDER straight into
// the notes table — including notes created from other IMAP clients
// (Roundcube, Apple Mail, mutt, etc.).
type Sink struct {
	Repo *Repo
}

func NewSink(repo *Repo) *Sink { return &Sink{Repo: repo} }

// UpsertFromIMAP is called by the poll worker once per message seen
// in IMAP_NOTES_FOLDER. Title falls back to "(untitled)" when empty
// so the row is still listable.
func (s *Sink) UpsertFromIMAP(ctx context.Context, in mailbox.NoteUpsert) error {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "(untitled)"
	}
	bodyMD := in.BodyMD
	n := Note{
		MessageID:    in.MessageID,
		UID:          in.UID,
		Title:        title,
		BodyMD:       bodyMD,
		BodyHTML:     renderMarkdown(bodyMD),
		Pinned:       in.Pinned,
		Tags:         in.Tags,
		SupersededBy: "",
	}
	// Upsert respects the unique (message_id) index, preserves
	// CreatedAt via the gorm tag in Note struct, updates the mutable
	// columns.
	if err := s.Repo.Upsert(ctx, n); err != nil {
		return err
	}
	// If the message carries X-Webmail-Note-Original-MID, mark every
	// older note in the chain superseded by THIS message-id.
	if in.OriginalMID != "" && in.OriginalMID != in.MessageID {
		_ = s.Repo.MarkSuperseded(ctx, in.OriginalMID, in.MessageID)
	}
	return nil
}

// Avoid unused-import deadcode by referencing uuid + gorm so this file
// continues to compile if future helpers need them.
var _ = uuid.New
var _ = (*gorm.DB)(nil)
