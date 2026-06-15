package auth

import (
	"context"
	"net/http"
)

type ctxKey struct{}

var userCtxKey = ctxKey{}

// RequireUser is a chi middleware that redirects unauthenticated
// requests to /login. Authenticated requests get the User attached to
// the request context via WithUser.
func RequireUser(sess *Sessions, repo *Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := sess.CurrentUserID(r.Context())
			if id == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			u, err := repo.Find(r.Context(), id)
			if err != nil {
				_ = sess.Forget(r.Context())
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CurrentUser returns the authenticated User from the request context.
// Panics if RequireUser didn't run — that's intentional, it's a wiring
// bug if a handler reaches CurrentUser without the middleware.
func CurrentUser(r *http.Request) User {
	u, ok := r.Context().Value(userCtxKey).(User)
	if !ok {
		panic("auth: CurrentUser called without RequireUser middleware")
	}
	return u
}
