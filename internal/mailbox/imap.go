package mailbox

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"time"

	gmcharset "github.com/emersion/go-message/charset"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// wordDecoder decodes RFC 2047 encoded-words (e.g.
// `=?iso-8859-2?Q?Mindaugas_=AEvirblis?=` → `Mindaugas Žvirblis`) using
// the full charset.Reader chain (handles Lithuanian windows-1257,
// iso-8859-13, etc.). Wired into imapclient.Options so envelope From
// names and Subjects come back already-decoded.
var wordDecoder = &mime.WordDecoder{CharsetReader: gmcharset.Reader}

// DecodeHeader is the public belt-and-braces decoder used by render
// layer for rows ingested before the WordDecoder wiring landed. Idempotent
// on already-decoded strings.
func DecodeHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	out, err := wordDecoder.DecodeHeader(s)
	if err != nil || out == "" {
		return s
	}
	return out
}

// imapClient is the SINGLE file in the codebase allowed to call
// imapclient.* — every other layer goes through these methods.
// scripts/check-imap-wrapper.sh enforces this with a grep gate.
//
// Read verbs (poll, never mark \Seen): dial, listFolders,
// examineReadOnly, fetchEnvelopesSince, fetchPartPeek.
//
// Read verbs (foreground, MAY mark \Seen): selectFolder, fetchPart.
//
// Write verbs (foreground): markSeen, markFlagged, markAnswered,
// moveMessage (RFC 6851 MOVE with COPY+\Deleted+EXPUNGE fallback),
// copyMessage, expungeUID, appendMessage (used for Sent + Notes).
type imapClient struct {
	c    *imapclient.Client
	caps imap.CapSet
}

// dial opens an authenticated IMAP session. TLS mode follows env config:
// "tls" wraps from start, "starttls" upgrades after greeting, "none"
// speaks plaintext (test only).
func dial(cfg AccountConfig) (*imapClient, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	var (
		c   *imapclient.Client
		err error
	)
	opts := &imapclient.Options{
		TLSConfig:   &tls.Config{ServerName: cfg.Host},
		WordDecoder: wordDecoder,
	}
	switch strings.ToLower(cfg.TLSMode) {
	case "", "tls":
		c, err = imapclient.DialTLS(addr, opts)
	case "starttls":
		c, err = imapclient.DialStartTLS(addr, opts)
	case "none":
		c, err = imapclient.DialInsecure(addr, &imapclient.Options{WordDecoder: wordDecoder})
	default:
		return nil, fmt.Errorf("imap: unknown tls mode %q", cfg.TLSMode)
	}
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	caps, _ := c.Capability().Wait()
	return &imapClient{c: c, caps: caps}, nil
}

func (i *imapClient) close() {
	_ = i.c.Logout().Wait()
	_ = i.c.Close()
}

func (i *imapClient) hasCap(cap imap.Cap) bool {
	if i.caps == nil {
		return false
	}
	return i.caps.Has(cap)
}

// listFolders enumerates SELECT-able mailboxes. Filters out:
//   - \Noselect folders
//   - \All (Gmail All-Mail mirror — every msg appears in INBOX too)
//
// Forumchat also excluded \Sent/\Drafts/\Trash/\Junk because it was an
// ingest pipeline. ORBITAL is a webmail — users need to view those
// folders. They're surfaced with Role tags via DetectRole.
func (i *imapClient) listFolders() ([]folderInfo, error) {
	cmd := i.c.List("", "*", nil)
	datas, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap list: %w", err)
	}
	out := make([]folderInfo, 0, len(datas))
	for _, d := range datas {
		if hasNoselect(d.Attrs) {
			continue
		}
		if hasAllMail(d.Attrs) {
			continue
		}
		out = append(out, folderInfo{
			Name: d.Mailbox,
			Role: detectRole(d.Attrs, d.Mailbox),
		})
	}
	return out, nil
}

// folderInfo is the per-folder discovery result. Returned by
// listFolders; consumed by the poll loop's UpsertFolder.
type folderInfo struct {
	Name string
	Role string
}

func hasNoselect(attrs []imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == imap.MailboxAttrNoSelect {
			return true
		}
	}
	return false
}

func hasAllMail(attrs []imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == imap.MailboxAttrAll {
			return true
		}
	}
	return false
}

