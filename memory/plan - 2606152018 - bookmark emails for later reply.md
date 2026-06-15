---
tldr: Add a "bookmark this thread for later" feature. Two flavours — personal ("I'll reply later") and shared ("team should look at this"). Same Message-ID-keyed shape as projects; distinct surface in UI (button in thread view + /bookmarks page + nav entry).
status: active
---

# Plan: bookmark emails for later reply

## Context

- Lives in the same repo as the rest of webmail: `/Users/mind/code/ai/webmail`.
- Spec: SPEC.md (no spec change needed — bookmarks are a small additive feature). Linking will go in v0.4 of the spec when this lands.
- Branch: continue on `task/phase-0-bootstrap` (everything still lives on that branch until merge).
- Reuses three existing patterns:
  - **Projects pattern** (`internal/projects`) — Message-ID as join key, gorm + goose, idempotent tag, thread-action endpoint, list page.
  - **Per-user filtering** — `auth.CurrentUser(r)` gives us `user.ID`, used to scope rows.
  - **Render layer** — Shell + reusable list template (`Inbox`), so the /bookmarks page can match the look without new CSS.
- Differences from projects:
  - **Scope** — projects are workspace-shared; bookmarks can be `personal` (one user) OR `shared` (team). Single table, `user_id` column nullable: NULL = shared, non-NULL = that user only.
  - **No attachment materialise** — bookmarks are pure pointers; the attachment lives wherever IMAP keeps it. Materialise stays on the projects path.
  - **No description** — flat list. v1.1 can add reminder time / note if it turns out useful.
- User-confirmed scope (via AskUserQuestion):
  - Per-user + shared variants both available.
  - NO reminder/due-date field — flat list only.
  - Add-bookmark UI: button in thread view (not inbox row).

## Phases

### Phase 1 - Schema + repo - status: open

1. [ ] goose migration `00010_bookmarks.sql` — `bookmarks(id PK, message_id NOT NULL, item_kind, user_id NULL, note, created_by NOT NULL, created_at NOT NULL)` + unique index on `(coalesce(user_id, ''), message_id)` so the same thread can be both personally bookmarked AND team-shared, but not double-bookmarked at the same scope.
   - `user_id NULL` = team-shared bookmark
   - `user_id` set = personal bookmark for that user
2. [ ] `internal/bookmarks/types.go` — `Bookmark` gorm struct + `KindEmail` / `KindNote` constants (same shape as projects so future notes-bookmarks slot in).
3. [ ] `internal/bookmarks/repo.go`:
   - `Add(ctx, messageID, kind, userID, note, createdBy)` — idempotent UPSERT.
   - `Remove(ctx, id)` — by row id (so removing a personal bookmark doesn't touch a sibling shared one).
   - `ListPersonal(ctx, userID)` — `WHERE user_id = ?`, newest first.
   - `ListShared(ctx)` — `WHERE user_id IS NULL`, newest first.
   - `IsBookmarked(ctx, messageID, userID) (personal bool, shared bool)` — used by the thread view to render correct button labels.

### Phase 2 - Thread view UI + tag flow - status: open

1. [ ] `internal/render/thread.templ` — under the existing "+ tag into project" `<details>`, add a "+ bookmark for later" section with two buttons: "Bookmark (personal)" + "Bookmark (team)", plus an optional `<input name="note">`.
   - Show the current bookmark state inline ("⭐ personal bookmark" / "👥 team bookmark") with a "remove" form.
2. [ ] `internal/httpx/handlers.go`:
   - `POST /thread/{id}/bookmark` — reads `scope` form field (`personal|shared`) + `note`, calls Repo.Add. Redirects back.
   - `POST /bookmark/{bookmark_id}/remove` — calls Repo.Remove. Redirects back.
   - Wire routes in `internal/httpx/server.go`.
3. [ ] Update thread handler to compute `personalSet`, `sharedSet`, `bookmarkRows` for the current thread and pass into the templ.
   - => Phase 2 result: open any thread → click "Bookmark (personal)" → state chip appears + persists across reload.

### Phase 3 - /bookmarks page + nav entry - status: open

1. [ ] `internal/render/bookmarks.templ` — list page styled like inbox (uses Shell + `.list` block). Two sections: "Mine" (personal) and "Team" (shared). Each row = subject + from + when, links to `/thread/{first ingest id with that message_id}`.
2. [ ] `internal/httpx/handlers.go` — `bookmarksIndex` handler: query personal + shared bookmarks for current user, join to `mailbox_ingest` on `message_id` (folder-agnostic per design), project to list rows with subject decoded via `mailbox.DecodeHeader`.
3. [ ] `internal/render/shell.templ` — add "Bookmarks" entry to the side nav under "workspace" (next to Projects + Notes). Also add a rail icon between Notes and Projects.
4. [ ] Route `/bookmarks` in `internal/httpx/server.go`, gated behind `RequireUser`.
   - => Phase 3 result: nav link visible → click → see your personal bookmarks + team bookmarks → click a row → opens the thread.

### Phase 4 - Inbox row badge - status: open

1. [ ] Extend the inbox/folder list query to include `is_bookmarked_personal` + `is_bookmarked_shared` via EXISTS subqueries scoped by `thread_id` (so a bookmark on any message in the thread surfaces on the inbox row).
2. [ ] `InboxRow` struct gains two bool fields; `inbox.templ` shows a small `🔖` chip when set.
   - => Phase 4 result: bookmarked threads visually stand out in the inbox without opening them.

### Phase 5 - Polish + plan close-out - status: open

1. [ ] Update SPEC.md to v0.4 with a one-paragraph "Bookmarks" section under §UI surfaces + a row in the route table.
2. [ ] Update CI grep gate check — no new IMAP calls in this feature (bookmarks are pure DB), so the gate just needs to keep passing.
3. [ ] Mark this plan `status: completed` and add the closing Progress Log entry.

## Verification

- `go build ./...` clean.
- `go vet ./...` clean.
- `./scripts/check-imap-wrapper.sh` still OK (this feature touches no `imapclient.*`).
- Manual end-to-end:
  1. Open a thread, click "Bookmark (personal)" → page re-renders with the personal-bookmark chip + "remove" button.
  2. Visit `/bookmarks` → row appears in "Mine" section. Click it → returns to the thread.
  3. Click "Bookmark (team)" on the same thread → row also appears in "Team" section. Confirm both can coexist (different scope = different uniq key).
  4. Log out, log in as a second user (`webmail user add other@x` first). `/bookmarks` "Mine" is empty. "Team" shows the shared bookmark. Cannot remove the other user's personal bookmark (not visible). Can remove the shared one (any team member may).
  5. Inbox row for the bookmarked thread shows the 🔖 chip.

## Adjustments

<!-- Plans evolve. Document changes with timestamps. -->

## Progress Log

- 2606152018 — Plan created. User-confirmed scope: per-user + shared variants both available, no reminder/due-date, add via thread-view button.
