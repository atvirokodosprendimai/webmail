package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

const sessionUserKey = "user_id"

// Sessions wraps an scs.SessionManager with the session-key knobs we use.
type Sessions struct{ *scs.SessionManager }

func NewSessions(maxAge time.Duration) *Sessions {
	m := scs.New()
	m.Lifetime = maxAge
	m.IdleTimeout = maxAge
	m.Cookie.Name = "orbital_sid"
	m.Cookie.HttpOnly = true
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Secure = false // flipped to true via reverse-proxy / env in prod
	return &Sessions{SessionManager: m}
}

// PutUserID stores the authenticated user ID in the session.
func (s *Sessions) PutUserID(ctx context.Context, id string) {
	s.Put(ctx, sessionUserKey, id)
}

// CurrentUserID returns the user ID in the session, or "" if not signed in.
func (s *Sessions) CurrentUserID(ctx context.Context) string {
	return s.GetString(ctx, sessionUserKey)
}

// Forget removes the current session — used by /logout.
func (s *Sessions) Forget(ctx context.Context) error {
	return s.Destroy(ctx)
}
