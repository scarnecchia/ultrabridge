package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// CredentialFunc returns the current username and bcrypt password hash.
// Called on every request so credentials picked up from DB changes
// (e.g., seed-user, setup page, Settings UI) take effect immediately.
type CredentialFunc func() (username, passwordHash string)

// TokenValidator validates a bearer token. Returns nil if valid.
type TokenValidator func(token string) error

type Middleware struct {
	credsFn        CredentialFunc
	tokenValidator TokenValidator
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

// SetTokenValidator adds bearer token validation to the auth chain.
// When set, Authorization: Bearer <token> is checked before Basic Auth.
func (m *Middleware) SetTokenValidator(v TokenValidator) {
	m.tokenValidator = v
}

// Wrap returns an http.Handler that requires auth before delegating to next.
// Checks bearer token first (if validator set), then Basic Auth.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try bearer token first
		if m.tokenValidator != nil {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				token := auth[len("Bearer "):]
				if m.tokenValidator(token) == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		// Fall back to Basic Auth
		user, pass, ok := r.BasicAuth()
		if ok && m.validBasic(user, pass) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="UltraBridge"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

func (m *Middleware) validBasic(user, password string) bool {
	username, hash := m.credsFn()
	if username == "" || hash == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
	passOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	return userOK && passOK
}
