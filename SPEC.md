# ORBITAL — webmail spec v0.3

Datastar + Go webmail. Shared IMAP mailbox, multi-user team access, project-scoped email/attachment archive. Patterns mined from `~/code/go/aks/forumchat/internal/mailbox`.

## Goal

A team of N webmail users shares **one** IMAP/SMTP mailbox (support inbox, intake, ops alias). Three coupled features:

1. **Webmail** — read, reply, compose, archive, flag. Full mutation surface (\Seen, \Flagged, MOVE, EXPUNGE).
2. **Projects** — tag a thread into a project; on tag, body + attachments materialise from IMAP into a local archive keyed by **RFC 2822 Message-ID** so server-side folder moves never break the link. Solves "files lost between threads when 5 people email about the same project".
3. **Notes (IMAP-backed)** — Apple Notes-style markdown notepad whose backing store is a dedicated IMAP folder (`WEBMAIL_NOTES_FOLDER`, default `Notes`). Each note = one RFC 2822 message APPEND'd to that folder. Server is source of truth → portable (any IMAP client sees them), free off-site backup, multi-device sync for free. Local mirror table for fast list/search.

## Out of scope (v1)

- Per-user mailboxes (one shared mailbox only)
- Rule-based auto-tag / filter routing (manual project tag only — forumchat's filter pattern is a v1.1 graft)
- Multi-tenant SaaS (single-org install)
- IMAP IDLE push (poll loop is enough at v1)
- Calendar / contacts / server-side labels
- Full-text search (sqlite FTS5 — v1.1)
- E2E client-side encryption (TLS-only)

## Stack

| Concern         | Lib                                                | Note |
|-----------------|----------------------------------------------------|------|
| Lang            | Go 1.25+                                           | matches forumchat |
| Router          | `github.com/go-chi/chi/v5`                         | + `httprate` |
| IMAP            | `github.com/emersion/go-imap/v2`                   | `imapclient` |
| MIME            | `github.com/emersion/go-message`                   | + `/charset` for transcode |
| HTML→text       | `github.com/jaytaylor/html2text`                   | body extract |
| HTML sanitiser  | `github.com/microcosm-cc/bluemonday`               | safe HTML render in thread view |
| Markdown render | `github.com/yuin/goldmark`                         | notes body → HTML |
| SMTP            | stdlib `net/smtp`                                  | TLS / STARTTLS dial |
| SQLite          | `gorm.io/driver/sqlite` + `gorm.io/gorm`           | CGO sqlite (mattn). Pure-Go alt: `github.com/glebarez/sqlite` |
| Migrations      | `github.com/pressly/goose/v3`                      | raw SQL, embedded via `//go:embed migrations/*.sql`; goose owns schema, gorm owns queries — **no AutoMigrate** |
| Sessions        | `github.com/alexedwards/scs/v2`                    | scs cookie store |
| Templating      | `github.com/a-h/templ`                             | tool dep |
| Datastar        | `github.com/starfederation/datastar-go`            | SSE |
| Env config      | `github.com/caarlos0/env/v11` + `joho/godotenv`    | typed struct |
| Logging         | stdlib `log/slog`                                  | JSON in prod, text in dev |
| Password hash   | `golang.org/x/crypto/bcrypt`                       | webmail user pw |
| UUID            | `github.com/google/uuid`                           | v7 preferred for sortable PKs |

## Architecture

Single Go binary. Package layout follows forumchat's flat-package style (not strict hexagonal — easier to navigate):

```
cmd/webmail/main.go             -- wire boot, run server + poll worker
internal/
  config/                        -- env struct, Load()
  auth/                          -- bcrypt users, scs sessions, login handler
  mailbox/                       -- IMAP poll, parse, ingest store (forumchat-style)
    imap.go                      -- THE ONLY file that imports imapclient.*
                                    (CI grep gate enforces this)
    poll.go                      -- PollWorker.cycle → folders → envelopes
    bodyparse.go                 -- ExtractBody, escape, html2text
    decode.go                    -- transfer-encoding + charset transcode
    repo.go                      -- gorm queries over ingest tables
    types.go                     -- Account, Folder, Ingest, Attachment
    service.go                   -- Materialise (tag-to-project), tx orchestration
  send/                          -- SMTP send (APPEND lives in mailbox/imap.go)
    smtp.go                      -- DialTLS/STARTTLS, SendMail
    build.go                     -- BuildMessage → raw RFC 2822 bytes
  projects/                      -- Project, ProjectItem, gorm repo
  uploads/                       -- CAS blobstore (sha256 → fs path)
  httpx/                         -- chi router, middleware, datastar helpers
  render/                        -- templ pages + bluemonday HTML renderer
migrations/
  00001_users.sql                -- goose up/down
  00002_mailbox_account.sql
  …
web/
  static/                        -- the existing html/styles/*.css verbatim
```

### IMAP wrapper invariant

Forumchat enforces a **read-only** contract on its IMAP layer because it's an ingest pipeline that mustn't disturb the user's mail client. **Webmail is the mail client** — it must mutate server-side state: mark `\Seen` when a thread is opened, toggle `\Flagged` when a user stars, `MOVE` on archive/delete, `EXPUNGE` on hard-delete. So we drop the read-only contract.

What we **keep** from forumchat: every `imapclient.*` call lives in a single file (`internal/mailbox/imap.go`). One greppable surface, easy to audit, easy to add new verbs to. Background poll uses `BODY.PEEK[…]` so silent ingest doesn't mark mail seen; foreground user actions use plain `BODY[…]` plus explicit STORE flags. Poll uses EXAMINE on folders we never write to; thread-open uses SELECT on the folder containing the opened message.

Outbound SMTP send appends to `IMAP_SENT_FOLDER` via `APPEND` in the same wrapper file — no separate `send/append.go` carve-out (was a forumchat hack we don't need).

CI gate: `grep -RnE '\bimapclient\.' internal/ | grep -v internal/mailbox/imap.go` must return zero hits.

### IMAP verbs the wrapper exposes

```go
// read (silent — \Seen preserved, used by poll)
dial(cfg) (*imapClient, error)
listFolders() ([]string, error)
examineReadOnly(name) (SelectInfo, error)
fetchEnvelopesSince(uid) ([]FetchedEnvelope, error)
fetchPartPeek(uid, path) ([]byte, error)         // BODY.PEEK[path]

// read (user-initiated — \Seen MAY mark)
selectFolder(name) (SelectInfo, error)            // read-write SELECT
fetchPart(uid, partID) ([]byte, error)            // BODY[partID] — marks \Seen

// write — flag mutations
markSeen(uid, seen bool) error                    // STORE +FLAGS / -FLAGS \Seen
markFlagged(uid, flagged bool) error              // \Flagged
markAnswered(uid) error                           // \Answered (set on reply)

// write — folder mutations
moveMessage(uid uint32, destFolder string) error  // MOVE (RFC 6851) w/ COPY+EXPUNGE fallback
copyMessage(uid uint32, destFolder string) error  // COPY
expungeUID(uid uint32) error                      // UID EXPUNGE (RFC 4315)
deleteMessage(uid uint32) error                   // move to \Trash; hard-delete = MOVE to Trash + EXPUNGE

// write — outbound
appendMessage(folder string, raw []byte) error    // APPEND (called after SMTP send for Sent folder)
```

Per-folder `SelectInfo` cache invalidates whenever a STORE/MOVE touches that folder.

## Configuration (env, loaded with caarlos0/env)

```go
type Config struct {
    Listen        string        `env:"WEBMAIL_LISTEN" envDefault:":8080"`
    BaseURL       string        `env:"WEBMAIL_BASE_URL" envDefault:"http://localhost:8080"`
    DBPath        string        `env:"WEBMAIL_DB_PATH" envDefault:"./data/webmail.db"`
    UploadsDir    string        `env:"WEBMAIL_UPLOADS_DIR" envDefault:"./data/uploads"`
    SessionKey    string        `env:"WEBMAIL_SESSION_KEY,required"`
    SessionMaxAge time.Duration `env:"WEBMAIL_SESSION_MAX_AGE" envDefault:"720h"`

    IMAPHost          string `env:"IMAP_HOST,required"`
    IMAPPort          int    `env:"IMAP_PORT" envDefault:"993"`
    IMAPTLS           string `env:"IMAP_TLS" envDefault:"tls"` // tls | starttls | none
    IMAPUsername      string `env:"IMAP_USERNAME,required"`
    IMAPPassword      string `env:"IMAP_PASSWORD,required"`
    IMAPSentFolder    string `env:"IMAP_SENT_FOLDER" envDefault:"Sent"`
    IMAPTrashFolder   string `env:"IMAP_TRASH_FOLDER" envDefault:"Trash"`
    IMAPArchiveFolder string `env:"IMAP_ARCHIVE_FOLDER" envDefault:"Archive"`
    IMAPNotesFolder   string `env:"IMAP_NOTES_FOLDER" envDefault:"Notes"`

    SMTPHost     string `env:"SMTP_HOST,required"`
    SMTPPort     int    `env:"SMTP_PORT" envDefault:"587"`
    SMTPTLS      string `env:"SMTP_TLS" envDefault:"starttls"`
    SMTPUsername string `env:"SMTP_USERNAME,required"`
    SMTPPassword string `env:"SMTP_PASSWORD,required"`

    PollInterval        time.Duration `env:"POLL_INTERVAL" envDefault:"60s"`
    AttachmentMaxBytes  int64         `env:"ATTACHMENT_MAX_BYTES" envDefault:"26214400"`
}
```

Boot fails fast on missing required keys.

## Auth model

**Shared mailbox, multi-user webmail logins.**

- `users` table: id, email, display_name, password_hash, role (admin|member), created_at.
- No self-signup. Admin CLI: `webmail user add <email>` prompts for password.
- Login → bcrypt verify → scs session cookie → redirect `/inbox`.
- All users see the same inbox (driven from the env IMAP creds).
- Outbound mail uses `From: "<DisplayName>" <user.email>` + `Reply-To: <IMAP_USERNAME>` so replies route back to the shared box. SMTP envelope auth uses the shared `SMTP_USERNAME`.
- `role=admin` can manage projects + users.

## Domain model

Schema in goose migrations; gorm structs annotate columns for query use only (no `AutoMigrate`). Tag with `gorm:"primaryKey"` etc. PKs are UUIDv7 strings.

```go
// internal/auth/types.go
type User struct {
    ID           string    `gorm:"primaryKey"`
    Email        string    `gorm:"uniqueIndex;not null"`
    DisplayName  string
    PasswordHash []byte
    Role         string    // admin | member
    CreatedAt    time.Time
}

// internal/mailbox/types.go — mirror forumchat shape; SINGLE account at v1
type Account struct {
    ID         string `gorm:"primaryKey"`
    Host       string
    Port       int
    Username   string
    TLSMode    string
    LastPollAt *time.Time
    LastError  string
    CreatedAt  time.Time
}

type Folder struct {
    ID          string `gorm:"primaryKey"`
    AccountID   string `gorm:"index;not null"`
    Name        string `gorm:"index;not null"`
    UIDValidity uint32
    LastUID     uint32
    Enabled     bool
    LastSeenAt  *time.Time
    LastError   string
}

type Ingest struct {
    ID          string `gorm:"primaryKey"`
    FolderID    string `gorm:"index;not null"`
    UID         uint32
    UIDValidity uint32
    MessageID   string `gorm:"uniqueIndex;not null"` // RFC 2822 — STABLE
    InReplyTo   string `gorm:"index"`
    References  string                                // space-joined
    ThreadID    string `gorm:"index"`                 // derived from MessageID chain
    Direction   string                                // in | out
    FromAddr    string `gorm:"index"`
    FromName    string
    ToAddrs     string
    CcAddrs     string
    Subject     string
    BodyText    string                                // plain or html2text
    BodyHTML    string                                // sanitised via bluemonday at render

    // Server-side flag mirror (kept in sync via STORE; source of truth = IMAP)
    Seen        bool `gorm:"index"`                   // \Seen
    Flagged     bool `gorm:"index"`                   // \Flagged
    Answered    bool                                  // \Answered
    Draft       bool                                  // \Draft
    Deleted     bool                                  // \Deleted (pre-EXPUNGE)

    ReceivedAt  time.Time `gorm:"index"`
    FetchedAt   time.Time
}

type Attachment struct {
    ID               string `gorm:"primaryKey"`
    IngestID         string `gorm:"index;not null"`
    Filename         string
    MIME             string
    SizeBytes        int64
    MIMEPartID       string    // BODYSTRUCTURE path "2.1" — used for lazy BODY.PEEK
    TransferEncoding string    // base64 | quoted-printable | 7bit | …
    ContentID        string    // for inline `cid:` rewrite
    Inline           bool
    UploadSHA256     string    // empty until materialised
    UploadSize       int64
    MaterialisedAt   *time.Time
    CreatedAt        time.Time
}

// internal/projects/types.go
type Project struct {
    ID          string `gorm:"primaryKey"`
    Slug        string `gorm:"uniqueIndex;not null"`
    Name        string
    Description string
    CreatedBy   string
    CreatedAt   time.Time
}

type ProjectItem struct {
    ID         string `gorm:"primaryKey"`
    ProjectID  string `gorm:"index;not null"`
    MessageID  string `gorm:"index;not null"` // RFC 2822 — folder-agnostic join (works for emails AND notes)
    ItemKind   string                          // email | note
    Note       string
    TaggedBy   string
    TaggedAt   time.Time
}

// internal/notes/types.go — mirror of one APPEND'd note. Source of truth is IMAP.
type Note struct {
    ID         string    `gorm:"primaryKey"`
    MessageID  string    `gorm:"uniqueIndex;not null"` // RFC 2822 — note identity
    UID        uint32                                  // current UID in Notes folder
    AuthorID   string    `gorm:"index"`                 // User.ID (from From: header)
    Title      string                                  // Subject
    BodyMD     string                                  // raw markdown
    BodyHTML   string                                  // rendered via goldmark; cached
    Pinned     bool      `gorm:"index"`                 // mirrors $Pinned IMAP keyword
    Tags       string                                  // space-joined IMAP keywords starting with $note_
    SupersededBy string                                // MessageID of newer version (set during edit)
    CreatedAt  time.Time `gorm:"index"`
    UpdatedAt  time.Time `gorm:"index"`
}
```

**Why `MessageID` (RFC 2822) as project join key**: the IMAP folder, UID, and UIDVALIDITY can all change (user files server-side, server re-numbers, account migration). Message-ID is mint-stamped at origin by the sending MTA and never changes.

## IMAP poll cycle (silent — flags preserved)

`PollWorker.cycle` runs every `POLL_INTERVAL`. Background ingest must **not** mark unread mail seen, so the poll path uses `EXAMINE` + `BODY.PEEK[…]`. User-initiated actions (next section) use `SELECT` + `BODY[…]` + explicit STORE.

One pass:

1. **Dial** `mailbox.dial(cfg)` — `imapclient.DialTLS` / `DialStartTLS` / `DialInsecure` per `IMAP_TLS`. Login. Defer logout+close.
2. **List folders** — `c.List("", "*", nil)`. Filter out:
   - `\Noselect` folders
   - SPECIAL-USE `\All` (Gmail All-Mail = double-ingest of every other folder)
   - **Keep** `\Sent` / `\Drafts` — webmail wants to display these (forumchat excluded them; we don't).
   - **Keep** `\Trash` / `\Junk` — user needs to view/restore. Mark `Folder.role` so UI shows them as system folders.
3. **Per folder**: `c.examineReadOnly(name)` returns `{UIDValidity, UIDNext, NumMessages}`.
   - `UpsertFolder` — if persisted `UIDValidity != server`, reset `last_uid = 0` (re-scan that folder only).
   - Skip if `last_uid >= UIDNext-1` AND a periodic flag-sync isn't due.
4. **Fetch envelopes** for `UID > last_uid` — batch FETCH with `Envelope + InternalDate + BodyStructure{Extended} + Flags`. **No body bytes**. Buffer-then-process.
5. **Per message**:
   - Walk `BodyStructure` → resolve `TextPath` (text/plain ≻ text/html) + attachment `ParsedPart[…]`. Skip `text/*` parts as attachments.
   - `BODY.PEEK[<TextPath>]` → `decodeTextBody(raw, encoding, charset)` (transfer-encoding decode + charset transcode, with sniff fallback for missing headers — Lithuanian `windows-1257`).
   - HTML-only: store HTML in `BodyHTML`; `BodyText = html2text(html)`.
   - **Insert `Ingest`** with `MessageID` unique + flag mirror (`Seen`, `Flagged`, `Answered` from server `Flags`). On Message-ID conflict: UPDATE `FolderID`/`UID`/`UIDValidity` + flag mirror in place.
   - **Insert `Attachment` rows** — metadata only.
   - **Save cursor per-message** (`SetFolderLastUID`) — crash-safe.
6. **Flag re-sync** (every Nth cycle, configurable `FLAG_SYNC_EVERY=10`): batch `FETCH 1:* (UID FLAGS)` per folder, reconcile mirror. Catches changes another mail client made.
7. **Broadcast** to in-process Bus → all open `/inbox` SSE handlers re-fragment.

## Foreground user actions (write path)

The actions UI raises and the wrapper verbs they map to. All foreground sessions are short-lived (open → act → close); no long-held IMAP connections per user.

| User action                  | IMAP verb                                          | Local mirror update          |
|------------------------------|----------------------------------------------------|------------------------------|
| Open thread                  | `SELECT folder` + `BODY[textPath]` (marks \Seen)   | `Ingest.Seen = true`         |
| Mark unread                  | `STORE -FLAGS (\Seen)`                             | `Ingest.Seen = false`        |
| Flag / star                  | `STORE +FLAGS (\Flagged)`                          | `Ingest.Flagged = true`      |
| Unflag                       | `STORE -FLAGS (\Flagged)`                          | `Ingest.Flagged = false`     |
| Reply (after send succeeds)  | `STORE +FLAGS (\Answered)` on original             | `Ingest.Answered = true`     |
| Archive                      | `MOVE` (RFC 6851) to `Archive` folder              | `Ingest.FolderID` updated    |
| Delete (soft)                | `MOVE` to `Trash` folder                           | `Ingest.FolderID` updated    |
| Delete (hard)                | `STORE +FLAGS (\Deleted)` + `UID EXPUNGE`          | `Ingest` row hard-deleted    |
| Move to folder X             | `MOVE` to X                                        | `Ingest.FolderID` updated    |
| Mark thread (all messages)   | repeat STORE/MOVE for each UID in thread           | bulk mirror update           |

`MOVE` graceful-degrades to `COPY` + `STORE +FLAGS (\Deleted)` + `UID EXPUNGE` when the server doesn't advertise MOVE capability.

All foreground writes are **synchronous** — user clicks Star, the request blocks until the IMAP STORE returns, then SSE patches the row. Failure → roll back the local mirror, flash an error chip. No optimistic UI at v1 (small surface, simpler).

### Lazy attachment materialisation (project-tag)

When a user tags a thread into a project (`POST /thread/{messageID}/tag`):

1. Find every `Ingest` in the thread (`WHERE thread_id = ?`).
2. For each `Attachment` row with `MaterialisedAt IS NULL`:
   - Short-lived IMAP session. `EXAMINE` the folder the message lives in — **EXAMINE here is intentional**: tagging shouldn't side-effect `\Seen` on messages the user hasn't actually opened.
   - `BODY.PEEK[<MIMEPartID>]` → raw bytes.
   - `decodeAttachmentBytes(raw, transferEncoding)`.
   - Stream into uploads CAS: `sha256(decoded)` → `UPLOADS_DIR/<hex[:2]>/<hex>`. `os.Rename` from `.tmp` for atomicity. Dedup.
   - Update Attachment row: `UploadSHA256`, `UploadSize`, `MaterialisedAt`.
   - Cap: `SizeBytes > ATTACHMENT_MAX_BYTES` → `UploadSHA256 = "TOO_LARGE"`, UI shows "open in mail client".
3. Insert `ProjectItem` (one per Ingest in the thread).
4. SSE patch the thread page: `{taggedProjects:[...], materialisedAttachments:N}`.

Untag = delete `ProjectItem`. Blobs not GC'd immediately — reference-counted by `ProjectItem` count; daily sweep removes orphans.

## Notes — markdown notepad backed by IMAP folder

Apple-Notes-style personal notepad. Persistence layer = the IMAP `Notes` folder. Each note is one RFC 2822 message APPEND'd to that folder.

### Note message format

```
From: "Display Name" <user@example.com>
To: <IMAP_USERNAME>
Date: <ISO 8601>
Subject: <note title>
Message-ID: <uuidv7@hostname>
X-Webmail-Note: v1
X-Webmail-Note-Version: 1                ; bump on edit
X-Webmail-Note-Original-MID: <orig-mid>  ; only on edits; points at first version
Content-Type: text/markdown; charset=utf-8
Content-Transfer-Encoding: quoted-printable

# title

raw markdown body…
```

- `X-Webmail-Note: v1` is the filter: poll loop treats any message in `Notes` **without** this header as a regular email (gracefully handles people emailing notes-to-self).
- IMAP keywords for state: `$Pinned` mirrors `Note.Pinned`; tags are user-defined keywords prefixed `$note_`.

### Operations

| Action       | IMAP                                                                                          |
|--------------|-----------------------------------------------------------------------------------------------|
| Create note  | `APPEND` to `IMAP_NOTES_FOLDER` with rendered headers + body                                  |
| Edit note    | `APPEND` new version (bump `X-Webmail-Note-Version`, carry `X-Webmail-Note-Original-MID`), then `STORE +FLAGS (\Deleted)` + `UID EXPUNGE` on the old UID |
| Pin / unpin  | `STORE +FLAGS ($Pinned)` / `STORE -FLAGS ($Pinned)`                                           |
| Tag          | `STORE +FLAGS ($note_<slug>)`                                                                 |
| Delete       | `MOVE` to `IMAP_TRASH_FOLDER` (so an accidental delete survives until Trash is emptied)       |

Edit-as-APPEND-plus-EXPUNGE is the only sane shape — IMAP has no UPDATE verb. Concurrent edits resolve last-write-wins; conflict detection deferred to v1.1.

### Sync

- Poll cycle examines `IMAP_NOTES_FOLDER` like any other folder, but routes via `internal/notes/sync.go`:
  - `X-Webmail-Note` header present → upsert into `notes` table.
  - On `X-Webmail-Note-Original-MID` present → also stamp `SupersededBy` on the old `Note` row (matched by that MID).
  - Header absent → fall through to `internal/mailbox` ingest (rare — someone emailed a list with `Notes` in the subject).
- Markdown body rendered via `goldmark` at ingest time; `BodyHTML` cached. Re-render on schema bump.
- All authenticated users see the shared notes (single mailbox = single notes pool). `Note.AuthorID` is informational (from `From:` header), not an ACL.

### Project linkage

Notes can be tagged to projects exactly like emails — `ProjectItem.MessageID` references the note's MID, `ItemKind="note"`. Project view interleaves emails + notes by `CreatedAt`.

## SMTP send + IMAP APPEND

`internal/send/build.go`:

```go
func BuildMessage(cmd SendCommand) (msgID string, raw []byte, err error)
```

- Generates `Message-ID: <uuidv7@hostname>` locally so we can insert the `Ingest` row at send-time and dedup against the poll-back later.
- Headers: `From`, `To`, `Cc`, `Subject`, `Date`, `Reply-To: IMAP_USERNAME`, `In-Reply-To`, `References` (chained for reply).
- MIME: text/plain + text/html alternative; attachments as `multipart/mixed`.

`internal/send/smtp.go`:

- TLS/STARTTLS dial per `SMTP_TLS`.
- `smtp.PlainAuth` + `c.Mail`/`Rcpt`/`Data` (stdlib).

`internal/send/append.go`:

- After `SendMail` returns, append `raw` to `IMAP_SENT_FOLDER`. Best-effort: log error, never propagate.
- Insert local `Ingest{Direction:"out"}` immediately on send success — poll will dedup by MessageID when the server-side copy comes back.

## UI surfaces

Map to existing `/html/*.html` mocks. CSS files (`html/styles/*.css`) served from `/static` verbatim. templ templates port the markup.

| Route                          | templ              | Source mock           |
|--------------------------------|--------------------|-----------------------|
| `GET /login`                   | `login.templ`      | `html/login.html`     |
| `GET /inbox`                   | `inbox.templ`      | `html/inbox.html`     |
| `GET /inbox/stream`            | (SSE)              | new — re-render rows  |
| `GET /thread/{messageID}`      | `thread.templ`     | `html/thread.html`    |
| `GET /compose`                 | `compose.templ`    | `html/compose.html`   |
| `POST /compose/send`           | (SSE)              |                       |
| `GET /p/{slug}`                | `project.templ`    | **new** (style from `pages.css`) |
| `POST /thread/{id}/tag`        | (SSE)              |                       |
| `POST /thread/{id}/seen`       | (SSE)              | mark read/unread      |
| `POST /thread/{id}/flag`       | (SSE)              | toggle \Flagged       |
| `POST /thread/{id}/move`       | (SSE)              | MOVE to folder        |
| `POST /thread/{id}/delete`     | (SSE)              | MOVE → Trash          |
| `GET /attach/{id}`             | stream download    | uses `http.ServeContent` |
| `GET /notes`                   | `notes.templ`      | **new**               |
| `GET /notes/{id}`              | `note.templ`       | **new**               |
| `POST /notes`                  | (SSE)              | create note → APPEND  |
| `POST /notes/{id}`             | (SSE)              | edit → APPEND+EXPUNGE |
| `POST /notes/{id}/pin`         | (SSE)              | toggle $Pinned        |
| `DELETE /notes/{id}`           | (SSE)              | MOVE → Trash          |
| `GET /settings`                | `settings.templ`   | `html/settings.html`  |
| `POST /admin/users`            | (SSE, admin only)  |                       |

Project list sits in left rail (`.rail`) under folders.

### Datastar signal contracts

Names use kebab-case in `data-bind` (forumchat lesson — the JS bridge maps to camelCase signals).

```jsonc
// login
{ email: "", password: "", error: "" }

// inbox
{ filter: "all", search: "", lastSyncAt: "", selectedThreadId: "" }

// thread
{ replyOpen: false, replyText: "", replyAttaching: [],
  projectPicker: false, taggedProjects: [], newProjectName: "" }

// compose
{ to: [], cc: [], subject: "", body: "",
  attachments: [], sending: false, error: "" }

// notes (list)
{ search: "", filter: "all", selectedNoteId: "" }
// notes (editor)
{ title: "", bodyMD: "", pinned: false, tags: [], dirty: false, saving: false }
```

SSE handlers `datastar.NewSSE(w, r)` + `sse.PatchSignals([]byte(\`{...}\`))` for state updates; templ fragments for DOM patches via `sse.PatchElements`.

## Body rendering (security)

- `BodyText` → render as plaintext / markdown (already escaped at ingest via `escapeMarkdownLiterals` from forumchat's `bodyparse.go`).
- `BodyHTML` → run through `bluemonday.UGCPolicy()` at render time. Allow inline images only when the `src` is an `upload://` reference rewritten from `cid:` (forumchat's `RewriteCIDImages` pattern). Strip all external `<img src>` to defeat tracking pixels (toggleable in `/settings`).

## Phases (TDD-first on IMAP/SMTP adapters)

Integration tests run against a real `greenmail` (or `dovecot-test`) container — **no IMAP mocks**.

1. **Skeleton + auth** — chi, scs sessions, `User` CRUD, `webmail user add` CLI, login page, health endpoint.
2. **Persistence** — goose migrations for users + mailbox tables + projects. gorm wiring.
3. **IMAP read** — `dial`, `listFolders`, `examineReadOnly`, `selectFolder`, `fetchEnvelopesSince`, `walkAttachmentParts`, `fetchPartPeek`, `fetchPart`. Tests against greenmail with a seeded MIME corpus (plain, html-alt, base64 PDF, QP Lithuanian subject, multipart inline image).
4. **Poll worker** — `cycle` + `scanFolder` + per-message cursor save + flag-mirror sync. Tests: UIDVALIDITY drift triggers folder rescan; idempotent on Message-ID; folder-move updates row in place; flag changes reconcile.
5. **IMAP write verbs** — `markSeen`, `markFlagged`, `markAnswered`, `moveMessage`, `copyMessage`, `expungeUID`, `appendMessage`. MOVE fallback test on non-MOVE-capable server.
6. **Inbox + thread UI** — `/inbox` list, `/thread/{id}` view (marks \Seen via SELECT+BODY[]), SSE patch on new ingest via Bus, action endpoints (seen/flag/move/delete).
7. **SMTP send** — `BuildMessage`, `SendMail`, `APPEND` to Sent + `STORE +FLAGS (\Answered)` on reply. Compose + reply UI.
8. **Projects + materialise** — Project CRUD, tag dropdown in thread view, `Materialise` service (short-lived IMAP session, `BODY.PEEK[partID]`, `decodeAttachmentBytes`, CAS write).
9. **Notes** — `internal/notes/`: `BuildNoteMessage`, sync routing via `X-Webmail-Note` header, edit-as-APPEND+EXPUNGE, $Pinned + $note_<tag> keywords. `/notes` + `/notes/{id}` UI with goldmark live preview.
10. **Settings + admin** — IMAP/SMTP config display, user admin, tracking-pixel toggle, folder role mapping (Sent/Trash/Archive/Notes overrides).

## Open decisions (v1.1 grafts)

- Filter routing (forumchat's `community_mail_filter` table) for auto-tag-by-From / auto-tag-by-Domain.
- FTS5 full-text search over `BodyText`.
- IMAP IDLE replacing the polling loop (single connection per process).
- Multiple shared mailboxes (lift the singleton `Account` constraint).
- DKIM signing on outbound (mid-term for per-user attribution).

## Reference

- forumchat `internal/mailbox/imap.go` — read-only IMAP wrapper template.
- forumchat `internal/mailbox/poll.go` — cycle loop, UIDVALIDITY tracking, per-message cursor save.
- forumchat `internal/mailbox/decode.go` — transfer-encoding + charset transcode with sniff fallback.
- forumchat `internal/mailbox/bodyparse.go` — html2text + markdown-literal escape.
- forumchat `internal/mailbox/service.go::Materialise` — lazy BODY.PEEK + CAS write.
