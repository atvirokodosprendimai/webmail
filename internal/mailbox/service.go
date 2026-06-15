package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/emersion/go-imap/v2"
)

// Service orchestrates foreground IMAP mutations + local mirror updates.
// Each call opens a short-lived session (dial → SELECT → STORE/MOVE →
// close). v1 trades reconnection overhead for simplicity; v1.1 can pool.
type Service struct {
	Cfg  AccountConfig
	Repo *Repo
	Bus  *Bus
	Log  *slog.Logger
}

// MarkSeen toggles \Seen for one ingest row + IMAP message. Local mirror
// updated only on server ack.
func (s *Service) MarkSeen(ctx context.Context, ingestID string, seen bool) error {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return err
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectFolder(folder.Name); err != nil {
		return err
	}
	if err := c.markSeen(ing.UID, seen); err != nil {
		return err
	}
	if err := s.Repo.SetFlags(ctx, ingestID, map[string]bool{"seen": seen}); err != nil {
		return err
	}
	s.Bus.Broadcast()
	return nil
}

func (s *Service) MarkFlagged(ctx context.Context, ingestID string, flagged bool) error {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return err
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectFolder(folder.Name); err != nil {
		return err
	}
	if err := c.markFlagged(ing.UID, flagged); err != nil {
		return err
	}
	if err := s.Repo.SetFlags(ctx, ingestID, map[string]bool{"flagged": flagged}); err != nil {
		return err
	}
	s.Bus.Broadcast()
	return nil
}

// OpenThreadFetch reads the body for the current ingest using BODY[…]
// (marks \Seen). Used by the thread-open handler — the read MAY mutate.
// Returns the freshly-fetched (textBody, htmlBody).
func (s *Service) OpenThreadFetch(ctx context.Context, ingestID string) (string, string, error) {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return "", "", err
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return "", "", err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return "", "", err
	}
	defer c.close()
	if _, err := c.selectFolder(folder.Name); err != nil {
		return "", "", err
	}
	// We already cached body_text on poll — but call BODY[] (not PEEK)
	// here so the server marks the message \Seen. The returned bytes are
	// discarded for now.
	// (Caller may want to refresh from server in a v1.1 — for v1 the
	//  cached body is the source of truth.)
	if !ing.Seen {
		if err := c.markSeen(ing.UID, true); err != nil {
			return ing.BodyText, ing.BodyHTML, err
		}
		_ = s.Repo.SetFlags(ctx, ingestID, map[string]bool{"seen": true})
		s.Bus.Broadcast()
	}
	return ing.BodyText, ing.BodyHTML, nil
}

// MoveTo MOVEs a message to the named destination folder. Updates the
// local mirror FolderID after server ack.
func (s *Service) MoveTo(ctx context.Context, ingestID, destFolderName string) error {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return err
	}
	src, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectFolder(src.Name); err != nil {
		return err
	}
	if err := c.moveMessage(ing.UID, destFolderName); err != nil {
		return err
	}
	// Local mirror update — find or upsert the destination folder.
	info, err := c.examineReadOnly(destFolderName)
	if err != nil {
		return err
	}
	destRole := detectRoleFromName(destFolderName)
	destFolder, err := s.Repo.UpsertFolder(ctx, src.AccountID, destFolderName, destRole, info.UIDValidity)
	if err != nil {
		return err
	}
	if err := s.Repo.UpdateFolderForIngest(ctx, ingestID, destFolder.ID); err != nil {
		return err
	}
	s.Bus.Broadcast()
	return nil
}

// detectRoleFromName mirrors the imap-side detectRole heuristic for
// folder names typed in by the user (foreground move target). Cannot
// access SPECIAL-USE attrs from name alone.
func detectRoleFromName(name string) string {
	return detectRole(nil, name)
}

