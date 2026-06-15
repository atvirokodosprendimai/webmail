package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// hasKeyword returns true when the named keyword is present in the
// FetchedEnvelope.Keywords list. Case-sensitive per RFC 3501.
func hasKeyword(keywords []string, keyword string) bool {
	for _, k := range keywords {
		if k == keyword {
			return true
		}
	}
	return false
}

// notesTagsFromKeywords collects every $note_<slug> keyword and returns
// them space-joined (matches our Note.Tags storage).
func notesTagsFromKeywords(keywords []string) string {
	var tags []string
	for _, k := range keywords {
		if strings.HasPrefix(k, "$note_") {
			tags = append(tags, strings.TrimPrefix(k, "$note_"))
		}
	}
	return strings.Join(tags, " ")
}

// PollWorker dials the configured shared IMAP mailbox on a ticker,
// walks every folder, and ingests new messages. Foreground actions
// (open, flag, move) use the Service directly with a fresh short-lived
// session — they don't share this worker's connection.
type PollWorker struct {
	Cfg            AccountConfig
	AccountID      string
	Interval       time.Duration
	FlagSyncEvery  int
	Repo           *Repo
	Bus            *Bus
	Log            *slog.Logger
	// NotesFolder, when matched, routes scanned messages into the
	// notes table instead of mailbox_ingest. Lets users create notes
	// from any IMAP client and have them appear in /notes.
	NotesFolder string
	NotesSink   NotesSink
	cycleCount  int
}

// NotesSink is the minimal contract poll.go uses to hand a parsed
// note off to the notes package. Lets us avoid an import cycle with
// internal/notes.
type NotesSink interface {
	UpsertFromIMAP(ctx context.Context, in NoteUpsert) error
}

// NoteUpsert is the shape the poll loop hands to the notes sink for
// every message in IMAP_NOTES_FOLDER.
type NoteUpsert struct {
	MessageID    string
	UID          uint32
	Title        string
	BodyMD       string
	Pinned       bool
	Tags         string
	OriginalMID  string
	UpdatedAt    time.Time
}

func (w *PollWorker) Start(ctx context.Context) {
	if w.Interval <= 0 {
		w.Interval = 60 * time.Second
	}
	if w.FlagSyncEvery <= 0 {
		w.FlagSyncEvery = 10
	}
	go w.run(ctx)
}

func (w *PollWorker) run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.cycle(ctx)
	for {
		select {
		case <-ctx.Done():
			w.Log.Info("mailbox: poll stopping")
			return
		case <-t.C:
			w.cycle(ctx)
		}
	}
}

func (w *PollWorker) cycle(ctx context.Context) {
	if w.Cfg.Host == "" || w.Cfg.Username == "" {
		w.Log.Warn("mailbox: cycle skipped — host/user unset")
		return
	}
	w.cycleCount++
	start := time.Now()
	c, err := dial(w.Cfg)
	if err != nil {
		w.Log.Error("mailbox: dial", "err", err)
		return
	}
	defer c.close()

	folders, err := c.listFolders()
	if err != nil {
		w.Log.Error("mailbox: list folders", "err", err)
		return
	}
	w.Log.Info("mailbox: cycle begin", "folders", len(folders))

	doFlagSync := w.cycleCount%w.FlagSyncEvery == 0
	var ingested int
	for _, fi := range folders {
		if ctx.Err() != nil {
			return
		}
		n, err := w.scanFolder(ctx, c, fi, doFlagSync)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			w.Log.Warn("mailbox: scan folder", "folder", fi.Name, "err", err)
			continue
		}
		ingested += n
	}
	w.Log.Info("mailbox: cycle end",
		"dur_ms", time.Since(start).Milliseconds(),
		"ingested", ingested,
		"flag_sync", doFlagSync,
	)
	if ingested > 0 {
		w.Bus.Broadcast()
	}
}

// isNotesFolder returns true when the named folder is the configured
// IMAP_NOTES_FOLDER and a notes sink is wired.
func (w *PollWorker) isNotesFolder(name string) bool {
	return w.NotesFolder != "" && w.NotesSink != nil && name == w.NotesFolder
}

