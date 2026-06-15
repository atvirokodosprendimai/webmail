// Package httpx wires the chi router, static file embed, and the
// shared mailbox bus into a single *http.Server suitable for cmd/webmail.
package httpx

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/atvirokodosprendimai/webmail/internal/render"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed all:static
var staticFS embed.FS

// Deps is the set of pre-wired application dependencies the router needs.
type Deps struct {
	Auth     *auth.Handler
	Sessions *auth.Sessions
	AuthRepo *auth.Repo
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(d.Sessions.LoadAndSave)

	// Static — embedded; no disk lookup at runtime.
	sub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Get("/login", d.Auth.LoginPage)
	r.Post("/login", d.Auth.LoginSubmit)
	r.Post("/logout", d.Auth.Logout)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser(d.Sessions, d.AuthRepo))
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/inbox", http.StatusSeeOther)
		})
		r.Get("/inbox", func(w http.ResponseWriter, r *http.Request) {
			u := auth.CurrentUser(r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = render.InboxStub(u.DisplayName, u.Email, u.Role).Render(r.Context(), w)
		})
	})

	return r
}