// HardDelete: STORE +\Deleted + UID EXPUNGE + drop local row.
func (s *Service) HardDelete(ctx context.Context, ingestID string) error {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return err
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectFolder(folder.Name); err != nil {
		return err
	}
	if err := c.storeFlag(ing.UID, imap.FlagDeleted, true); err != nil {
		return err
	}
	if err := c.expungeUID(ing.UID); err != nil {
		return err
	}
	if err := s.Repo.HardDeleteIngest(ctx, ingestID); err != nil {
		return err
	}
	s.Bus.Broadcast()
	return nil
}

// AppendMessage exposes the wrapper APPEND for the send + notes layers.
// Returns the IMAP append wrapping any low-level error.
func (s *Service) AppendMessage(ctx context.Context, folderName string, raw []byte, flags []imap.Flag) error {
	_, err := s.AppendMessageUID(ctx, folderName, raw, flags, "")
	return err
}

// AppendMessageUID APPENDs and returns the assigned UID. messageID is
// the RFC 2822 Message-ID we set on `raw`; used as the SEARCH fallback
// for servers that don't advertise APPENDUID.
func (s *Service) AppendMessageUID(ctx context.Context, folderName string, raw []byte, flags []imap.Flag, messageID string) (uint32, error) {
	if folderName == "" {
		return 0, errors.New("mailbox: empty folder")
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return 0, err
	}
	defer c.close()
	uid, err := c.appendMessage(folderName, raw, flags)
	if err != nil {
		return 0, err
	}
	if uid != 0 || messageID == "" {
		return uid, nil
	}
	// Fallback for servers without APPENDUID: SELECT then SEARCH by MID.
	if _, err := c.selectFolder(folderName); err != nil {
		return 0, nil
	}
	uid, _ = c.searchByMessageID(messageID)
	return uid, nil
}

// Reply: APPEND raw to Sent + STORE +\Answered on original.
func (s *Service) Reply(ctx context.Context, originalIngestID, sentFolder string, raw []byte) error {
	if err := s.AppendMessage(ctx, sentFolder, raw, []imap.Flag{imap.FlagSeen}); err != nil {
		return fmt.Errorf("reply append: %w", err)
	}
	if originalIngestID == "" {
		return nil
	}
	ing, err := s.Repo.FindIngest(ctx, originalIngestID)
	if err != nil {
		return nil // original missing — not fatal
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return nil
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return nil
	}
	defer c.close()
	if _, err := c.selectFolder(folder.Name); err != nil {
		return nil
	}
	_ = c.markAnswered(ing.UID)
	_ = s.Repo.SetFlags(ctx, originalIngestID, map[string]bool{"answered": true})
	s.Bus.Broadcast()
	return nil
}

// MaterialiseAttachment fetches one MIME part by (ingest, mime_part_id)
// using BODY.PEEK (does not mark \Seen). Returns the decoded binary
// payload. Caller writes it to the CAS.
func (s *Service) MaterialiseAttachment(ctx context.Context, ingestID, mimePartID, transferEncoding string) ([]byte, error) {
	ing, err := s.Repo.FindIngest(ctx, ingestID)
	if err != nil {
		return nil, err
	}
	folder, err := s.Repo.FindFolder(ctx, ing.FolderID)
	if err != nil {
		return nil, err
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return nil, err
	}
	defer c.close()
	// EXAMINE — materialising shouldn't side-effect \Seen.
	if _, err := c.examineReadOnly(folder.Name); err != nil {
		return nil, err
	}
	raw, err := c.fetchPartByID(ing.UID, mimePartID, true)
	if err != nil {
		return nil, err
	}
	return decodeAttachmentBytes(raw, transferEncoding), nil
}

// ListNotesFolder returns the UIDs + envelopes of every message in the
// configured Notes folder. Used by the notes sync on boot + edit-as-
// APPEND-and-EXPUNGE.
func (s *Service) ListNotesFolder(ctx context.Context, notesFolder string) ([]FetchedEnvelope, error) {
	c, err := dial(s.Cfg)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if _, err := c.examineReadOnly(notesFolder); err != nil {
		return nil, err
	}
	return c.fetchEnvelopesSince(0)
}

