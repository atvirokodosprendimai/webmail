package auth

import (
	"errors"
	"net/http"

	"github.com/atvirokodosprendimai/webmail/internal/render"
)

type Handler struct {
	Repo *Repo
	Sess *Sessions
}

// LoginPage renders the login form.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if h.Sess.CurrentUserID(r.Context()) != "" {
		http.Redirect(w, r, "/inbox", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.Login("").Render(r.Context(), w)
}

// LoginSubmit processes the form POST.
func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")

	u, err := h.Repo.Authenticate(r.Context(), email, password)
	if err != nil {
		msg := "Unknown credentials. Try again."
		if !errors.Is(err, ErrInvalidCredentials) {
			msg = "Login error. Try again."
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = render.Login(msg).Render(r.Context(), w)
		return
	}

	// Renew session id on login to defeat session fixation.
	if err := h.Sess.RenewToken(r.Context()); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	h.Sess.PutUserID(r.Context(), u.ID)
	http.Redirect(w, r, "/inbox", http.StatusSeeOther)
}

// Logout drops the session and redirects to /login.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	_ = h.Sess.Forget(r.Context())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