// detectRole maps SPECIAL-USE flags to our internal role enum, with a
// name-leaf fallback for servers that don't tag.
func detectRole(attrs []imap.MailboxAttr, name string) string {
	for _, a := range attrs {
		switch a {
		case imap.MailboxAttrSent:
			return FolderRoleSent
		case imap.MailboxAttrDrafts:
			return FolderRoleDrafts
		case imap.MailboxAttrTrash:
			return FolderRoleTrash
		case imap.MailboxAttrArchive:
			return FolderRoleArchive
		case imap.MailboxAttrJunk:
			return FolderRoleTrash // surfaced as trash for UI purposes
		}
	}
	leaf := name
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		leaf = name[idx+1:]
	}
	low := strings.ToLower(leaf)
	switch {
	case low == "inbox":
		return FolderRoleInbox
	case strings.Contains(low, "sent"):
		return FolderRoleSent
	case strings.Contains(low, "draft"):
		return FolderRoleDrafts
	case strings.Contains(low, "trash") || strings.Contains(low, "bin"):
		return FolderRoleTrash
	case strings.Contains(low, "archive"):
		return FolderRoleArchive
	case strings.Contains(low, "junk") || strings.Contains(low, "spam"):
		return FolderRoleTrash
	case strings.Contains(low, "note"):
		return FolderRoleNotes
	}
	return FolderRoleOther
}

// examineReadOnly issues EXAMINE on the named mailbox. The READ-ONLY
// flag is enforced here so the silent poll path never accidentally
// marks \Seen via implicit SELECT.
func (i *imapClient) examineReadOnly(name string) (SelectInfo, error) {
	cmd := i.c.Select(name, &imap.SelectOptions{ReadOnly: true})
	data, err := cmd.Wait()
	if err != nil {
		return SelectInfo{}, fmt.Errorf("imap examine %q: %w", name, err)
	}
	return SelectInfo{
		UIDValidity: data.UIDValidity,
		UIDNext:     uint32(data.UIDNext),
		NumMessages: data.NumMessages,
	}, nil
}

// selectFolder issues SELECT (read-write). Used by foreground actions
// that need to mutate \Seen / \Flagged / etc.
func (i *imapClient) selectFolder(name string) (SelectInfo, error) {
	cmd := i.c.Select(name, nil)
	data, err := cmd.Wait()
	if err != nil {
		return SelectInfo{}, fmt.Errorf("imap select %q: %w", name, err)
	}
	return SelectInfo{
		UIDValidity: data.UIDValidity,
		UIDNext:     uint32(data.UIDNext),
		NumMessages: data.NumMessages,
	}, nil
}

// fetchEnvelopesSince fetches envelope+BODYSTRUCTURE+FLAGS for every
// UID strictly greater than since. Returns ALL envelopes in one batch —
// callers cannot issue another IMAP command while this stream is
// in-flight (single client = single command at a time).
// refsHeaderSection asks for just the References header (envelope only
// carries In-Reply-To; References is fetched separately).
var refsHeaderSection = &imap.FetchItemBodySection{
	Peek:         true,
	Specifier:    imap.PartSpecifierHeader,
	HeaderFields: []string{"References"},
}

func (i *imapClient) fetchEnvelopesSince(since uint32) ([]FetchedEnvelope, error) {
	if since == ^uint32(0) {
		return nil, errors.New("imap: refusing to fetch with overflow since value")
	}
	set := imap.UIDSet{imap.UIDRange{Start: imap.UID(since + 1), Stop: 0}}
	cmd := i.c.Fetch(set, &imap.FetchOptions{
		UID:           true,
		Flags:         true,
		Envelope:      true,
		InternalDate:  true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{refsHeaderSection},
	})
	out := []FetchedEnvelope{}
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, fmt.Errorf("imap fetch envelope: %w", err)
		}
		out = append(out, envelopeFromBuffer(buf))
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap fetch close: %w", err)
	}
	return out, nil
}

// parseReferencesHeader pulls the `References:` value out of a raw
// header blob (e.g. "References: <a@x> <b@y>\r\n") and returns the
// joined message-IDs. Empty when the header is absent.
func parseReferencesHeader(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	s := string(raw)
	const prefix = "References:"
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(prefix):]
	// Stop at the next header line (CRLF + non-whitespace) — folded
	// continuation lines (CRLF + space/tab) belong to References.
	end := len(rest)
	for i := 0; i+1 < len(rest); i++ {
		if rest[i] == '\n' && i+1 < len(rest) && rest[i+1] != ' ' && rest[i+1] != '\t' {
			end = i
			break
		}
	}
	val := strings.TrimSpace(rest[:end])
	// Collapse CRLF folding to a single space.
	val = strings.ReplaceAll(val, "\r\n ", " ")
	val = strings.ReplaceAll(val, "\r\n\t", " ")
	val = strings.ReplaceAll(val, "\n ", " ")
	val = strings.ReplaceAll(val, "\n\t", " ")
	return val
}

