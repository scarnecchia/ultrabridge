package auth

import (
	"crypto/subtle"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

type Middleware struct {
	username     string
	passwordHash []byte
}

func New(username, passwordHash string) *Middleware {
	return &Middleware{
		username:     username,
		passwordHash: []byte(passwordHash),
	}
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
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(m.username)) == 1
	passOK := bcrypt.CompareHashAndPassword(m.passwordHash, []byte(password)) == nil
	return userOK && passOK
}
