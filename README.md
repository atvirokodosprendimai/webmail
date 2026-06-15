# ORBITAL — self-hosted webmail with projects, bookmarks, IMAP-backed markdown notes, and a built-in issue tracker

<img width="1299" height="732" alt="Screenshot 2026-06-15 at 22 18 44" src="https://github.com/user-attachments/assets/57856eff-e4ef-4894-bc48-e8d1171db4d4" />



> One webmail to read, reply, archive — and turn email chaos into **projects**, **bookmarks for later**, **shared markdown notes** that live inside your own IMAP server, and **issues** with title + assignees + open/closed state. Single Go binary. No JavaScript framework. CGO-free. Datastar-driven UI.

---

## Why another webmail client?

Most webmail clients stop at *read* and *reply*. ORBITAL extends them with **four coupled features** that turn an inbox into a tiny knowledge base:

1. **Projects** — tag a thread into a project. Every body and every attachment is materialised from IMAP into a local content-addressed archive keyed by **RFC 2822 Message-ID**. Server-side folder moves do not break the link.
2. **Bookmarks** — mark a thread for *later* with a personal **or** team-shared scope. Personal = "I'll reply tonight." Team = "Everyone should see this." No reminder needed at v1, just a clean list.
3. **Notes** — Apple-Notes-style markdown notepad **whose backing store is a dedicated IMAP folder** (`Notes` by default). Every note is one RFC 2822 message APPEND'd with a `X-Webmail-Note: v1` header. Open Apple Mail, Thunderbird, or your iPhone client and you see the notes there too — server-side sync, off-site backup, multi-device — all for free. Edits = APPEND new + EXPUNGE old (IMAP has no UPDATE; this is the protocol-correct shape).
4. **Issues** — promote any thread into a tracked issue with a clear title, multiple assignees, open/closed state, and internal markdown notes only visible to your team. The email thread itself stays the conversation surface — internal notes are the "between us" context that never goes over SMTP. Monotonic `#N` numbers per install. One issue per thread (idempotent).

All four are **pointer-only** decorations: they reference the email thread by RFC 2822 Message-ID, so a server-side folder move never breaks the link. Everything sits on one Go binary, one SQLite file, an embedded CSS bundle, and your IMAP credentials. No Electron, no Node, no SaaS account.

---

## Features

### Mail surface

- **Shared mailbox, multi-user** — a single configured IMAP/SMTP account, N webmail users with bcrypt logins. Reply attribution stays per-human via the RFC 5322 `From:` header.
- **Polled background ingest** — every 60 seconds (configurable), per-folder UIDVALIDITY tracked, per-message cursor saved (crash mid-batch never re-ingests). Auto-discovers your folder layout via IMAP LIST + SPECIAL-USE.
- **Foreground actions** — open thread (marks `\Seen`), star (`\Flagged`), reply (sets `\Answered`), archive (MOVE), delete (MOVE to Trash), hard-delete (UID EXPUNGE).
- **Charset & encoding** — RFC 2047 encoded-word decode (Lithuanian `iso-8859-2`, Russian `koi8-r`, Polish `windows-1250`, German `iso-8859-15` and friends), quoted-printable + base64 transfer-decoding with sniff fallback for servers that omit `Content-Transfer-Encoding`, charset transcode via `go-message/charset`.
- **Threading** — by RFC 2822 `Message-ID`, `In-Reply-To`, and `References`. Inbox groups by thread, shows thread length chip.
- **Reply with quoted history** — textarea pre-fills with `On <date>, <name> wrote: > ...` so the recipient sees context.
- **Send with proper headers** — `Message-ID`, `From`, `Reply-To`, `In-Reply-To`, `References` all wrapped in `<>` per RFC 5322 §3.6.4 so Gmail / Outlook / Apple Mail actually thread the reply on the recipient side.
- **APPEND to Sent** — outbound mail lands in your IMAP Sent folder automatically. If the folder doesn't exist, `appendMessage` auto-CREATEs and retries (catches the `[TRYCREATE]` response).
- **MOVE with fallback** — uses RFC 6851 MOVE when the server advertises it; gracefully degrades to `COPY` + `STORE +FLAGS (\Deleted)` + `UID EXPUNGE` on older servers.

