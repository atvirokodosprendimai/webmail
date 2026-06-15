package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/atvirokodosprendimai/webmail/internal/bookmarks"
	"github.com/atvirokodosprendimai/webmail/internal/config"
	"github.com/atvirokodosprendimai/webmail/internal/issues"
	"github.com/atvirokodosprendimai/webmail/internal/mailbox"
	"github.com/atvirokodosprendimai/webmail/internal/notes"
	"github.com/atvirokodosprendimai/webmail/internal/projects"
	"github.com/atvirokodosprendimai/webmail/internal/render"
	"github.com/atvirokodosprendimai/webmail/internal/send"
	"github.com/atvirokodosprendimai/webmail/internal/uploads"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// App bundles the wired application services. One instance built in
// cmd/webmail/main.go; passed into New() for routing.
type App struct {
	Cfg          config.Config
	DB           *gorm.DB
	AuthRepo     *auth.Repo
	AuthHandler  *auth.Handler
	Sessions     *auth.Sessions
	MailboxRepo  *mailbox.Repo
	MailboxSvc   *mailbox.Service
	Bus          *mailbox.Bus
	ProjectsRepo  *projects.Repo
	NotesRepo     *notes.Repo
	BookmarksRepo *bookmarks.Repo
	IssuesRepo    *issues.Repo
	Uploads       *uploads.Store
}

// --- inbox / thread ---

func (a *App) navCounts(ctx context.Context) render.NavCounts {
	var unread, total int64
	a.DB.WithContext(ctx).
		Table("mailbox_ingest").
		Joins("INNER JOIN mailbox_folder ON mailbox_folder.id = mailbox_ingest.folder_id").
		Where("mailbox_folder.role = ?", mailbox.FolderRoleInbox).
		Count(&total)
	a.DB.WithContext(ctx).
		Table("mailbox_ingest").
		Joins("INNER JOIN mailbox_folder ON mailbox_folder.id = mailbox_ingest.folder_id").
		Where("mailbox_folder.role = ? AND mailbox_ingest.seen = 0", mailbox.FolderRoleInbox).
		Count(&unread)
	openIssues := 0
	if a.IssuesRepo != nil {
		openIssues, _ = a.IssuesRepo.CountOpen(ctx)
	}
	return render.NavCounts{
		InboxUnread: int(unread),
		InboxTotal:  int(total),
		IssuesOpen:  openIssues,
	}
}

type viewerKey struct{}

