---
tldr: Build ORBITAL — a Go+Datastar webmail (shared IMAP/SMTP mailbox, multi-user), with project tagging that materialises emails+attachments to a local archive, and an Apple-Notes-style markdown notepad backed by a dedicated IMAP folder. 10 phases, TDD against greenmail.
status: active
---

# Plan: ORBITAL webmail (IMAP + SMTP + projects + notes)

## Context

- Spec: [[SPEC]] (/Users/mind/code/ai/webmail/SPEC.md v0.3)
- Design system mocks: `/html/*.html` + `/html/styles/*.css` — port to templ verbatim
- Reference impl mined for IMAP patterns: `~/code/go/aks/forumchat/internal/mailbox`
  - read-only wrapper contract → adapted (we drop read-only, keep single-file wrapper)
  - EXAMINE+BODY.PEEK for silent poll; SELECT+BODY[] for foreground
  - per-folder UIDVALIDITY tracking, per-message cursor save
  - decodeTextBody / decodeAttachmentBytes with transfer-encoding + charset sniff
  - lazy materialise via BODY.PEEK[MIMEPartID]
  - inline cid: rewrite
- Mempalace drawers: `wing=webmail, room=decisions` (v0.2 + v0.3 deltas)
- Stack locked: Go 1.25 · chi · go-imap/v2 · go-message · gorm + glebarez or driver/sqlite · goose v3 · scs/v2 · caarlos0/env/v11 · a-h/templ · starfederation/datastar-go · bluemonday · goldmark · jaytaylor/html2text · bcrypt

## Phases

### Phase 0 - Repo bootstrap - status: completed

1. [x] `go mod init github.com/atvirokodosprendimai/webmail` + add deps from spec stack table
   - => module path picked by user (matches forumchat org)
   - => deps installed via `go get`: chi, httprate, go-imap/v2, go-message, glebarez/sqlite, gorm, goose, scs/v2, caarlos0/env, godotenv, templ, datastar-go, bluemonday, goldmark, html2text, bcrypt, uuid
   - => templ + goose added as `go tool` entries (no separate install)
