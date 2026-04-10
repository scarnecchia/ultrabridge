package auth

import (
	"crypto/subtle"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// CredentialFunc returns the current username and bcrypt password hash.
// Called on every request so credentials picked up from DB changes
// (e.g., seed-user, setup page, Settings UI) take effect immediately.
type CredentialFunc func() (username, passwordHash string)

type Middleware struct {
	credsFn CredentialFunc
}

// New creates auth middleware with a static username and password hash.
func New(username, passwordHash string) *Middleware {
	return &Middleware{
		credsFn: func() (string, string) { return username, passwordHash },
	}
}

// NewDynamic creates auth middleware that reads credentials on each request.
func NewDynamic(fn CredentialFunc) *Middleware {
	return &Middleware{credsFn: fn}
}

// Wrap returns an http.Handler that requires Basic Auth before delegating to next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !m.valid(user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="UltraBridge"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) valid(user, password string) bool {
	username, hash := m.credsFn()
	if username == "" || hash == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
	passOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	return userOK && passOK
}
