// Package mailbox implements the IMAP layer for ORBITAL webmail.
//
// All `imapclient.*` calls live in imap.go; a CI grep gate
// (scripts/check-imap-wrapper.sh) enforces this. The split is what lets
// us audit IMAP behaviour in a single greppable surface.
//
// The poll loop uses EXAMINE + BODY.PEEK[…] so silent background ingest
// never marks \Seen on the user's mail. Foreground actions (open thread,
// flag, archive) use SELECT + BODY[] + explicit STORE.
package mailbox

import "time"

// Account is the singleton row describing the shared IMAP endpoint.
// Populated at boot from env via EnsureAccount.
type Account struct {
	ID         string     `gorm:"primaryKey;column:id"`
	Host       string     `gorm:"column:host"`
	Port       int        `gorm:"column:port"`
	Username   string     `gorm:"column:username"`
	TLSMode    string     `gorm:"column:tls_mode"`
	LastPollAt *time.Time `gorm:"column:last_poll_at"`
	LastError  string     `gorm:"column:last_error"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
}

func (Account) TableName() string { return "mailbox_account" }

// FolderRole tags special-use folders so the UI can render them with the
// right icon and the foreground actions know where to MOVE/APPEND.
const (
	FolderRoleInbox   = "inbox"
	FolderRoleSent    = "sent"
	FolderRoleTrash   = "trash"
	FolderRoleArchive = "archive"
	FolderRoleNotes   = "notes"
	FolderRoleDrafts  = "drafts"
	FolderRoleOther   = ""
)

// Folder holds the per-folder sync cursor. UIDVALIDITY drift on one
// folder triggers a full re-scan of THAT folder only — siblings are
// unaffected.
type Folder struct {
	ID          string     `gorm:"primaryKey;column:id"`
	AccountID   string     `gorm:"column:account_id;index"`
	Name        string     `gorm:"column:name;index"`
	Role        string     `gorm:"column:role"`
	UIDValidity uint32     `gorm:"column:uid_validity"`
	LastUID     uint32     `gorm:"column:last_uid"`
	Enabled     bool       `gorm:"column:enabled"`
	LastSeenAt  *time.Time `gorm:"column:last_seen_at"`
	LastError   string     `gorm:"column:last_error"`
}

func (Folder) TableName() string { return "mailbox_folder" }

// Ingest is one persisted message. Body bytes are stored; attachment
// bytes are NOT (only metadata). Project-tag triggers lazy materialise.
type Ingest struct {
	ID          string `gorm:"primaryKey;column:id"`
	FolderID    string `gorm:"column:folder_id;index"`
	UID         uint32 `gorm:"column:uid"`
	UIDValidity uint32 `gorm:"column:uid_validity"`
	MessageID   string `gorm:"column:message_id;uniqueIndex"`
	InReplyTo   string `gorm:"column:in_reply_to;index"`
	References  string `gorm:"column:refs"`
	ThreadID    string `gorm:"column:thread_id;index"`
	Direction   string `gorm:"column:direction"`
	FromAddr    string `gorm:"column:from_addr;index"`
	FromName    string `gorm:"column:from_name"`
	ToAddrs     string `gorm:"column:to_addrs"`
	CcAddrs     string `gorm:"column:cc_addrs"`
	Subject     string `gorm:"column:subject"`
	BodyText    string `gorm:"column:body_text"`
	BodyHTML    string `gorm:"column:body_html"`

	Seen     bool `gorm:"column:seen;index"`
	Flagged  bool `gorm:"column:flagged;index"`
	Answered bool `gorm:"column:answered"`
	Draft    bool `gorm:"column:draft"`
	Deleted  bool `gorm:"column:deleted"`

	ReceivedAt time.Time `gorm:"column:received_at;index"`
	FetchedAt  time.Time `gorm:"column:fetched_at"`
}

func (Ingest) TableName() string { return "mailbox_ingest" }

// Attachment is metadata-only at poll time. UploadSHA256 is populated
// when a user materialises the attachment by tagging the thread into a
// project. TransferEncoding captures the Content-Transfer-Encoding so
// the lazy fetch can decode the raw BODY.PEEK bytes before CAS save.
type Attachment struct {
	ID               string     `gorm:"primaryKey;column:id"`
	IngestID         string     `gorm:"column:ingest_id;index"`
	Filename         string     `gorm:"column:filename"`
	MIME             string     `gorm:"column:mime"`
	SizeBytes        int64      `gorm:"column:size_bytes"`
	MIMEPartID       string     `gorm:"column:mime_part_id"`
	TransferEncoding string     `gorm:"column:transfer_encoding"`
	ContentID        string     `gorm:"column:content_id"`
	Inline           bool       `gorm:"column:inline"`
	UploadSHA256     string     `gorm:"column:upload_sha256"`
	UploadSize       int64      `gorm:"column:upload_size"`
	MaterialisedAt   *time.Time `gorm:"column:materialised_at"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
}

func (Attachment) TableName() string { return "mailbox_attachment" }

// AccountConfig is the boot-time IMAP configuration passed to dial.
// Mirrors the env keys IMAP_HOST/PORT/USERNAME/PASSWORD/TLS.
type AccountConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLSMode  string
}

// SelectInfo is the subset of imap.SelectData / EXAMINE response the
// poll loop cares about.
type SelectInfo struct {
	UIDValidity uint32
	UIDNext     uint32 // 0 when server didn't send one
	NumMessages uint32
}

// FetchedEnvelope is one message's envelope + attachment metadata, the
// poll worker's unit of work. TextPath is the BODYSTRUCTURE path of the
// best text part (text/plain ≻ text/html), pre-resolved during
// envelopeFromBuffer so the caller can fetch the body bytes with one
// targeted BODY.PEEK[<path>] round-trip.
type FetchedEnvelope struct {
	UID          uint32
	FromAddr     string
	FromName     string
	Subject      string
	MessageID    string
	InReplyTo    string
	References   string
	InternalDate time.Time
	TextPath     []int
	IsTextPlain  bool
	TextEncoding string
	TextCharset  string
	Attachments  []ParsedPart

	// Flag mirror from server response.
	Seen     bool
	Flagged  bool
	Answered bool
	Draft    bool
	Deleted  bool

	// Keywords is every non-system flag — custom keywords like
	// $Pinned, $note_inbox, $Label1, etc. Used by the notes sink to
	// detect Pinned + custom tags.
	Keywords []string
}

// ParsedPart describes one attachment part discovered in BODYSTRUCTURE.
// Bytes are NOT downloaded at poll time — only metadata. MIMEPartID
// matches IMAP's body-part numbering (e.g. "2", "2.1") so the lazy-fetch
// path can request exactly this part later with BODY.PEEK[2.1].
type ParsedPart struct {
	Filename   string
	MIME       string
	SizeBytes  int64
	MIMEPartID string
	Encoding   string
	ContentID  string
	Inline     bool
}