func (a *App) inbox(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ctx := context.WithValue(r.Context(), viewerKey{}, u.ID)
	rows, err := a.fetchThreadedRows(ctx, mailbox.FolderRoleInbox, q, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Inbox(u.DisplayName, a.navCounts(r.Context()), rows, q, "/inbox").Render(r.Context(), w)
}

// folderView renders the same list template as /inbox but for any role
// (sent, drafts, trash, archive). Lets the nav links work without
// duplicating the template.
func (a *App) folderView(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	role := chi.URLParam(r, "role")
	switch role {
	case mailbox.FolderRoleSent, mailbox.FolderRoleDrafts,
		mailbox.FolderRoleTrash, mailbox.FolderRoleArchive,
		mailbox.FolderRoleInbox:
	default:
		http.NotFound(w, r)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ctx := context.WithValue(r.Context(), viewerKey{}, u.ID)
	rows, err := a.fetchThreadedRows(ctx, role, q, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Inbox(u.DisplayName, a.navCounts(r.Context()), rows, q, "/folder/"+role).Render(r.Context(), w)
}

func (a *App) fetchRowsForRole(ctx context.Context, role string, limit int) ([]render.InboxRow, error) {
	return a.fetchThreadedRows(ctx, role, "", limit)
}

func (a *App) fetchInboxRows(ctx context.Context, limit int) ([]render.InboxRow, error) {
	return a.fetchThreadedRows(ctx, mailbox.FolderRoleInbox, "", limit)
}

// fetchThreadedRows groups by thread_id, picks the latest message per
// thread, computes per-thread count + OR'd flagged + attach indicators
// across the whole thread (so an old flag still shows on the
// representative row).
//
// q (when non-empty) restricts to threads where ANY message matches
// LIKE on subject / from_addr / from_name / body_text / to_addrs.
// Case-insensitive via LOWER().
func (a *App) fetchThreadedRows(ctx context.Context, role, q string, limit int) ([]render.InboxRow, error) {
	// Read viewer user_id off the auth-loaded ctx (passed in via the
	// goroutine-local mechanism would be heavier than reading the row).
	// Bookmarked column fires when ANY bookmark for THIS user OR a
	// shared bookmark targets ANY message_id in the thread.
	viewerID, _ := ctx.Value(viewerKey{}).(string)
	var rows []struct {
		ID          string
		Subject     string
		FromName    string
		FromAddr    string
		BodyText    string
		ReceivedAt  time.Time
		Seen        bool
		Flagged     bool
		HasAttach   bool
		Bookmarked  bool
		ThreadID    string
		ThreadCount int
	}
	// SQLite trick: pick MAX(received_at) per thread, join back to get
	// the corresponding row. EXISTS subqueries fold flagged/attach
	// across the full thread.
	err := a.DB.WithContext(ctx).Raw(`
SELECT
  m.id, m.subject, m.from_name, m.from_addr, m.body_text,
  m.received_at, m.seen, m.flagged, m.thread_id,
  EXISTS (
    SELECT 1 FROM mailbox_attachment a
    INNER JOIN mailbox_ingest mi ON mi.id = a.ingest_id
    WHERE mi.thread_id = m.thread_id
  ) AS has_attach,
  EXISTS (
    SELECT 1 FROM bookmarks bk
    INNER JOIN mailbox_ingest mb ON mb.message_id = bk.message_id
    WHERE mb.thread_id = m.thread_id AND (bk.user_id = ? OR bk.user_id = '')
  ) AS bookmarked,
  (
    SELECT COUNT(*) FROM mailbox_ingest mc
    INNER JOIN mailbox_folder fc ON fc.id = mc.folder_id
    WHERE mc.thread_id = m.thread_id
  ) AS thread_count
FROM mailbox_ingest m
INNER JOIN mailbox_folder f ON f.id = m.folder_id
INNER JOIN (
  SELECT mx.thread_id, MAX(mx.received_at) AS latest
  FROM mailbox_ingest mx
  INNER JOIN mailbox_folder fx ON fx.id = mx.folder_id
  WHERE fx.role = ?
  GROUP BY mx.thread_id
) t ON t.thread_id = m.thread_id AND t.latest = m.received_at
WHERE f.role = ?` + searchClause(q) + `
ORDER BY m.received_at DESC
LIMIT ?`, searchArgs(viewerID, role, q, limit)...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]render.InboxRow, 0, len(rows))
	for _, r := range rows {
		snip := r.BodyText
		if len(snip) > 140 {
			snip = snip[:140] + "…"
		}
		snip = strings.ReplaceAll(snip, "\n", " ")
		out = append(out, render.InboxRow{
			IngestID:    r.ID,
			Subject:     mailbox.DecodeHeader(r.Subject),
			FromName:    mailbox.DecodeHeader(r.FromName),
			FromAddr:    r.FromAddr,
			Snippet:     snip,
			ReceivedAt:  r.ReceivedAt,
			Seen:        r.Seen,
			Flagged:     r.Flagged,
			HasAttach:   r.HasAttach,
			Bookmarked:  r.Bookmarked,
			ThreadCount: r.ThreadCount,
		})
	}
	return out, nil
}

func (a *App) inboxStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	sub, cancel := a.Bus.Subscribe()
	defer cancel()
	ctx := r.Context()
	// initial fragment
	rows, _ := a.fetchInboxRows(ctx, 50)
	_ = writeRowsFragment(w, rows)
	flusher.Flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub:
			rows, _ := a.fetchInboxRows(ctx, 50)
			_ = writeRowsFragment(w, rows)
			flusher.Flush()
		case <-time.After(30 * time.Second):
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeRowsFragment(w http.ResponseWriter, rows []render.InboxRow) error {
	// Datastar morph: send `event: datastar-patch-elements\ndata: ...`.
	// To keep this dependency-light we emit a plain SSE message that the
	// client uses for a `data-on('sse:rows')` reload trigger. Phase 6
	// wires real Datastar fragments.
	fmt.Fprint(w, "event: rows\n")
	fmt.Fprint(w, "data: refresh\n\n")
	return nil
}

func (a *App) thread(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	ing, err := a.MailboxRepo.FindIngest(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Mark seen via foreground service (issues SELECT + STORE).
	go func() {
		if !ing.Seen {
			_, _, _ = a.MailboxSvc.OpenThreadFetch(context.Background(), id)
		}
	}()
	thread, err := a.MailboxRepo.ThreadIngests(r.Context(), ing.ThreadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msgs := make([]render.ThreadMessage, 0, len(thread))
	for _, m := range thread {
		atts, _ := a.MailboxRepo.AttachmentsForIngest(r.Context(), m.ID)
		ratt := make([]render.ThreadAttachment, 0, len(atts))
		for _, a := range atts {
			ratt = append(ratt, render.ThreadAttachment{
				ID: a.ID, Filename: a.Filename, MIME: a.MIME, Size: a.SizeBytes,
				Materialised: a.UploadSHA256 != "" && a.UploadSHA256 != "TOO_LARGE",
			})
		}
		msgs = append(msgs, render.ThreadMessage{
			IngestID:    m.ID,
			FromName:    mailbox.DecodeHeader(m.FromName),
			FromAddr:    m.FromAddr,
			Subject:     mailbox.DecodeHeader(m.Subject),
			BodyText:    m.BodyText,
			BodyHTML:    m.BodyHTML,
			ReceivedAt:  m.ReceivedAt,
			Seen:        m.Seen,
			Flagged:     m.Flagged,
			Answered:    m.Answered,
			Attachments: ratt,
		})
	}
	// project options
	allProjects, _ := a.ProjectsRepo.List(r.Context())
	opts := make([]render.ProjectOption, 0, len(allProjects))
	for _, p := range allProjects {
		opts = append(opts, render.ProjectOption{Slug: p.Slug, Name: p.Name})
	}
	// already-tagged
	tagged := []string{}
	if ps, err := a.ProjectsRepo.ProjectsForMessage(r.Context(), ing.MessageID); err == nil {
		for _, p := range ps {
			tagged = append(tagged, p.Name)
		}
	}
	subject := "(no subject)"
	if len(msgs) > 0 && msgs[0].Subject != "" {
		subject = mailbox.DecodeHeader(msgs[0].Subject)
	}
	// Bookmark state for the thread (any message_id in the thread counts).
	var threadMIDs []string
	for _, m := range thread {
		threadMIDs = append(threadMIDs, m.MessageID)
	}
	bookState := render.BookmarkState{}
	if personal, shared, err := a.BookmarksRepo.FindForThread(r.Context(), u.ID, threadMIDs); err == nil {
		if len(personal) > 0 {
			bookState.PersonalID = personal[0].ID
		}
		if len(shared) > 0 {
			bookState.SharedID = shared[0].ID
		}
	}
	// Issue banner (if a thread has already been promoted to one).
	issueBanner := render.IssueBanner{}
	if iss, ierr := a.IssuesRepo.FindByMessageID(r.Context(), ing.MessageID); ierr == nil {
		issueBanner = render.IssueBanner{
			IssueID: iss.ID,
			Number:  iss.Number,
			Status:  iss.Status,
			Title:   iss.Title,
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Thread(u.DisplayName, a.navCounts(r.Context()), subject, msgs, opts, tagged, bookState, issueBanner).Render(r.Context(), w)
}

// --- issues ---

func (a *App) issuesIndex(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	mine := r.URL.Query().Get("mine") == "1"
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	opts := issues.ListOptions{Status: status, Search: q, Limit: 200}
	if status == "all" {
		opts.Status = ""
	}
	if mine {
		opts.AssignedTo = u.ID
	}
	rows, err := a.IssuesRepo.List(r.Context(), opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]render.IssueRow, 0, len(rows))
	for _, it := range rows {
		out = append(out, render.IssueRow{
			ID:        it.ID,
			Number:    it.Number,
			Title:     it.Title,
			Status:    it.Status,
			UpdatedAt: it.UpdatedAt,
			Assignees: a.loadAssignees(r.Context(), it.ID),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Issues(u.DisplayName, a.navCounts(r.Context()), out, render.IssueListFilters{
		Status: status, AssignedMe: mine, Search: q,
	}).Render(r.Context(), w)
}

func (a *App) loadAssignees(ctx context.Context, issueID string) []render.IssueAssignee {
	ids, err := a.IssuesRepo.AssigneesFor(ctx, issueID)
	if err != nil || len(ids) == 0 {
		return nil
	}
	var users []auth.User
	a.DB.WithContext(ctx).Where("id IN ?", ids).Find(&users)
	out := make([]render.IssueAssignee, 0, len(users))
	for _, u := range users {
		out = append(out, render.IssueAssignee{
			UserID: u.ID, DisplayName: u.DisplayName, Email: u.Email,
		})
	}
	return out
}

func (a *App) issuesCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	ingestID := r.FormValue("ingest_id")
	messageID := r.FormValue("message_id")
	title := r.FormValue("title")
	if ingestID != "" {
		ing, ierr := a.MailboxRepo.FindIngest(r.Context(), ingestID)
		if ierr != nil {
			http.Error(w, "thread not found", http.StatusNotFound)
			return
		}
		messageID = ing.MessageID
		if title == "" {
			title = mailbox.DecodeHeader(ing.Subject)
		}
	}
	if messageID == "" {
		http.Error(w, "message_id required", http.StatusBadRequest)
		return
	}
	it, _, err := a.IssuesRepo.CreateFromThread(r.Context(), messageID, title, u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+it.ID, http.StatusSeeOther)
}

func (a *App) issueShow(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	it, err := a.IssuesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Find an ingest row for this thread so we can link to the email view.
	threadIngestID, threadSubject := "", ""
	if ing, ferr := a.MailboxRepo.FindIngestByMessageID(r.Context(), it.MessageID); ferr == nil {
		threadIngestID = ing.ID
		threadSubject = mailbox.DecodeHeader(ing.Subject)
	}
	assignees := a.loadAssignees(r.Context(), it.ID)
	assignedSet := map[string]bool{}
	for _, a := range assignees {
		assignedSet[a.UserID] = true
	}
	// Candidates = every user not already assigned.
	var users []auth.User
	a.DB.WithContext(r.Context()).Order("display_name ASC").Find(&users)
	cands := make([]render.IssueAssignee, 0, len(users))
	for _, uu := range users {
		if assignedSet[uu.ID] {
			continue
		}
		cands = append(cands, render.IssueAssignee{
			UserID: uu.ID, DisplayName: uu.DisplayName, Email: uu.Email,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.IssueShow(u.DisplayName, a.navCounts(r.Context()), render.IssueDetail{
		ID:             it.ID,
		Number:         it.Number,
		Title:          it.Title,
		NotesMD:        it.NotesMD,
		NotesHTML:      it.NotesHTML,
		Status:         it.Status,
		UpdatedAt:      it.UpdatedAt,
		CreatedAt:      it.CreatedAt,
		ThreadIngestID: threadIngestID,
		ThreadSubject:  threadSubject,
		Assignees:      assignees,
		Candidates:     cands,
	}).Render(r.Context(), w)
}

func (a *App) issueStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := r.FormValue("status")
	if err := a.IssuesRepo.SetStatus(r.Context(), id, st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
}

func (a *App) issueTitle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := a.IssuesRepo.SetTitle(r.Context(), id, r.FormValue("title")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
}

func (a *App) issueNotes(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := a.IssuesRepo.SetNotes(r.Context(), id, r.FormValue("notes_md")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
}

func (a *App) issueAssign(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userID := r.FormValue("user_id")
	if userID == "" {
		http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
		return
	}
	if err := a.IssuesRepo.Assign(r.Context(), id, userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
}

func (a *App) issueUnassign(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := chi.URLParam(r, "userId")
	if err := a.IssuesRepo.Unassign(r.Context(), id, userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+id, http.StatusSeeOther)
}

func (a *App) threadSeen(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	seen := r.FormValue("seen") == "true"
	if err := a.MailboxSvc.MarkSeen(r.Context(), id, seen); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/thread/"+id, http.StatusSeeOther)
}

func (a *App) threadFlag(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	flagged := r.FormValue("flagged") == "true"
	if err := a.MailboxSvc.MarkFlagged(r.Context(), id, flagged); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/thread/"+id, http.StatusSeeOther)
}

func (a *App) threadMove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dest := r.FormValue("dest")
	folderName := dest
	switch strings.ToLower(dest) {
	case "trash":
		folderName = a.Cfg.IMAPTrashFolder
	case "archive":
		folderName = a.Cfg.IMAPArchiveFolder
	}
	if err := a.MailboxSvc.MoveTo(r.Context(), id, folderName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/inbox", http.StatusSeeOther)
}

// --- compose / send / reply ---

func (a *App) composePage(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Compose(u.DisplayName, a.navCounts(r.Context()), render.ComposePrefill{}, "").Render(r.Context(), w)
}

func (a *App) composeSend(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	cmd := send.Command{
		From:     formatFrom(u.DisplayName, u.Email),
		To:       splitAddrs(r.FormValue("to")),
		Cc:       splitAddrs(r.FormValue("cc")),
		Subject:  r.FormValue("subject"),
		BodyText: r.FormValue("body"),
		ReplyTo:  a.Cfg.IMAPUsername,
	}
	replyTo := r.FormValue("reply_to")
	if replyTo != "" {
		orig, err := a.MailboxRepo.FindIngest(r.Context(), replyTo)
		if err == nil {
			cmd.InReplyTo = orig.MessageID
			cmd.Refs = append(strings.Fields(orig.References), orig.MessageID)
		}
	}
	_, raw, err := send.BuildMessage(cmd)
	if err != nil {
		http.Error(w, "build: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rcpts := append(append([]string{}, cmd.To...), cmd.Cc...)
	slog.Info("smtp send begin",
		"to", rcpts, "subject", cmd.Subject,
		"header_from", cmd.From, "envelope_from", a.Cfg.SMTPUsername,
		"bytes", len(raw),
	)
	if err := send.Send(send.Config{
		Host: a.Cfg.SMTPHost, Port: a.Cfg.SMTPPort, TLSMode: a.Cfg.SMTPTLS,
		Username: a.Cfg.SMTPUsername, Password: a.Cfg.SMTPPassword,
	}, a.Cfg.SMTPUsername, rcpts, raw); err != nil {
		slog.Error("smtp send failed", "err", err, "to", rcpts)
		http.Error(w, "smtp: "+err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("smtp send ok", "to", rcpts)
	if rerr := a.MailboxSvc.Reply(r.Context(), replyTo, a.Cfg.IMAPSentFolder, raw); rerr != nil {
		slog.Warn("imap append to Sent failed (mail still sent)", "err", rerr)
	}
	http.Redirect(w, r, "/inbox?sent=1", http.StatusSeeOther)
}

// formatFrom returns an RFC 5322 address-with-display-name. Uses
// net/mail so the display name is Q-encoded when it contains non-ASCII
// (Lithuanian / German / etc.) and quoted when it contains tspecials.
func formatFrom(displayName, email string) string {
	addr := (&mail.Address{Name: displayName, Address: email}).String()
	return addr
}

func splitAddrs(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) threadReply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := chi.URLParam(r, "id")
	orig, err := a.MailboxRepo.FindIngest(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	u := auth.CurrentUser(r)
	subject := mailbox.DecodeHeader(orig.Subject)
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	cmd := send.Command{
		From:      formatFrom(u.DisplayName, u.Email),
		To:        []string{orig.FromAddr},
		Subject:   subject,
		BodyText:  r.FormValue("body"),
		ReplyTo:   a.Cfg.IMAPUsername,
		InReplyTo: orig.MessageID,
		Refs:      append(strings.Fields(orig.References), orig.MessageID),
	}
	_, raw, err := send.BuildMessage(cmd)
	if err != nil {
		http.Error(w, "build: "+err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("smtp reply begin",
		"to", cmd.To, "subject", cmd.Subject,
		"header_from", cmd.From, "envelope_from", a.Cfg.SMTPUsername,
		"bytes", len(raw),
	)
	if err := send.Send(send.Config{
		Host: a.Cfg.SMTPHost, Port: a.Cfg.SMTPPort, TLSMode: a.Cfg.SMTPTLS,
		Username: a.Cfg.SMTPUsername, Password: a.Cfg.SMTPPassword,
	}, a.Cfg.SMTPUsername, cmd.To, raw); err != nil {
		slog.Error("smtp reply failed", "err", err, "to", cmd.To)
		http.Error(w, "smtp: "+err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("smtp reply ok", "to", cmd.To)
	if rerr := a.MailboxSvc.Reply(r.Context(), id, a.Cfg.IMAPSentFolder, raw); rerr != nil {
		slog.Warn("imap append to Sent failed (mail still sent)", "err", rerr)
	}
	http.Redirect(w, r, "/thread/"+id+"?sent=1", http.StatusSeeOther)
}

// --- projects ---

func (a *App) projectsIndex(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	all, _ := a.ProjectsRepo.List(r.Context())
	items := make([]render.ProjectListItem, 0, len(all))
	for _, p := range all {
		its, _ := a.ProjectsRepo.ItemsForProject(r.Context(), p.ID)
		items = append(items, render.ProjectListItem{
			Slug: p.Slug, Name: p.Name, Description: p.Description, ItemCount: len(its),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.ProjectsIndex(u.DisplayName, a.navCounts(r.Context()), items).Render(r.Context(), w)
}

func (a *App) projectsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	p, err := a.ProjectsRepo.Create(r.Context(), r.FormValue("name"), r.FormValue("description"), u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/p/"+p.Slug, http.StatusSeeOther)
}

func (a *App) projectPage(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	slug := chi.URLParam(r, "slug")
	p, err := a.ProjectsRepo.FindBySlug(r.Context(), slug)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	items, _ := a.ProjectsRepo.ItemsForProject(r.Context(), p.ID)
	feed := make([]render.ProjectFeedItem, 0, len(items))
	for _, it := range items {
		switch it.ItemKind {
		case projects.KindEmail:
			ing, err := a.MailboxRepo.FindIngestByMessageID(r.Context(), it.MessageID)
			if err != nil {
				continue
			}
			atts, _ := a.MailboxRepo.AttachmentsForIngest(r.Context(), ing.ID)
			ratt := make([]render.ThreadAttachment, 0, len(atts))
			for _, a := range atts {
				if a.UploadSHA256 == "" || a.UploadSHA256 == "TOO_LARGE" {
					continue
				}
				ratt = append(ratt, render.ThreadAttachment{
					ID: a.ID, Filename: a.Filename, MIME: a.MIME, Size: a.SizeBytes, Materialised: true,
				})
			}
			feed = append(feed, render.ProjectFeedItem{
				Kind:        "email",
				IngestID:    ing.ID,
				Title:       ing.Subject,
				From:        firstNonEmpty(ing.FromName, ing.FromAddr),
				Snippet:     truncate(ing.BodyText, 240),
				At:          ing.ReceivedAt,
				Attachments: ratt,
			})
		case projects.KindNote:
			n, err := a.NotesRepo.FindByMessageID(r.Context(), it.MessageID)
			if err != nil {
				continue
			}
			feed = append(feed, render.ProjectFeedItem{
				Kind:    "note",
				NoteID:  n.ID,
				Title:   n.Title,
				Snippet: truncate(n.BodyMD, 240),
				At:      n.UpdatedAt,
			})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.ProjectPage(u.DisplayName, a.navCounts(r.Context()), render.ProjectDetail{
		Slug: p.Slug, Name: p.Name, Description: p.Description, Items: feed,
	}).Render(r.Context(), w)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// threadTag tags every message in a thread into a project AND
// materialises every attachment that isn't yet on disk.
func (a *App) threadTag(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	id := r.FormValue("ingest_id")
	slug := r.FormValue("project_slug")
	if slug == "" {
		http.Error(w, "project required", http.StatusBadRequest)
		return
	}
	p, err := a.ProjectsRepo.FindBySlug(r.Context(), slug)
	if err != nil {
		http.Error(w, "no such project", http.StatusBadRequest)
		return
	}
	root, err := a.MailboxRepo.FindIngest(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	thread, _ := a.MailboxRepo.ThreadIngests(r.Context(), root.ThreadID)
	for _, m := range thread {
		_, _ = a.ProjectsRepo.Tag(r.Context(), p.ID, m.MessageID, projects.KindEmail, u.ID)
		// Materialise attachments not yet on disk.
		atts, _ := a.MailboxRepo.AttachmentsForIngest(r.Context(), m.ID)
		for _, at := range atts {
			if at.UploadSHA256 != "" {
				continue
			}
			if a.Cfg.AttachmentMaxBytes > 0 && at.SizeBytes > a.Cfg.AttachmentMaxBytes {
				_ = a.MailboxRepo.UpdateAttachmentUpload(r.Context(), at.ID, "TOO_LARGE", at.SizeBytes)
				continue
			}
			raw, err := a.MailboxSvc.MaterialiseAttachment(r.Context(), m.ID, at.MIMEPartID, at.TransferEncoding)
			if err != nil {
				continue
			}
			sha, size, err := a.Uploads.Write(raw)
			if err != nil {
				continue
			}
			_ = a.MailboxRepo.UpdateAttachmentUpload(r.Context(), at.ID, sha, size)
		}
	}
	http.Redirect(w, r, "/p/"+p.Slug, http.StatusSeeOther)
}

func (a *App) attachDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var att mailbox.Attachment
	if err := a.DB.WithContext(r.Context()).Where("id = ?", id).First(&att).Error; err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if att.UploadSHA256 == "" || att.UploadSHA256 == "TOO_LARGE" {
		http.Error(w, "not materialised", http.StatusNotFound)
		return
	}
	a.Uploads.Serve(w, r, att.UploadSHA256, att.Filename, att.MIME)
}

// --- notes ---

func (a *App) notesIndex(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	ns, _ := a.NotesRepo.List(r.Context())
	rows := make([]render.NoteRow, 0, len(ns))
	for _, n := range ns {
		rows = append(rows, render.NoteRow{
			ID: n.ID, Title: n.Title, Preview: truncate(n.BodyMD, 120),
			Pinned: n.Pinned, UpdatedAt: n.UpdatedAt,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.NotesIndex(u.DisplayName, a.navCounts(r.Context()), rows).Render(r.Context(), w)
}

func (a *App) noteNew(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.NoteEditor(u.DisplayName, a.navCounts(r.Context()), render.NoteEdit{}).Render(r.Context(), w)
}

func (a *App) noteCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	title := strings.TrimSpace(r.FormValue("title"))
	body := r.FormValue("body_md")
	mid, raw, err := notes.BuildNoteMessage(u.Email, u.DisplayName, title, body, "", 1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	uid, err := a.MailboxSvc.AppendMessageUID(r.Context(), a.Cfg.IMAPNotesFolder, raw, nil, mid)
	if err != nil {
		http.Error(w, "append: "+err.Error(), http.StatusInternalServerError)
		return
	}
	n := notes.Note{
		MessageID: mid,
		UID:       uid,
		AuthorID:  u.ID,
		Title:     title,
		BodyMD:    body,
		Tags:      r.FormValue("tags"),
	}
	if err := a.NotesRepo.Upsert(r.Context(), n); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	row, _ := a.NotesRepo.FindByMessageID(r.Context(), mid)
	http.Redirect(w, r, "/notes/"+row.ID, http.StatusSeeOther)
}

func (a *App) noteShow(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	n, err := a.NotesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.NoteShow(u.DisplayName, a.navCounts(r.Context()), render.NoteView{
		ID: n.ID, Title: n.Title, BodyHTML: n.BodyHTML, BodyMD: n.BodyMD,
		Pinned: n.Pinned, Tags: n.Tags, UpdatedAt: n.UpdatedAt,
	}).Render(r.Context(), w)
}

func (a *App) noteEdit(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	n, err := a.NotesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.NoteEditor(u.DisplayName, a.navCounts(r.Context()), render.NoteEdit{
		ID: n.ID, Title: n.Title, BodyMD: n.BodyMD, Pinned: n.Pinned, Tags: n.Tags,
	}).Render(r.Context(), w)
}

func (a *App) noteSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	old, err := a.NotesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	body := r.FormValue("body_md")
	// Find root original MID (first version).
	origMID := old.MessageID
	mid, raw, err := notes.BuildNoteMessage(u.Email, u.DisplayName, title, body, origMID, 2)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	newUID, eerr := a.MailboxSvc.EditNoteAppendExpunge(r.Context(), a.Cfg.IMAPNotesFolder, old.UID, old.MessageID, raw, mid)
	if eerr != nil {
		// EXPUNGE failed (e.g. old UID unrecoverable) — log and
		// continue so the new version is still saved locally. Stale
		// IMAP copy is filtered out by superseded_by below.
		slog.Warn("note edit append-expunge", "err", eerr, "old_mid", old.MessageID)
	}
	_ = a.NotesRepo.MarkSuperseded(r.Context(), old.MessageID, mid)
	_ = a.NotesRepo.Upsert(r.Context(), notes.Note{
		MessageID: mid,
		UID:       newUID,
		AuthorID:  u.ID,
		Title:     title,
		BodyMD:    body,
		Tags:      r.FormValue("tags"),
		Pinned:    old.Pinned,
	})
	row, _ := a.NotesRepo.FindByMessageID(r.Context(), mid)
	// Redirect to the view (not editor) after save so the user sees the
	// rendered markdown.
	http.Redirect(w, r, "/notes/"+row.ID, http.StatusSeeOther)
}

func (a *App) notePin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pinned := r.FormValue("pinned") == "true"
	n, err := a.NotesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := a.MailboxSvc.KeywordSet(r.Context(), a.Cfg.IMAPNotesFolder, n.UID, notes.KeywordPinned, pinned); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.NotesRepo.SetPinned(r.Context(), id, pinned)
	http.Redirect(w, r, "/notes/"+id, http.StatusSeeOther)
}

func (a *App) noteDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := a.NotesRepo.FindByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Move IMAP message to Trash
	_ = a.MailboxSvc.MoveTo(r.Context(), n.ID, a.Cfg.IMAPTrashFolder)
	_ = a.NotesRepo.HardDelete(r.Context(), n.ID)
	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}

// --- settings ---

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Settings(u.DisplayName, a.navCounts(r.Context()), render.SettingsView{
		IMAPHost: a.Cfg.IMAPHost, IMAPPort: a.Cfg.IMAPPort, IMAPUser: a.Cfg.IMAPUsername, IMAPTLS: a.Cfg.IMAPTLS,
		SMTPHost: a.Cfg.SMTPHost, SMTPPort: a.Cfg.SMTPPort, SMTPUser: a.Cfg.SMTPUsername, SMTPTLS: a.Cfg.SMTPTLS,
		NotesFolder: a.Cfg.IMAPNotesFolder,
	}).Render(r.Context(), w)
}

// --- bookmarks ---

func (a *App) bookmarkAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := auth.CurrentUser(r)
	id := chi.URLParam(r, "id")
	ing, err := a.MailboxRepo.FindIngest(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	scope := r.FormValue("scope")
	userID := u.ID
	if scope == "shared" {
		userID = bookmarks.SharedUserID
	}
	if _, err := a.BookmarksRepo.Add(r.Context(), ing.MessageID, bookmarks.KindEmail, userID, r.FormValue("note"), u.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/thread/"+id, http.StatusSeeOther)
}

func (a *App) bookmarkRemove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.BookmarksRepo.Remove(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	back := r.FormValue("back")
	if back == "" {
		back = "/bookmarks"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *App) bookmarksIndex(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	personal, _ := a.BookmarksRepo.ListPersonal(r.Context(), u.ID)
	shared, _ := a.BookmarksRepo.ListShared(r.Context())
	mine := a.bookmarksToRows(r.Context(), personal)
	team := a.bookmarksToRows(r.Context(), shared)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Bookmarks(u.DisplayName, a.navCounts(r.Context()), mine, team).Render(r.Context(), w)
}

func (a *App) bookmarksToRows(ctx context.Context, bs []bookmarks.Bookmark) []render.BookmarkRow {
	out := make([]render.BookmarkRow, 0, len(bs))
	for _, b := range bs {
		ing, err := a.MailboxRepo.FindIngestByMessageID(ctx, b.MessageID)
		if err != nil {
			continue
		}
		out = append(out, render.BookmarkRow{
			ID:         b.ID,
			IngestID:   ing.ID,
			Subject:    mailbox.DecodeHeader(ing.Subject),
			FromName:   mailbox.DecodeHeader(ing.FromName),
			FromAddr:   ing.FromAddr,
			Note:       b.Note,
			ReceivedAt: ing.ReceivedAt,
			CreatedAt:  b.CreatedAt,
		})
	}
	return out
}

// searchClause appends a thread-scoped LIKE filter when q is non-empty.
// Matches threads where ANY message in the thread matches the term.
func searchClause(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return ` AND m.thread_id IN (
  SELECT DISTINCT ms.thread_id FROM mailbox_ingest ms
  WHERE
    LOWER(ms.subject)   LIKE ? OR
    LOWER(ms.from_addr) LIKE ? OR
    LOWER(ms.from_name) LIKE ? OR
    LOWER(ms.body_text) LIKE ? OR
    LOWER(ms.to_addrs)  LIKE ?
)`
}

func searchArgs(viewerID, role, q string, limit int) []any {
	args := []any{viewerID, role, role}
	if strings.TrimSpace(q) != "" {
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like, like, like, like)
	}
	args = append(args, limit)
	return args
}

var _ = strconv.Itoa
var _ = fmt.Sprintf
