package notes

import (
	"context"
	"fmt"
	"strings"

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
	// Some IMAP clients APPEND without a Message-ID header. Fabricate
	// a stable synthetic ID anchored on UID so rows stay distinct.
	msgID := strings.TrimSpace(in.MessageID)
	if msgID == "" {
		msgID = fmt.Sprintf("<no-mid-uid-%d@orbital.local>", in.UID)
	}
	bodyMD := in.BodyMD
	n := Note{
		MessageID:    msgID,
		UID:          in.UID,
		Title:        title,
		BodyMD:       bodyMD,
		BodyHTML:     renderMarkdown(bodyMD),
		Pinned:       in.Pinned,
		Tags:         in.Tags,
		SupersededBy: "",
	}
	// UpsertContent — NOT Upsert. Poll must NEVER touch the
	// superseded_by column or it would wipe out handler-set chain
	// links every cycle (=> old versions reappear).
	if err := s.Repo.UpsertContent(ctx, n); err != nil {
		return err
	}
	if in.OriginalMID != "" && in.OriginalMID != msgID {
		_ = s.Repo.MarkSuperseded(ctx, in.OriginalMID, msgID)
	}
	return nil
}

// PurgeStale deletes local rows whose MID is not in keep. IMAP is the
// source of truth; anything missing from the server (EXPUNGE'd by us,
// Roundcube-deleted, etc.) gets cleaned up.
func (s *Sink) PurgeStale(ctx context.Context, keep []string) (int, error) {
	return s.Repo.DeleteByMessageIDsNotIn(ctx, keep)
}
