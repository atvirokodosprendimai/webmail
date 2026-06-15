---
tldr: Turn any email thread into a tracked issue with title + multiple assignees + open/closed state + side notes. Pointer-only (Message-ID join key, same shape as projects/bookmarks).
---

# Issues

A lightweight issue tracker layered on top of the existing webmail. Inspired by GitHub Issues but reusing two patterns already proven in the codebase:

- **Pointer storage** — an issue is a thin row joined to an email thread by RFC 2822 Message-ID. The thread body, attachments, and reply chain are not duplicated; the existing thread view IS the conversation.
- **Sidecar metadata** — title, status, assignees, internal notes live on the issue row, not on the email.

## Target

Email-driven teams use a thread to discuss but lose track once it scrolls off the inbox. "Who's handling this?" "Did we close that?" "Where's the question about the invoice?" — answers live inside one of N messages. The tracker promotes a thread to a first-class object you can title, assign, mark done, and find later.

Specifically:

- A bug report arriving by email becomes an Issue with the same thread, the same attachments, but a clear title, an owner, and an open/closed state.
- A customer question that needs research becomes an Issue assignable to two people; the email reply IS the response.
- Internal-only context (rejection reason, root cause, "actually it's a duplicate") goes into the issue notes, never sent over email.

## Behaviour

### Lifecycle

- States: `open` (default) and `closed`. Nothing in between.
- Toggling state takes one click. No transition workflow, no required fields.
- Closed issues stay searchable + visible behind a "Show closed" toggle on `/issues`.
- Reopening = setting status back to `open`. No separate "reopened" state.

### Creation

- From any thread, a "Convert to issue" button creates an Issue rooted at that thread.
- Default title = decoded subject of the thread's representative message.
- Default status = `open`.
- Initial assignees = empty (the user explicitly picks one or more from the local users list).
- After creation, the thread page shows an "Issue #N" chip linking to `/issues/{id}`.

### Assignees

- Multiple assignees per issue, picked from `users` (webmail logins).
- Modeled as `issue_assignees(issue_id, user_id)` join table.
- Adding/removing an assignee is a single form post; no bulk-edit UI at v1.
- Self-assign = drop yourself onto an issue. Indistinguishable from being assigned by another admin.
- No notification fan-out at v1 — the surface is the issue list filtered "assigned to me".

### Conversation source

- The email thread (existing `/thread/{id}` view) is the conversation surface for external messages.
- A reply made from the thread view writes a real outbound email + STORE +\Answered (existing flow).
- Internal notes (visible only to webmail logins, never sent over email) attach to the issue row in a `notes` column — markdown, rendered via goldmark.
- Issue notes are a single editable text field at v1, not a comment stream.

### Independence from projects

- An issue does **not** belong to a project (user decision: "Independent").
- Projects and issues are sibling features. A thread can be tagged into a project AND promoted to an issue simultaneously; the two systems don't share state.
- Future graft: a `project_id` column could land later without migration of existing data (NULL = independent).

### List + filtering

- `/issues` shows all issues, default sort by `updated_at DESC`.
- Pills: "Open" (default) / "Closed" / "All".
- Filter "Assigned to me" — pulls `issues` where the viewer's `user_id` is in `issue_assignees`.
- Search box behaves like the inbox search: case-insensitive LIKE over title + notes.

### Auto-decoration on the thread

- When a thread is promoted, the `/thread/{messageID}` view gets a small banner: `Issue #N — open — assigned to Alice, Bob`.
- Banner links to `/issues/{id}`.
- Closing an issue from the thread banner is a one-click action (mirrors the issue page button).

## Design

### Schema

Three new tables (goose migrations `00011_..` through `00013_..`):

```sql
CREATE TABLE issues (
    id            TEXT PRIMARY KEY,        -- uuidv7
    number        INTEGER NOT NULL UNIQUE, -- monotonic per-install (#1, #2, …)
    message_id    TEXT NOT NULL,           -- RFC 2822 root MID, join to mailbox_ingest
    title         TEXT NOT NULL,
    notes_md      TEXT NOT NULL DEFAULT '',
    notes_html    TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'open',  -- open | closed
    created_by    TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    closed_at     DATETIME
);
CREATE INDEX issues_status_idx     ON issues(status);
CREATE INDEX issues_updated_idx    ON issues(updated_at DESC);
CREATE UNIQUE INDEX issues_msg_idx ON issues(message_id);
-- One issue per thread root. Promoting an already-promoted thread is a
-- no-op redirect; the user can't accidentally create two issues for the
-- same conversation.

CREATE TABLE issue_assignees (
    issue_id TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX issue_assignees_user_idx ON issue_assignees(user_id);

CREATE TABLE issue_counter (
    -- Singleton row; SELECT + UPDATE within a tx generates the next #N.
    -- Avoids the GitHub-style "issue number = max(number)+1" race that
    -- can collide under concurrent creation.
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    next_id INTEGER NOT NULL
);
INSERT INTO issue_counter (id, next_id) VALUES (1, 1);
```

Why a counter table instead of `max() + 1`: two concurrent "Convert to issue" clicks would otherwise pick the same number. Sqlite serialised reads + an UPDATE-RETURNING (or SELECT-FOR-UPDATE in WAL mode) on a singleton row is the cheap correct pattern.

### Routes

```
GET  /issues                               -- list
POST /issues                               -- create (from thread)
GET  /issues/{id}                          -- detail
POST /issues/{id}/status                   -- toggle open/closed
POST /issues/{id}/assignees                -- add user_id
POST /issues/{id}/assignees/{userId}/remove
POST /issues/{id}/title                    -- rename
POST /issues/{id}/notes                    -- save markdown notes
```