func (w *PollWorker) scanFolder(ctx context.Context, c *imapClient, fi folderInfo, doFlagSync bool) (int, error) {
	info, err := c.examineReadOnly(fi.Name)
	if err != nil {
		return 0, err
	}
	folder, err := w.Repo.UpsertFolder(ctx, w.AccountID, fi.Name, fi.Role, info.UIDValidity)
	if err != nil {
		return 0, err
	}

	if info.NumMessages == 0 {
		return 0, nil
	}

	// Optional flag reconciliation pass — catches mutations made by
	// other mail clients connected to the same account.
	if doFlagSync {
		if err := w.reconcileFlags(ctx, c, folder); err != nil {
			w.Log.Warn("mailbox: flag reconcile", "folder", fi.Name, "err", err)
		}
	}

	// Skip body fetch when we're already at the head of the folder.
	if folder.LastUID >= info.UIDNext-1 && info.UIDNext > 0 {
		return 0, nil
	}

	envs, err := c.fetchEnvelopesSince(folder.LastUID)
	if err != nil {
		return 0, err
	}
	if len(envs) == 0 {
		return 0, nil
	}

	var inserted int
	var maxUID = folder.LastUID
	saveCursor := func() {
		if maxUID > folder.LastUID {
			if err := w.Repo.SetFolderLastUID(ctx, folder.ID, maxUID); err == nil {
				folder.LastUID = maxUID
			}
		}
	}
	notesPath := w.isNotesFolder(fi.Name)
	for _, e := range envs {
		if ctx.Err() != nil {
			break
		}
		if e.UID > maxUID {
			maxUID = e.UID
		}

		// Notes-folder routing: parse body, upsert into notes table,
		// do NOT touch mailbox_ingest. Keeps the inbox view clean.
		if notesPath {
			body := ""
			if len(e.TextPath) > 0 {
				raw, err := c.fetchPartPeek(e.UID, e.TextPath)
				if err == nil {
					body = strings.TrimSpace(decodeTextBody(raw, e.TextEncoding, e.TextCharset))
				}
			}
			err := w.NotesSink.UpsertFromIMAP(ctx, NoteUpsert{
				MessageID:   e.MessageID,
				UID:         e.UID,
				Title:       DecodeHeader(e.Subject),
				BodyMD:      body,
				Pinned:      hasKeyword(e.Keywords, "$Pinned"),
				Tags:        notesTagsFromKeywords(e.Keywords),
				OriginalMID: "",
				UpdatedAt:   e.InternalDate,
			})
			if err != nil {
				w.Log.Warn("mailbox: notes upsert", "uid", e.UID, "err", err)
				saveCursor()
				continue
			}
			inserted++
			saveCursor()
			continue
		}

		bodyText, bodyHTML := "", ""
		if len(e.TextPath) > 0 {
			raw, err := c.fetchPartPeek(e.UID, e.TextPath)
			if err != nil {
				w.Log.Warn("mailbox: body fetch", "uid", e.UID, "err", err)
			} else {
				decoded := decodeTextBody(raw, e.TextEncoding, e.TextCharset)
				if e.IsTextPlain {
					bodyText = strings.TrimSpace(decoded)
				} else {
					bodyHTML = decoded
					bodyText = ExtractBody("", decoded)
				}
			}
		}

		in := IngestInsert{
			FolderID:    folder.ID,
			UID:         e.UID,
			UIDValidity: info.UIDValidity,
			MessageID:   e.MessageID,
			InReplyTo:   e.InReplyTo,
			References:  e.References,
			FromAddr:    e.FromAddr,
			FromName:    e.FromName,
			Subject:     e.Subject,
			BodyText:    bodyText,
			BodyHTML:    bodyHTML,
			Seen:        e.Seen,
			Flagged:     e.Flagged,
			Answered:    e.Answered,
			Draft:       e.Draft,
			Deleted:     e.Deleted,
			ReceivedAt:  e.InternalDate,
		}
		_, isNew, err := w.Repo.InsertIngest(ctx, in)
		if err != nil {
			w.Log.Warn("mailbox: insert ingest", "uid", e.UID, "err", err)
			saveCursor()
			continue
		}
		// We INSERT attachments only for new rows (folder-move updates
		// keep the original attachment list).
		if isNew {
			// IngestID is the row we just looked up by MessageID.
			row, err := w.Repo.FindIngestByMessageID(ctx, e.MessageID)
			if err == nil {
				if aerr := w.Repo.InsertAttachments(ctx, row.ID, e.Attachments); aerr != nil {
					w.Log.Warn("mailbox: insert attachments", "uid", e.UID, "err", aerr)
				}
			}
			inserted++
		}
		// Per-message cursor save — crash mid-batch never re-ingests.
		saveCursor()
	}
	saveCursor()
	return inserted, nil
}

// reconcileFlags pulls server-side flags for every UID in the folder
// and updates the local mirror where it drifted. Cheap: server returns
// only UID + flags.
func (w *PollWorker) reconcileFlags(ctx context.Context, c *imapClient, folder Folder) error {
	server, err := c.fetchFlagsAll()
	if err != nil {
		return err
	}
	if len(server) == 0 {
		return nil
	}
	// Pull the local mirror — UID + flag mirror columns — once per folder.
	var locals []struct {
		ID      string
		UID     uint32
		Seen    bool
		Flagged bool
	}
	if err := w.Repo.db.WithContext(ctx).
		Table("mailbox_ingest").
		Select("id", "uid", "seen", "flagged").
		Where("folder_id = ?", folder.ID).
		Scan(&locals).Error; err != nil {
		return err
	}
	for _, l := range locals {
		flags, ok := server[l.UID]
		if !ok {
			continue
		}
		want := map[string]bool{
			"seen":    false,
			"flagged": false,
		}
		for _, f := range flags {
			switch string(f) {
			case `\Seen`:
				want["seen"] = true
			case `\Flagged`:
				want["flagged"] = true
			}
		}
		if want["seen"] == l.Seen && want["flagged"] == l.Flagged {
			continue
		}
		_ = w.Repo.SetFlags(ctx, l.ID, want)
	}
	return nil
}