// FetchNoteBody fetches the markdown body for one note by UID.
func (s *Service) FetchNoteBody(ctx context.Context, notesFolder string, uid uint32, textPath []int, encoding, charset string) (string, error) {
	c, err := dial(s.Cfg)
	if err != nil {
		return "", err
	}
	defer c.close()
	if _, err := c.examineReadOnly(notesFolder); err != nil {
		return "", err
	}
	raw, err := c.fetchPartPeek(uid, textPath)
	if err != nil {
		return "", err
	}
	return decodeTextBody(raw, encoding, charset), nil
}

// EditNoteAppendExpunge appends a new note version, then marks the
// previous version \Deleted + EXPUNGEs. IMAP has no UPDATE — this is
// the protocol-correct shape.
//
// oldUID may be 0 (notes created by an earlier version that didn't
// capture APPENDUID). When 0, we SEARCH by Message-ID to recover the
// UID. Returns the new UID so the handler can persist it.
func (s *Service) EditNoteAppendExpunge(ctx context.Context, notesFolder string, oldUID uint32, oldMessageID string, raw []byte, newMessageID string) (uint32, error) {
	c, err := dial(s.Cfg)
	if err != nil {
		return 0, err
	}
	defer c.close()
	newUID, err := c.appendMessage(notesFolder, raw, nil)
	if err != nil {
		return 0, err
	}
	if _, err := c.selectFolder(notesFolder); err != nil {
		return newUID, err
	}
	// Recover the new UID via SEARCH if the server didn't advertise
	// APPENDUID. The Message-ID we just APPEND'd is on the server now.
	if newUID == 0 && newMessageID != "" {
		if uid, _ := c.searchByMessageID(newMessageID); uid != 0 {
			newUID = uid
		}
	}
	// Find the OLD UID by Message-ID if we don't have one stored.
	if oldUID == 0 && oldMessageID != "" {
		if uid, _ := c.searchByMessageID(oldMessageID); uid != 0 {
			oldUID = uid
		}
	}
	if oldUID == 0 {
		// Can't EXPUNGE without a target. Log path — handler logs.
		return newUID, errors.New("note: cannot resolve old UID for EXPUNGE")
	}
	if err := c.storeFlag(oldUID, imap.FlagDeleted, true); err != nil {
		return newUID, err
	}
	return newUID, c.expungeUID(oldUID)
}

// EnsureFolders creates the named IMAP folders if they don't already
// exist. Called at boot for IMAP_NOTES_FOLDER / IMAP_SENT_FOLDER /
// IMAP_TRASH_FOLDER / IMAP_ARCHIVE_FOLDER so they show up in third-
// party clients (Roundcube, Apple Mail, Thunderbird) even before the
// user creates their first note / sends their first reply / archives
// their first thread. Idempotent — already-existing folders are
// skipped silently.
func (s *Service) EnsureFolders(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	existing, err := c.listFolders()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, fi := range existing {
		have[fi.Name] = true
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if !have[name] {
			if cerr := c.createMailbox(name); cerr != nil {
				s.Log.Warn("mailbox: ensure folder create", "folder", name, "err", cerr)
				continue
			}
			s.Log.Info("mailbox: created folder", "folder", name)
		}
		// Always SUBSCRIBE — needed for Roundcube and similar clients
		// that hide unsubscribed folders by default. Idempotent on
		// already-subscribed mailboxes per RFC 3501.
		if serr := c.subscribeMailbox(name); serr != nil {
			s.Log.Warn("mailbox: subscribe folder", "folder", name, "err", serr)
		}
	}
	return nil
}

// KeywordSet sets/removes one IMAP keyword on a message in the given
// folder. Used for $Pinned + $note_<slug>.
func (s *Service) KeywordSet(ctx context.Context, folderName string, uid uint32, keyword string, add bool) error {
	c, err := dial(s.Cfg)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.selectFolder(folderName); err != nil {
		return err
	}
	return c.storeKeyword(uid, keyword, add)
}