// fetchFlagsAll returns the current (UID, Flags) mapping for every
// message in the currently-selected folder. Used by the periodic
// flag-mirror reconciliation pass.
func (i *imapClient) fetchFlagsAll() (map[uint32][]imap.Flag, error) {
	set := imap.UIDSet{imap.UIDRange{Start: 1, Stop: 0}}
	cmd := i.c.Fetch(set, &imap.FetchOptions{UID: true, Flags: true})
	out := map[uint32][]imap.Flag{}
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, fmt.Errorf("imap fetch flags: %w", err)
		}
		out[uint32(buf.UID)] = buf.Flags
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap fetch flags close: %w", err)
	}
	return out, nil
}

// fetchPartPeek fetches a single MIME part WITHOUT marking \Seen
// (BODY.PEEK[…]). Used by:
//   - poll loop body extract,
//   - lazy attachment materialise on project tag.
func (i *imapClient) fetchPartPeek(uid uint32, path []int) ([]byte, error) {
	return i.fetchPartImpl(uid, path, true)
}

// fetchPart fetches a single MIME part WITH \Seen as a side effect
// (BODY[…]). Used by foreground thread-open.
func (i *imapClient) fetchPart(uid uint32, path []int) ([]byte, error) {
	return i.fetchPartImpl(uid, path, false)
}

func (i *imapClient) fetchPartImpl(uid uint32, path []int, peek bool) ([]byte, error) {
	section := &imap.FetchItemBodySection{Peek: peek, Part: append([]int(nil), path...)}
	set := imap.UIDSet{imap.UIDRange{Start: imap.UID(uid), Stop: imap.UID(uid)}}
	cmd := i.c.Fetch(set, &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	})
	var data []byte
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, fmt.Errorf("imap fetch part: %w", err)
		}
		if got := buf.FindBodySection(section); got != nil {
			data = got
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap fetch part close: %w", err)
	}
	return data, nil
}

// fetchPartByID is the dotted-string-id wrapper around fetchPartPeek.
// Materialise stores MIMEPartID as "2.1"; this turns it back into the
// []int path the protocol expects.
func (i *imapClient) fetchPartByID(uid uint32, partID string, peek bool) ([]byte, error) {
	path, err := parsePartPath(partID)
	if err != nil {
		return nil, err
	}
	if peek {
		return i.fetchPartPeek(uid, path)
	}
	return i.fetchPart(uid, path)
}