`POST /issues` accepts `message_id` (the thread root) + optional `title`. If an issue with that MID already exists, redirect to it instead of creating a duplicate.

### UI

- New rail icon (Lucide `circle-dot` for open, filled when current page is `/issues`).
- New nav entry under "Workspace" — `Issues` with the open-count.
- `/issues` list reuses the `.list` component pattern from inbox.
- `/issues/{id}` reuses `.reader` layout. Two sections: **Thread** (links to `/thread/{messageID}` + inline preview) and **Internal notes** (markdown editor + rendered view, same shape as `/notes`).
- Assignee strip: avatars of every assignee + an "Add…" picker dropdown sourced from `users`.
- Status chip in the header: green for open, neutral for closed.

### Where the code lives

```
internal/issues/
  types.go     -- Issue + Assignee gorm models
  repo.go      -- CRUD + Counter logic in a transaction
  service.go   -- (optional) transaction orchestration
internal/render/
  issues.templ -- list + detail
internal/httpx/handlers.go
  +issuesIndex, issueShow, issueCreate, issueStatus,
   issueAssign, issueUnassign, issueRename, issueNotes
```

Reusing `render.NavCounts` for the rail badge would mean threading an additional `IssuesOpen` field through — cleaner to add it once and reuse.

## Verification

- **Create from thread.** Open any thread → "Convert to issue" → `/issues/{id}` shows with the thread linked, default title = subject, status open, 0 assignees, # number incremented.
- **Idempotent on the same thread.** Click "Convert to issue" twice → second click redirects to the existing issue, no duplicate row.
- **Assignee round-trip.** Add Alice + Bob → reload → both avatars visible. Remove Bob → only Alice.
- **Close + reopen.** Click close → status chip flips, closed_at set, banner on `/thread/{id}` updates. Click reopen → state restored.
- **Filter assigned-to-me.** Login as Alice, set filter → only issues with Alice in `issue_assignees` show.
- **Internal note isolation.** Set notes "internal context" on an issue → reply to the same thread from `/thread/{id}` → outbound SMTP does NOT contain the internal note text. The note only renders inside `/issues/{id}`.
- **Search.** Issue title "broken SSL" + note containing "let's encrypt" → both `q=ssl` and `q=encrypt` surface the issue.
- **Counter under concurrency.** Two simultaneous POST /issues with different message_ids → both succeed with sequential numbers (#N, #N+1). No collision.
- **Closed visibility.** Default list hides closed. "Show closed" toggle reveals them.

## Friction

- **No notifications.** Assigning someone doesn't email them or surface a notification badge anywhere. v1 surface = the "Assigned to me" filter. Most teams will want notifications by v1.1.
- **No labels / tags.** GitHub's `bug`/`enhancement`/etc. are out of scope. Users can encode labels in title prefixes (`[bug] xxx`) until v1.1 ships a labels table.
- **No history / audit.** Status changes + assignee deltas aren't logged. A future `issue_events(issue_id, kind, actor_id, at, payload)` table can fill this in.
- **Markdown notes vs full comment stream.** Single editable notes field is simpler but loses chronology. If a team needs "Alice said X, then Bob said Y", they should use the email thread for that and reserve the notes for internal context like rejection reason / root cause.
- **No protection on the counter.** A backup-restore that brings back an old `issue_counter.next_id` could re-mint numbers. Disaster recovery is a known sharp edge (sqlite + a single counter row).
- **One issue per thread.** A long thread that grows multiple problems can only have one issue at a time. Splitting requires a manual workflow (close + start a new thread).

## Interactions

- Depends on the **mailbox_ingest** table (Message-ID join key, `ThreadID` derivation).
- Depends on the **users** table (assignees pulled from there; no separate identity).
- Sibling to **projects** and **bookmarks** — same Message-ID-pointer shape, three independent decorations on top of email threads.
- Reuses **render.NavCounts** (new `IssuesOpen` field).
- Reuses **goldmark** (notes_md → notes_html via the same path as `notes`).

## Mapping

> [[internal/issues/types.go]]
> [[internal/issues/repo.go]]
> [[internal/render/issues.templ]]
> [[internal/httpx/handlers.go]]
> [[internal/db/migrations/00011_issues.sql]]
> [[internal/db/migrations/00012_issue_assignees.sql]]
> [[internal/db/migrations/00013_issue_counter.sql]]

## Future

{[!] Notifications — at least an in-app badge for "you have N assigned issues"; later, email or push.}
{[!] Labels — `issue_labels` + a `labels` table with colour. Filter by label on `/issues`.}
{[!] Audit log — `issue_events` capturing every status change + assignee add/remove + title rename.}
{[?] Project link — optional `project_id` on `issues` to bucket per project without forcing the structure.}
{[?] Auto-close on email reply — heuristic to mark an issue closed when the original reporter says "thanks, that worked".}
{[?] IMAP keyword sync — set `$Issue` keyword on the root message so the issue is visible from Roundcube as a flag.}
{[?] CLI: `webmail issues list --assigned you@example.com` for terminal flows.}

## Notes

This spec deliberately stays small. The hardest call was "comments separate or use the email thread"; we picked **both** — email thread for the customer-facing conversation, internal notes for the "between us" context. That maps cleanly onto how email-driven teams already think.

The pointer-only approach means an issue costs ~150 bytes of DB row + N rows in `issue_assignees`. Tens of thousands of issues fit comfortably in sqlite without needing FTS5 or pagination tricks.

User-confirmed scope via AskUserQuestion (2606152228):
- States: Open / Closed only.
- Project link: independent.
- Assignees: multiple.
- Comments: both email thread AND internal notes.