### Projects

- Tag any thread into a project.
- On tag, the body **and every attachment in the whole thread** materialise from IMAP into a content-addressed filesystem store (`sha256` → `<root>/<hex[:2]>/<hex>`). Dedup is automatic: the same PDF attached across N projects = one file on disk.
- Inline `cid:` references in HTML bodies get rewritten to `upload://` pointers for the local renderer.
- The project view interleaves emails and notes by date.
- The join key is `Message-ID` (folder-agnostic) so users moving emails in Apple Mail / Outlook / Thunderbird does not break project tags.

### Bookmarks (new)

- Per-user **and** team-shared scopes, single table, sentinel empty-string user_id for shared rows.
- Optional note ("draft answer in my head" / "needs legal review").
- `/bookmarks` page split into "Mine" and "Team" sections.
- 🔖 chip on bookmarked threads in inbox view, surfaced via SQL `EXISTS` subquery over the thread's message-ids.
- Idempotent UPSERT — clicking "Bookmark" twice doesn't create duplicates.

### Issues

- **Promote any thread** with one click ("Convert to issue" in the thread view).
- **Title + multiple assignees** picked from local users; assigned-to-me filter on the list.
- **States: open / closed** — no transition workflow, just toggle.
- **Two conversation surfaces**: the email thread for customer-facing replies, plus internal markdown notes on the issue row that NEVER leave your DB.
- **Monotonic `#N` numbers** allocated via a singleton counter table — race-free under concurrent creation.
- **One issue per thread** — UNIQUE on `message_id`. Clicking "Convert to issue" on an already-promoted thread redirects to the existing one.
- **Thread banner** — once promoted, the thread page shows a chip linking to `/issues/{id}` so you always see status + open the issue.
- Independent from projects (a thread can be tagged into a project AND promoted to an issue; the two systems don't share state).

### Notes (IMAP-backed markdown notepad)

- Each note = one RFC 2822 message APPEND'd to `IMAP_NOTES_FOLDER` (default `Notes`).
- Headers route the message:
  - `X-Webmail-Note: v1` — identifies notes vs accidentally-emailed reminders.
  - `X-Webmail-Note-Version: N` — bumps on edit.
  - `X-Webmail-Note-Original-MID: <root-mid>` — points at the first version so chains are reconstructable.
- Body = quoted-printable encoded markdown.
- Rendered via `goldmark` at sync time; HTML cached on the row.
- IMAP keywords carry state:
  - `$Pinned` — mirrors the UI pin toggle.
  - `$note_<slug>` — user-defined tags.
- Edit = APPEND new version + `STORE +FLAGS (\Deleted)` + `UID EXPUNGE` on the old UID. IMAP has no UPDATE; this is the only sane shape.
- Delete = MOVE to Trash (recoverable until Trash is emptied).
- Multi-device: open the same `Notes` folder in any IMAP client and the notes are there. Free off-site backup via your mail provider's normal backup policy.

### Security & data

- TLS / STARTTLS for IMAP and SMTP independently configurable.
- bcrypt password hashes for local logins (default cost 10).
- `scs/v2` session cookies, SameSite=Lax, HttpOnly, backed by a SQLite store so sessions survive restarts.
- Attachments served with explicit `Content-Type` and `Content-Disposition: attachment; filename=...`; `http.ServeContent` for range support.
- CI grep gate (`scripts/check-imap-wrapper.sh`) forbids `imapclient.*` calls outside `internal/mailbox/imap.go` — every IMAP verb is auditable in one file.
- `make clean` only removes build artifacts; the destructive `make reset-db RESET=yes` exists but requires the explicit flag.

---

## Stack

| Layer            | Library                                                  | Notes |
|------------------|----------------------------------------------------------|-------|
| Language         | Go 1.25+                                                 | |
| HTTP router      | `go-chi/chi/v5` + `httprate`                             | |
| IMAP client      | `emersion/go-imap/v2`                                    | v2 beta, single-wrapper file |
| MIME parser      | `emersion/go-message` + `/charset`                       | every common encoding |
| HTML → text      | `jaytaylor/html2text`                                    | for body indexing |
| HTML sanitiser   | `microcosm-cc/bluemonday`                                | UGC policy for rendering |
| Markdown render  | `yuin/goldmark`                                          | for notes |
| Database         | `glebarez/sqlite` (pure Go) + `gorm.io/gorm`             | **CGO-free**, single binary |
| Migrations       | `pressly/goose/v3`                                       | embedded via `//go:embed`, **no `AutoMigrate`** |
| Sessions         | `alexedwards/scs/v2` + gorm-backed Store                 | survives restart |
| Templating       | `a-h/templ`                                              | type-safe HTML |
| SSE              | `starfederation/datastar-go`                             | reactive UI without npm |
| Config           | `caarlos0/env/v11` + `joho/godotenv`                     | typed struct, fail-fast on missing required vars |
| Password hash    | `golang.org/x/crypto/bcrypt`                             | |
| UUID             | `google/uuid`                                            | v7 sortable PKs |
| Build tools      | `go tool templ generate`, `go tool goose ...`            | no `$PATH` install needed |

**JS payload at runtime: only the Datastar bundle from `cdn.jsdelivr.net`. No framework. No npm. The whole UI is templ-rendered HTML + a sprinkle of `data-*` attributes.**

---

## Quick start

```bash
git clone git@github.com:atvirokodosprendimai/webmail.git
cd webmail
cp .env.example .env

# generate a 32-byte session key
KEY=$(openssl rand -hex 32)
sed -i '' "s/dev-only-change-me-32-bytes-min!!/${KEY}/g" .env

# fill in IMAP_HOST / IMAP_USERNAME / IMAP_PASSWORD / SMTP_*
$EDITOR .env

# build
go tool templ generate
go build -o bin/webmail ./cmd/webmail

# create the first user (admin)
./bin/webmail user add you@example.com "Your Name" --admin

# run
./bin/webmail
```

Open <http://localhost:8080/>. Log in. Wait one poll cycle (60 s by default) for your inbox to populate.

### Docker / docker compose

The recommended deployment is via `compose.yml` against the
prebuilt image at `ghcr.io/atvirokodosprendimai/webmail:latest`:

```bash
cp .env.example .env
# (fill in IMAP / SMTP creds + generate a 32-byte session key)
docker compose up -d
```

The image is built on `distroless/static-debian12:nonroot`. CSS, SQL
migrations, and the templ output are all `//go:embed`'d into the
binary — nothing outside `/app/webmail` and the volume-mounted
`/data`.

---

## Creating users

There is **no self-signup** — admins seed every user via the CLI.

### Local binary

```bash
./bin/webmail user add you@example.com "Your Name" --admin
```

You'll be prompted for a password twice (8+ chars). The flag flags:

- `--admin` — grants admin role (can manage other users in `/settings`).
  Without it, the role is `member`.
- Second positional arg is the display name (defaults to the email).

Add a second member:

```bash
./bin/webmail user add alice@example.com "Alice"
```

### Docker compose

Run the CLI inside a one-shot container against the same image:

```bash
docker compose run --rm webmail /app/webmail user add you@example.com "Your Name" --admin
```

This boots a new container, runs the CLI subcommand against the
mounted `./data/webmail.db`, then exits. The persistent container
keeps running.

### Plain docker

```bash
docker run --rm -it \
  --env-file .env \
  -v $(pwd)/data:/data \
  -e WEBMAIL_DB_PATH=/data/webmail.db \
  ghcr.io/atvirokodosprendimai/webmail:latest \
  /app/webmail user add you@example.com "Your Name" --admin
```

The `-it` flags are required so `term.ReadPassword` can prompt
on a real tty. The same volume mount is needed so the new user
lands in the same database the main container uses.

### Listing / removing users

User list and removal are managed from the **/settings** admin
page once an admin user is signed in. v1 doesn't ship a CLI
counterpart — the admin UI is the supported surface.

---

## Configuration

Every knob is an env var (loaded via `caarlos0/env/v11`). Required keys fail-fast at boot.

| Key                     | Default                       | Notes |
|-------------------------|-------------------------------|-------|
| `WEBMAIL_LISTEN`        | `:8080`                       | HTTP listen address |
| `WEBMAIL_BASE_URL`      | `http://localhost:8080`       | for generating absolute links |
| `WEBMAIL_DB_PATH`       | `./data/webmail.db`           | sqlite, WAL journal, foreign-keys on |
| `WEBMAIL_UPLOADS_DIR`   | `./data/uploads`              | content-addressed attachment CAS |
| `WEBMAIL_SESSION_KEY`   | **required**                  | 32+ bytes, `openssl rand -hex 32` |
| `WEBMAIL_SESSION_MAX_AGE` | `720h`                      | 30 days default |
| `IMAP_HOST`             | **required**                  | e.g. `imap.gmail.com` |
| `IMAP_PORT`             | `993`                         | |
| `IMAP_TLS`              | `tls`                         | `tls` \| `starttls` \| `none` |
| `IMAP_USERNAME`         | **required**                  | the shared mailbox account |
| `IMAP_PASSWORD`         | **required**                  | use an app password for Gmail / iCloud |
| `IMAP_SENT_FOLDER`      | `Sent`                        | outbound APPEND target |
| `IMAP_TRASH_FOLDER`     | `Trash`                       | MOVE target for delete |
| `IMAP_ARCHIVE_FOLDER`   | `Archive`                     | MOVE target for archive |
| `IMAP_NOTES_FOLDER`     | `Notes`                       | the markdown notepad's home |
| `SMTP_HOST`             | **required**                  | |
| `SMTP_PORT`             | `587`                         | |
| `SMTP_TLS`              | `starttls`                    | `tls` \| `starttls` \| `none` |
| `SMTP_USERNAME`         | **required**                  | used as envelope `MAIL FROM:` |
| `SMTP_PASSWORD`         | **required**                  | |
| `POLL_INTERVAL`         | `60s`                         | how often to fetch new mail |
| `FLAG_SYNC_EVERY`       | `10`                          | reconcile `\Seen`/`\Flagged` every Nth cycle |
| `ATTACHMENT_MAX_BYTES`  | `26214400` (25 MiB)           | over-cap = "open in mail client" |
| `MIGRATE_ON_BOOT`       | `true`                        | run goose up at startup |

---

## Architecture

```
cmd/webmail/main.go               -- wires everything; graceful shutdown
internal/
  config/                          -- caarlos0/env Load()
  auth/                            -- bcrypt users, scs sessions, gorm store
  db/                              -- gorm.Open + goose-managed schema
  mailbox/                         -- ONE-FILE imap wrapper + decode + poll
    imap.go                        -- every imapclient.* call lives here
    poll.go                        -- per-folder cycle + cursor save
    decode.go                      -- RFC 2047 + transfer + charset
    bodyparse.go                   -- html2text + markdown escape
    repo.go                        -- gorm queries
    service.go                     -- foreground command surface
    bus.go                         -- in-process SSE fan-out
  send/
    build.go                       -- BuildMessage (raw RFC 2822)
    smtp.go                        -- DialTLS/STARTTLS + auth
  projects/                        -- Project + Item gorm + tag flow
  bookmarks/                       -- Bookmark gorm + Add/Remove/List
  issues/                          -- Issue + Assignee + Counter; CreateFromThread
  notes/                           -- BuildNoteMessage + goldmark render
  uploads/                         -- CAS blobstore (sha256)
  render/                          -- templ pages + components
  httpx/                           -- chi router + handlers + embed.FS
internal/db/migrations/            -- 00001..0001N SQL, embedded
web/static/styles/                 -- the ORBITAL design system CSS
scripts/check-imap-wrapper.sh      -- CI grep gate
```

### The IMAP read-only / write split

Background ingest must **not** disturb flag state on the user's other mail clients, so the poll loop uses `EXAMINE` + `BODY.PEEK[...]`. Foreground actions (open thread, star, archive) use `SELECT` + `BODY[...]` + explicit `STORE`. All of this routes through the single `internal/mailbox/imap.go` wrapper.

### The notes-as-IMAP-folder trick

Notes are real RFC 2822 messages in a real IMAP folder. That gives you:

- Multi-device sync via any IMAP client (Apple Mail, Thunderbird, K-9, Outlook, mutt, ...).
- Off-site backup via your provider's normal mailbox backup policy.
- Editable from a phone in airplane mode (the iPhone IMAP client queues APPEND, the next sync resolves it).
- No vendor lock-in: export your notes by literally copying the IMAP folder.

The downside (IMAP has no UPDATE verb, so edit = APPEND + EXPUNGE) is hidden by the UI; you click Save, the new version lands and the old one quietly disappears.

---

## Roadmap

- Greenmail-based integration test harness (TDD against real IMAP semantics).
- Real Datastar fragments on `/inbox/stream` (currently a placeholder ping).
- `bluemonday` sanitiser on inbound HTML body render (currently renders raw via `templ.Raw`; XSS-aware sender ⇒ XSS — fix in flight).
- Multipart compose with attachments (currently text only).
- Search via `sqlite-fts5`.
- Rule-based auto-tag (sender / domain → project).
- Multiple shared mailboxes per install.
- DKIM signing for per-user attribution on outbound.
- IMAP IDLE replacing the polling loop.

---

## FAQ

### Is this Gmail / Outlook / Fastmail compatible?

Yes. ORBITAL is a regular IMAP / SMTP client. It works with anything that speaks the protocol: Gmail (with an app password), iCloud, Fastmail, Migadu, Purelymail, your company Exchange server, a Postfix/Dovecot pair you set up yourself, or a `greenmail` container for tests.

### Can I run this for a team / family?

Yes. The auth model is *one shared mailbox, many local webmail logins*. Each user has their own bcrypt-hashed login (admin seeds them via `./bin/webmail user add`); everyone sees the same inbox, sent folder, notes, and bookmarks (with the shared scope).

### Is it really CGO-free?

Yes. We use `glebarez/sqlite` (a pure-Go port of `modernc.org/sqlite`) under `gorm.io/driver/sqlite`. `CGO_ENABLED=0 go build` produces a single static binary that runs on `distroless/static`.

### What's the difference between a project, a bookmark, and an issue?

All three are pointer-only decorations on top of an email thread. Pick by intent:

- **Project** = a *bucket* of related threads + materialised attachments. Long-lived, shared, with a description. Good for "everything about the Acme Corp launch".
- **Bookmark** = a *note-to-self or note-to-team* for later. No state, no metadata, just "remember this". Personal or shared scope.
- **Issue** = a *tracked task* with title, assignees, and open/closed state. Use when "who's doing this?" matters and you want to know when it's done.

A thread can be all three at once — they're independent.

### Where do my notes go?

Into your IMAP server, in `IMAP_NOTES_FOLDER` (default `Notes`). One note = one email-shaped message with a `X-Webmail-Note: v1` header. You can read them from any IMAP client; if you delete ORBITAL entirely, the notes survive in your mailbox.

### What happens to projects if I move emails server-side?

Nothing breaks. Projects join on RFC 2822 `Message-ID`, which is set by the sending MTA and never changes. The folder, UID, and even UIDVALIDITY can shift; the project tag and any materialised attachments stay valid.

### Does it scale?

ORBITAL is a *personal-to-small-team* webmail. Sqlite + a single Go binary + a polled IMAP loop is enough for thousands of messages, hundreds of projects, and a handful of users. For tens of users on one shared mailbox, fine. For multi-tenant SaaS, you want a different tool.

### What about IMAP IDLE / push?

Roadmap. v1 polls every minute. The latency is fine for human use; the cost is a server-side flag-sync round-trip every Nth cycle.

### How are attachments stored?

Content-addressed by SHA-256 under `WEBMAIL_UPLOADS_DIR`. Path = `<root>/<hex[:2]>/<hex>`. The same PDF attached across N projects = one file on disk. Atomic writes via `os.Rename` from a `.tmp` sibling. Range requests via `http.ServeContent`.

### Is there an API?

The HTTP handlers are HTML-first (form posts + SSE). A JSON layer is *not* a v1 priority — the whole point is to keep the surface small and inspectable. If you need automation, drive the IMAP server directly.

### Why Datastar instead of HTMX / Alpine / React?

Server-rendered HTML + a few `data-*` attributes for reactivity covers everything we need. No build step. No `node_modules`. No FOUC on first render. The binary is the deployable artefact.

### What licence?

**GNU Affero General Public License v3.0 (AGPL-3.0)**. Use, modify, and self-host ORBITAL freely. If you run a **modified** version as a network service (i.e. a SaaS offering), you must publish the source of your modifications to its users under the same licence (AGPL §13, the "network use" clause). Self-hosting for your own team or company is fully permitted; copyleft only kicks in when you serve modified code to others over a network.

For proprietary integration or different terms, contact the maintainers — dual licensing is possible.

---

## Search-friendly summary

If you arrived here looking for any of these — yes, ORBITAL is what you want:

> Self-hosted webmail. Open-source webmail client written in Go. IMAP web client. SMTP send. Markdown notes in IMAP folder. Apple Notes alternative on IMAP. Shared team inbox webmail. Multi-user webmail single mailbox. Webmail with projects. Webmail with bookmarks. Webmail with issue tracker. Email to issue. GitHub Issues alternative for email teams. Self-hosted issue tracker. Per-thread issue tracker. Issue tracker with multiple assignees. Open close issue lifecycle. Internal notes on issues. Reply later webmail. Save email for later. Save email to project. Materialise attachments. Content-addressed attachment store. RFC 2047 encoded-word decoder. Lithuanian / Polish / German email charset support. Datastar webmail. templ webmail. Pure-Go SQLite webmail. CGO-free webmail. Single binary webmail. Distroless docker webmail. Goose migrations webmail. scs sessions webmail. RFC 6851 MOVE fallback webmail. UIDVALIDITY tracking webmail. Per-folder cursor poll webmail. RFC 2822 Message-ID threading. Folder-move resilient project tagging. Edit-as-APPEND-EXPUNGE notes. X-Webmail-Note custom header. $Pinned IMAP keyword. $note_ custom keyword. bluemonday sanitiser HTML email rendering. goldmark markdown notes. CI grep gate IMAP wrapper. Read-only EXAMINE vs read-write SELECT. BODY.PEEK silent ingest. Per-message cursor save crash-safe poll. Flag mirror reconciliation. mime.WordDecoder iso-8859-2 windows-1257 charset chain. go-message/charset reader. RFC 5322 Address quoting. envelope MAIL FROM SMTP_USERNAME. APPEND TRYCREATE auto-create. RFC 4315 UIDPLUS expunge. IMAP gorm sqlite glebarez. Linux webmail. macOS webmail. ARM webmail. Raspberry Pi webmail. Homelab webmail. NixOS webmail. Privacy-focused webmail self-hosted. No tracker no analytics no JavaScript framework webmail. Static binary deployment webmail.

That paragraph is for the search engines, not for you. Skip it if you got here from a real link.

---

## Acknowledgements

ORBITAL's IMAP layer was mined from the open-source `forumchat` project's read-only ingest pipeline (`internal/mailbox`), then extended with the full mutation surface (`STORE` / `MOVE` / `EXPUNGE` / `APPEND` / `CREATE`) a webmail client needs. The single-wrapper-file pattern + CI grep gate came across verbatim.

Design system mocks (`/html/*.html` + `/html/styles/*.css`) port directly into `internal/render/*.templ`. The CSS is `//go:embed`'d so the binary is self-contained.

---

## License

**[GNU Affero General Public License v3.0](./LICENSE)** (AGPL-3.0).

ORBITAL is free software:

- ✅ Use it for any purpose, including commercial.
- ✅ Modify it.
- ✅ Self-host it for your team / company / family.
- ⚠️ If you offer a **modified** version as a network service (SaaS), you must publish your source modifications to those users under the same AGPL-3.0 licence — the "network use" clause of AGPL §13.
- ⚠️ Derivative works distributed in any form must also be AGPL-3.0 (or a compatible later version).

For proprietary or non-AGPL terms, open an issue — dual licensing is on the table.

Copyright © 2026 Atvirų Kodų Sprendimai and contributors.

```
SPDX-License-Identifier: AGPL-3.0-or-later
```
