package auth

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// CredentialFunc returns the current username and bcrypt password hash.
// Called on every request so credentials picked up from DB changes
// (e.g., seed-user, setup page, Settings UI) take effect immediately.
type CredentialFunc func() (username, passwordHash string)

// TokenValidator validates a bearer token. Returns the token's identifying
// label on success (empty string is allowed for anonymous/internal tokens);
// returns an error otherwise. The label is exposed to downstream handlers
// via auth.IdentityFromContext so mutation audit logs can attribute who
// did what.
type TokenValidator func(token string) (label string, err error)

// Identity describes the auth path that succeeded for a request. Surfaced
// to handlers via IdentityFromContext. Method is "basic", "bearer", or "" if
// no auth middleware was hit.
type Identity struct {
	Method string
	Label  string // bearer-token label, or the basic-auth username
}

type identityCtxKey struct{}

// IdentityFromContext extracts the Identity installed by the auth
// middleware. Returns the zero Identity when absent (e.g. pre-middleware
// code paths or tests that skip auth).
func IdentityFromContext(ctx context.Context) Identity {
	if v, ok := ctx.Value(identityCtxKey{}).(Identity); ok {
		return v
	}
	return Identity{}
}

// WithIdentity attaches an Identity to a context. Exported so tests can
// drive handlers directly without exercising the middleware.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

type Middleware struct {
	credsFn        CredentialFunc
	tokenValidator TokenValidator
	logger         *slog.Logger
	verbose        bool
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

// SetLogger sets the logger and verbose flag for the middleware.
func (m *Middleware) SetLogger(logger *slog.Logger, verbose bool) {
	m.logger = logger
	m.verbose = verbose
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
			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				token := authHeader[len("Bearer "):]
				if label, err := m.tokenValidator(token); err == nil {
					id := Identity{Method: "bearer", Label: label}
					next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
					return
				}
				if m.verbose && m.logger != nil {
					m.logger.Warn("auth failure: invalid bearer token", "remote_ip", r.RemoteAddr, "path", r.URL.Path)
				}
			}
		}

		// Fall back to Basic Auth
		user, pass, ok := r.BasicAuth()
		if ok {
			if m.validBasic(user, pass) {
				id := Identity{Method: "basic", Label: user}
				next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
				return
			}
			if m.verbose && m.logger != nil {
				m.logger.Warn("auth failure: invalid basic credentials", "user", user, "remote_ip", r.RemoteAddr, "path", r.URL.Path)
			}
		} else if m.verbose && m.logger != nil && r.Header.Get("Authorization") != "" {
			// They sent an Authorization header but it wasn't valid Bearer or Basic
			m.logger.Warn("auth failure: malformed authorization header", "remote_ip", r.RemoteAddr, "path", r.URL.Path)
		}

		if m.tokenValidator != nil {
			w.Header().Add("WWW-Authenticate", `Bearer realm="UltraBridge"`)
			w.Header().Add("WWW-Authenticate", `Basic realm="UltraBridge"`)
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="UltraBridge"`)
		}
		if m.verbose && m.logger != nil {
			m.logger.Warn("auth failure: unauthorized request", "remote_ip", r.RemoteAddr, "path", r.URL.Path, "has_auth", r.Header.Get("Authorization") != "")
		}
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