2. [x] Project layout dirs (`cmd/webmail`, `internal/{config,auth,mailbox,send,projects,notes,uploads,httpx,render,db}`, `migrations`, `web/static/styles`, `scripts`)
   - => `web/static/styles/*.css` populated by copy of `html/styles/*.css` (Phase 1 will embed via //go:embed)
3. [x] `.env.example` matching `Config` struct in SPEC §Configuration
   - => includes new keys IMAP_TRASH_FOLDER, IMAP_ARCHIVE_FOLDER, IMAP_NOTES_FOLDER, FLAG_SYNC_EVERY
4. [x] `Makefile` with `dev` / `build` / `test` / `migrate-up` / `migrate-down` / `templ-gen` / `lint` / `ci-imap-gate` / `clean`
   - => uses `go tool goose` + `go tool templ` (no PATH dep)
5. [x] `Dockerfile` (multi-stage; CGO-free via glebarez/sqlite)
   - => single binary; CSS + migrations embedded via //go:embed (Phase 1/2 wires this)
6. [x] CI grep gate script (`scripts/check-imap-wrapper.sh`) — fails if `imapclient.` appears outside `internal/mailbox/imap.go`
   - => smoke test passes on empty internal/ tree

### Phase 1 - Skeleton + auth - status: open

1. [ ] `internal/config/config.go` — caarlos0/env Load(); fail-fast on missing required
   - => verify boot with .env.example + missing-var error path
2. [ ] `internal/httpx/server.go` — chi router, health endpoint, static file mount via `//go:embed web/static/*` → `http.FS(embedded)` (no disk read at runtime)
3. [ ] `internal/auth/{repo,session,handler}.go` — bcrypt User + scs cookie session + login/logout handlers
4. [ ] `cmd/webmail/main.go` — wire boot, run server, graceful shutdown on SIGTERM
5. [ ] CLI subcommand `webmail user add <email>` — stdin password prompt, bcrypt hash, insert row
6. [ ] Port `html/login.html` → `internal/render/login.templ`, wire `GET /login` + `POST /login`
   - => Phase 1 result: visit `/login`, sign in, land on a stub `/inbox` page

### Phase 2 - Persistence (goose schema + gorm queries) - status: open

1. [ ] `migrations/00001_users.sql` (id, email unique, display_name, password_hash, role, created_at)
2. [ ] `migrations/00002_mailbox_account.sql` + `00003_mailbox_folder.sql` (UIDValidity, LastUID, Role enum)
3. [ ] `migrations/00004_mailbox_ingest.sql` + `00005_mailbox_attachment.sql` (Message-ID unique, flag mirror cols, MIMEPartID, TransferEncoding)
4. [ ] `migrations/00006_projects.sql` + `00007_project_items.sql` (`ItemKind` email|note)
5. [ ] `migrations/00008_notes.sql` (mirror of IMAP Notes folder)
6. [ ] `internal/db/db.go` — open gorm with goose-managed schema (no AutoMigrate); embed migrations via `//go:embed`; `MIGRATE_ON_BOOT=true` flag
7. [ ] Goose migrate up at boot; verify with `sqlite3 data/webmail.db .schema`

### Phase 3 - IMAP read primitives - status: open

1. [ ] `internal/mailbox/imap.go` — single wrapper file, verbs from SPEC §"IMAP verbs the wrapper exposes" (read half: dial, listFolders, examineReadOnly, selectFolder, fetchEnvelopesSince, fetchPartPeek, fetchPart)
2. [ ] `internal/mailbox/decode.go` — port forumchat's decodeTextBody, decodeAttachmentBytes, looksLikeBase64, looksLikeQuotedPrintable, charset reader chain
3. [ ] `internal/mailbox/bodyparse.go` — ExtractBody (html2text fallback) + escapeMarkdownLiterals
4. [ ] `internal/mailbox/types.go` — Account, Folder, Ingest, Attachment, FetchedEnvelope, ParsedPart
5. [ ] Spin up `greenmail` via docker-compose for integration tests; seed corpus (plain, html-alt, base64 PDF, QP Lithuanian subject, multipart inline image, missing-CTE base64)
6. [ ] `imap_test.go` — listFolders excludes \All; examineReadOnly returns UIDValidity; fetchEnvelopesSince batches; fetchPartPeek doesn't mark \Seen
   - => Phase 3 result: `go test ./internal/mailbox/...` green vs live greenmail

### Phase 4 - Poll worker + ingest - status: open

1. [ ] `internal/mailbox/poll.go` — PollWorker.{Start, run, cycle, scanFolder} ported from forumchat
2. [ ] `internal/mailbox/repo.go` — UpsertFolder (UIDValidity drift → reset LastUID=0), InsertIngest (UPSERT on Message-ID), InsertAttachments, SetFolderLastUID
3. [ ] Per-message cursor save inside scan loop (not after batch) — crash-safe property test
4. [ ] Thread derivation: walk References + In-Reply-To chain → assign ThreadID
5. [ ] Flag-mirror sync (every `FLAG_SYNC_EVERY=10` cycles) — `FETCH 1:* (UID FLAGS)` per folder, reconcile Seen/Flagged/Answered
6. [ ] Bus broadcast on new ingest → SSE handlers re-fragment
7. [ ] Wire PollWorker into `cmd/webmail/main.go` with context cancellation
   - => Phase 4 result: seeded greenmail messages appear in `Ingest` table; folder-move test (greenmail MOVE script) updates row in place via Message-ID dedup

### Phase 5 - IMAP write verbs + flag mutations - status: open

1. [ ] Extend `internal/mailbox/imap.go` with: markSeen, markFlagged, markAnswered, moveMessage (RFC 6851 + COPY+\Deleted+EXPUNGE fallback), copyMessage, expungeUID, deleteMessage, appendMessage
2. [ ] Capability probe at dial — cache MOVE / UIDPLUS / SPECIAL-USE flags
3. [ ] Integration test: MOVE between folders works AND graceful-degrades on capability-stripped server
4. [ ] `internal/mailbox/service.go` — SetSeen/SetFlagged/MoveTo/Delete commands; sync local mirror after server ack
5. [ ] CI gate script run: `grep -RnE '\bimapclient\.' internal/ | grep -v internal/mailbox/imap.go` returns zero

### Phase 6 - Inbox + thread UI + foreground actions - status: open

1. [ ] Port `html/inbox.html` → `inbox.templ` — list view, pills, search input, signal contract from SPEC §Datastar
2. [ ] `GET /inbox` + `GET /inbox/stream` (SSE) — Bus subscription re-renders rows on new ingest
3. [ ] Port `html/thread.html` → `thread.templ` — open thread calls `SELECT + BODY[textPath]`, marks \Seen via STORE, patches Ingest.Seen=true
4. [ ] Foreground action endpoints (POST + SSE): `/thread/{id}/seen`, `/flag`, `/move`, `/delete`
5. [ ] Render BodyHTML via `bluemonday.UGCPolicy()` — strip external `<img src>`; rewrite `cid:` → `upload://`
6. [ ] Render BodyText via markdown (escaped at ingest)
   - => Phase 6 result: end-to-end demo — sign in, see inbox of greenmail messages, open one (marks read), star/unstar, archive (MOVE)

### Phase 7 - SMTP send + reply + APPEND to Sent - status: open

1. [ ] `internal/send/build.go` — BuildMessage(SendCommand) → (msgID, raw []byte); MIME multipart with attachments; In-Reply-To + References chain for replies
2. [ ] `internal/send/smtp.go` — DialTLS / DialStartTLS; PlainAuth; SendMail
3. [ ] After send: `mailbox.appendMessage(IMAP_SENT_FOLDER, raw)` (best-effort, log on fail)
4. [ ] On reply send: `markAnswered(originalUID)` (best-effort)
5. [ ] Insert local `Ingest{Direction:"out"}` immediately so UI reflects; poll-back dedups by MessageID
6. [ ] Port `html/compose.html` → `compose.templ`; reply form embedded in `thread.templ`
   - => Phase 7 result: compose + send via greenmail SMTP; verify message lands in Sent folder

### Phase 8 - Projects + lazy attachment materialise - status: open

1. [ ] `internal/projects/{types,repo}.go` — Project + ProjectItem CRUD (slug-uniq, ItemKind discriminator)
2. [ ] `internal/uploads/cas.go` — content-addressed FS store (sha256 → `<UPLOADS_DIR>/<hex[:2]>/<hex>`), `os.Rename` from `.tmp`, `http.ServeContent` for download
3. [ ] `internal/mailbox/materialise.go` — `Materialise(messageID, projectID)`: short-lived IMAP session, EXAMINE current folder, BODY.PEEK[MIMEPartID] per attachment, decodeAttachmentBytes, CAS write, update Attachment row
4. [ ] Tag dropdown in `thread.templ` — `POST /thread/{id}/tag` SSE handler
5. [ ] New `project.templ` styled with `pages.css` tokens — interleaves tagged emails + notes (Phase 9 hookup) by CreatedAt
6. [ ] `GET /attach/{id}` — stream download with proper Content-Type + Content-Disposition
   - => Phase 8 result: tag a thread → attachments downloaded, viewable on `/p/<slug>`

### Phase 9 - Notes (Apple-Notes-style, IMAP-backed) - status: open

1. [ ] `internal/notes/build.go` — `BuildNoteMessage` produces RFC 2822 with X-Webmail-Note headers + markdown body (QP-encoded)
2. [ ] `internal/notes/sync.go` — poll sees IMAP_NOTES_FOLDER, routes by `X-Webmail-Note: v1` header; X-Webmail-Note-Original-MID → stamp SupersededBy on superseded row
3. [ ] Edit-as-APPEND+EXPUNGE: `EditNote` → new APPEND with bumped Version + Original-MID, then `STORE +FLAGS (\Deleted)` + `UID EXPUNGE` on old UID
4. [ ] $Pinned + $note_<slug> IMAP keyword sync via STORE
5. [ ] `goldmark` render at ingest → cache Note.BodyHTML
6. [ ] `notes.templ` (list) + `note.templ` (editor with live preview); routes from SPEC §UI surfaces
7. [ ] Notes link into projects via ProjectItem with ItemKind="note"
   - => Phase 9 result: create a note in web UI → visible in Apple Mail / Thunderbird's `Notes` folder; edit it → superseded version vanishes from list

### Phase 10 - Settings + admin + polish - status: open

1. [ ] Port `html/settings.html` → `settings.templ`: read-only IMAP/SMTP config, tracking-pixel toggle (`/settings/tracking-pixel`)
2. [ ] Admin user management UI (admin role only): create/disable/role-change users
3. [ ] Folder role mapping override (Sent/Trash/Archive/Notes per-account picker)
4. [ ] Structured logging (slog JSON in prod, text in dev) + access-log middleware
5. [ ] README with run/deploy instructions; sample compose.yml with greenmail for local dev
6. [ ] Smoke test script `scripts/smoke.sh` — boot, login, ingest 1 message, open, flag, archive, tag-to-project, create note

## Verification

- `go test ./...` green
- `go vet ./...` + `golangci-lint run` clean
- CI grep gate: zero hits for `imapclient.` outside `internal/mailbox/imap.go`
- Integration acceptance vs greenmail container:
  - poll ingests new mail without marking \Seen
  - opening a thread DOES mark \Seen on server
  - star + archive + delete each round-trip to server
  - send + APPEND to Sent verified by re-fetch
  - reply marks original \Answered
  - tag-to-project materialises attachments to CAS, file content sha256 matches
  - create note → appears in IMAP Notes folder with X-Webmail-Note header
  - edit note → old UID EXPUNGE'd, new UID present, list shows only newest
- Manual: golden-path demo in a browser end-to-end: login → inbox → open → reply → archive → tag → notes

## Adjustments

- 2606152005 — Static CSS will be served via `embed.FS` in `internal/httpx`, not from disk. Dockerfile dropped `COPY web/static` and `COPY migrations` accordingly — single binary deploy. Phase 1 action 2 updated implicitly to use `//go:embed web/static/styles/*` instead of `http.FileServer(http.Dir(...))`.

## Progress Log

- 2606151930 — Plan created. Spec v0.3 finalised (webmail mutation surface + Notes-as-IMAP-folder). Git repo initialised.
- 2606152005 — Phase 0 complete. Module = `github.com/atvirokodosprendimai/webmail`. Deps + tools installed. Layout, .env.example, Makefile, Dockerfile, CI grep gate landed and tested. Branch: `task/phase-0-bootstrap`.