func parsePartPath(s string) ([]int, error) {
	if s == "" {
		return nil, errors.New("imap: empty part id")
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("imap: bad part id %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

// markSeen flips \Seen on/off via STORE +/- FLAGS.
func (i *imapClient) markSeen(uid uint32, seen bool) error {
	return i.storeFlag(uid, imap.FlagSeen, seen)
}

func (i *imapClient) markFlagged(uid uint32, flagged bool) error {
	return i.storeFlag(uid, imap.FlagFlagged, flagged)
}

// markAnswered sets \Answered (one-way — never unset; that's not what
// the spec means).
func (i *imapClient) markAnswered(uid uint32) error {
	return i.storeFlag(uid, imap.FlagAnswered, true)
}

func (i *imapClient) storeFlag(uid uint32, flag imap.Flag, add bool) error {
	set := imap.UIDSet{imap.UIDRange{Start: imap.UID(uid), Stop: imap.UID(uid)}}
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	cmd := i.c.Store(set, &imap.StoreFlags{
		Op:     op,
		Silent: true,
		Flags:  []imap.Flag{flag},
	}, nil)
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("imap store flag %s: %w", flag, err)
	}
	return nil
}

// storeKeyword adds/removes a custom IMAP keyword (e.g. "$Pinned",
// "$note_<slug>"). Used by the notes layer.
func (i *imapClient) storeKeyword(uid uint32, keyword string, add bool) error {
	return i.storeFlag(uid, imap.Flag(keyword), add)
}

// moveMessage uses RFC 6851 MOVE when the server advertises it; falls
// back to COPY + STORE +FLAGS (\Deleted) + UID EXPUNGE otherwise.
func (i *imapClient) moveMessage(uid uint32, destFolder string) error {
	set := imap.UIDSet{imap.UIDRange{Start: imap.UID(uid), Stop: imap.UID(uid)}}
	if i.hasCap(imap.CapMove) {
		cmd := i.c.Move(set, destFolder)
		if _, err := cmd.Wait(); err != nil {
			return fmt.Errorf("imap move: %w", err)
		}
		return nil
	}
	// Fallback: COPY then STORE +\Deleted then UID EXPUNGE.
	if err := i.copyMessage(uid, destFolder); err != nil {
		return err
	}
	if err := i.storeFlag(uid, imap.FlagDeleted, true); err != nil {
		return err
	}
	return i.expungeUID(uid)
}

func (i *imapClient) copyMessage(uid uint32, destFolder string) error {
	set := imap.UIDSet{imap.UIDRange{Start: imap.UID(uid), Stop: imap.UID(uid)}}
	cmd := i.c.Copy(set, destFolder)
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("imap copy: %w", err)
	}
	return nil
}

// expungeUID removes a single \Deleted-marked message. Uses UID EXPUNGE
// (RFC 4315) when UIDPLUS is advertised; otherwise falls back to plain
// EXPUNGE (which removes ALL \Deleted-marked messages in the folder —
// fine for our usage because we only mark one at a time, but we log a
// warning so multi-delete patterns can be debugged).
func (i *imapClient) expungeUID(uid uint32) error {
	if i.hasCap(imap.CapUIDPlus) {
		set := imap.UIDSet{imap.UIDRange{Start: imap.UID(uid), Stop: imap.UID(uid)}}
		cmd := i.c.UIDExpunge(set)
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("imap uid expunge: %w", err)
		}
		return nil
	}
	cmd := i.c.Expunge()
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("imap expunge: %w", err)
	}
	return nil
}

// appendMessage APPENDs a raw RFC 2822 message to the named folder.
// Called by:
//   - send layer after SMTP send (Sent folder),
//   - notes layer for create/edit (Notes folder).
//
// Flags can be set at append time — pass nil for none.
//
// On TRYCREATE (folder doesn't exist), creates the folder and retries
// once. Lets the user APPEND to Notes / Sent / Archive / Trash on a
// fresh account without manual mailbox setup.
func (i *imapClient) appendMessage(folder string, raw []byte, flags []imap.Flag) error {
	err := i.appendMessageRaw(folder, raw, flags)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "TRYCREATE") && !strings.Contains(strings.ToLower(err.Error()), "doesn't exist") {
		return err
	}
	if cerr := i.createMailbox(folder); cerr != nil {
		return fmt.Errorf("%w (also: %v)", err, cerr)
	}
	// SUBSCRIBE the freshly-created mailbox so Roundcube et al. see it
	// without a manual "show all folders" toggle.
	_ = i.subscribeMailbox(folder)
	return i.appendMessageRaw(folder, raw, flags)
}

func (i *imapClient) appendMessageRaw(folder string, raw []byte, flags []imap.Flag) error {
	opts := &imap.AppendOptions{
		Time:  time.Now().UTC(),
		Flags: flags,
	}
	cmd := i.c.Append(folder, int64(len(raw)), opts)
	if _, err := cmd.Write(raw); err != nil {
		return fmt.Errorf("imap append write: %w", err)
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("imap append close: %w", err)
	}
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("imap append wait: %w", err)
	}
	return nil
}

// createMailbox issues IMAP CREATE for the named folder. Used by
// appendMessage's TRYCREATE retry and by EnsureFolder.
func (i *imapClient) createMailbox(name string) error {
	cmd := i.c.Create(name, nil)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("imap create %q: %w", name, err)
	}
	return nil
}

// subscribeMailbox issues IMAP SUBSCRIBE so the folder shows up in
// clients that filter their tree by the subscribed list (Roundcube,
// Apple Mail with "show all folders" off, mutt with mailboxes
// directive). CREATE alone is not enough on most servers.
func (i *imapClient) subscribeMailbox(name string) error {
	cmd := i.c.Subscribe(name)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("imap subscribe %q: %w", name, err)
	}
	return nil
}

