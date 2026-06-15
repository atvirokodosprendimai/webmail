// Package httpx wires the chi router and embedded static files into a
// single *http.Server. Routes are defined here; handler logic lives in
// handlers.go.
package httpx

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed all:static
var staticFS embed.FS

func New(a *App) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(a.Sessions.LoadAndSave)

	sub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Get("/login", a.AuthHandler.LoginPage)
	r.Post("/login", a.AuthHandler.LoginSubmit)
	r.Post("/logout", a.AuthHandler.Logout)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser(a.Sessions, a.AuthRepo))
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/inbox", http.StatusSeeOther)
		})

		r.Get("/inbox", a.inbox)
		r.Get("/inbox/stream", a.inboxStream)
		r.Get("/folder/{role}", a.folderView)
		r.Get("/thread/{id}", a.thread)
		r.Post("/thread/{id}/seen", a.threadSeen)
		r.Post("/thread/{id}/flag", a.threadFlag)
		r.Post("/thread/{id}/move", a.threadMove)
		r.Post("/thread/{id}/reply", a.threadReply)
		r.Post("/thread/tag", a.threadTag)

		r.Get("/compose", a.composePage)
		r.Post("/compose/send", a.composeSend)

		r.Get("/projects", a.projectsIndex)
		r.Post("/projects", a.projectsCreate)
		r.Get("/p/{slug}", a.projectPage)

		r.Get("/bookmarks", a.bookmarksIndex)
		r.Post("/thread/{id}/bookmark", a.bookmarkAdd)
		r.Post("/bookmark/{id}/remove", a.bookmarkRemove)

		r.Get("/issues", a.issuesIndex)
		r.Post("/issues", a.issuesCreate)
		r.Get("/issues/{id}", a.issueShow)
		r.Post("/issues/{id}/status", a.issueStatus)
		r.Post("/issues/{id}/title", a.issueTitle)
		r.Post("/issues/{id}/notes", a.issueNotes)
		r.Post("/issues/{id}/assignees", a.issueAssign)
		r.Post("/issues/{id}/assignees/{userId}/remove", a.issueUnassign)

		r.Get("/attach/{id}", a.attachDownload)

		r.Get("/notes", a.notesIndex)
		r.Get("/notes/new", a.noteNew)
		r.Post("/notes", a.noteCreate)
		r.Get("/notes/{id}", a.noteShow)
		r.Get("/notes/{id}/edit", a.noteEdit)
		r.Post("/notes/{id}", a.noteSave)
		r.Post("/notes/{id}/pin", a.notePin)
		r.Post("/notes/{id}/delete", a.noteDelete)

		r.Get("/settings", a.settings)
	})

	return r
}