// walkAttachmentParts extracts attachment metadata from a parsed
// BODYSTRUCTURE tree. The protocol numbers parts depth-first starting
// at 1; we mirror that so MIMEPartID is what BODY.PEEK[...] expects.
//
// Heuristics:
//   - text/* leaves are NEVER attachments. They're body parts even when
//     a filename is set (newsletter.html).
//   - Disposition=inline parts are kept only when they carry a
//     Content-ID — the body refers to them via cid: and the rewriter
//     needs an upload target.
//   - Disposition=attachment ALWAYS counts.
//   - Non-text leaf with a filename and no disposition counts.
func walkAttachmentParts(bs imap.BodyStructure) []ParsedPart {
	out := []ParsedPart{}
	if bs == nil {
		return out
	}
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		mime := sp.MediaType()
		if strings.HasPrefix(mime, "multipart/") {
			return true
		}
		if strings.HasPrefix(mime, "text/") {
			return true
		}
		disp := sp.Disposition()
		filename := sp.Filename()
		cid := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(sp.ID), "<"), ">")

		isInline := disp != nil && strings.EqualFold(disp.Value, "inline")
		isAttachment := false
		if disp != nil && strings.EqualFold(disp.Value, "attachment") {
			isAttachment = true
		}
		if filename != "" {
			isAttachment = true
		}
		if isInline && cid != "" {
			isAttachment = true
		}
		if !isAttachment {
			return true
		}
		if filename == "" && cid != "" {
			filename = "inline-" + cid
		}
		out = append(out, ParsedPart{
			Filename:   filename,
			MIME:       mime,
			SizeBytes:  int64(sp.Size),
			MIMEPartID: formatPath(path),
			Encoding:   sp.Encoding,
			ContentID:  cid,
			Inline:     isInline,
		})
		return true
	})
	return out
}

func formatPath(path []int) string {
	if len(path) == 0 {
		return "1"
	}
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// findTextPartPath walks BODYSTRUCTURE for the best text part. Preference:
// text/plain ≻ text/html. Empty path = no usable text part.
func findTextPartPath(bs imap.BodyStructure) ([]int, bool) {
	var plain, html []int
	if bs == nil {
		return nil, false
	}
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		switch sp.MediaType() {
		case "text/plain":
			if plain == nil {
				plain = append([]int(nil), path...)
			}
		case "text/html":
			if html == nil {
				html = append([]int(nil), path...)
			}
		}
		return true
	})
	if plain != nil {
		return plain, true
	}
	if html != nil {
		return html, false
	}
	return nil, false
}

// textPartCodec returns (Content-Transfer-Encoding, charset) for the
// resolved text path so the caller can decode the body bytes.
func textPartCodec(bs imap.BodyStructure, target []int) (encoding, charset string) {
	if len(target) == 0 || bs == nil {
		return "", ""
	}
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		if encoding != "" {
			return false
		}
		if len(path) != len(target) {
			return true
		}
		for i, n := range path {
			if n != target[i] {
				return true
			}
		}
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		encoding = sp.Encoding
		if sp.Params != nil {
			if cs, ok := sp.Params["charset"]; ok {
				charset = cs
			}
		}
		return false
	})
	return encoding, charset
}

func envelopeFromBuffer(buf *imapclient.FetchMessageBuffer) FetchedEnvelope {
	env := FetchedEnvelope{
		UID:          uint32(buf.UID),
		InternalDate: buf.InternalDate,
	}
	if buf.Envelope != nil {
		env.Subject = buf.Envelope.Subject
		env.MessageID = buf.Envelope.MessageID
		env.InReplyTo = strings.Join(buf.Envelope.InReplyTo, " ")
		if len(buf.Envelope.From) > 0 {
			a := buf.Envelope.From[0]
			env.FromName = strings.TrimSpace(a.Name)
			env.FromAddr = strings.ToLower(strings.TrimSpace(a.Addr()))
		}
	}
	if buf.BodyStructure != nil {
		env.Attachments = walkAttachmentParts(buf.BodyStructure)
		env.TextPath, env.IsTextPlain = findTextPartPath(buf.BodyStructure)
		env.TextEncoding, env.TextCharset = textPartCodec(buf.BodyStructure, env.TextPath)
	}
	if refs := buf.FindBodySection(refsHeaderSection); refs != nil {
		env.References = parseReferencesHeader(refs)
	}
	for _, f := range buf.Flags {
		switch f {
		case imap.FlagSeen:
			env.Seen = true
		case imap.FlagFlagged:
			env.Flagged = true
		case imap.FlagAnswered:
			env.Answered = true
		case imap.FlagDraft:
			env.Draft = true
		case imap.FlagDeleted:
			env.Deleted = true
		default:
			env.Keywords = append(env.Keywords, string(f))
		}
	}
	return env
}
