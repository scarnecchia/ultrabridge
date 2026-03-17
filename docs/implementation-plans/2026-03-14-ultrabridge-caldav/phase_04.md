# UltraBridge CalDAV — Phase 4: Auth Middleware

**Goal:** Protect CalDAV and Web UI endpoints with Basic Auth against bcrypt-hashed password.

**Architecture:** HTTP middleware that wraps handlers. Validates Basic Auth credentials against configured username and bcrypt hash. Applied to CalDAV handler and Web UI handler. Health endpoint is NOT auth-protected.

**Tech Stack:** Go 1.22, `net/http`, `golang.org/x/crypto/bcrypt`

**Scope:** 8 phases from original design (phase 4 of 8)

**Codebase verified:** 2026-03-17

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC5: Simple auth
- **ultrabridge-caldav.AC5.1 Success:** Valid Basic Auth credentials grant access to CalDAV and web UI
- **ultrabridge-caldav.AC5.2 Failure:** Missing or invalid credentials return 401 with generic message (no credential hints)

---

<!-- START_TASK_1 -->
### Task 1: Auth middleware

**Files:**
- Create: `internal/auth/auth.go`

**Implementation:**

HTTP middleware that checks Basic Auth credentials. Username compared directly, password compared against bcrypt hash via `bcrypt.CompareHashAndPassword`.

```go
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
```

**Step 2: Add `golang.org/x/crypto` to go.mod**

```bash
cd /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
go get golang.org/x/crypto
```

**Step 3: Wire auth into main.go**

In `cmd/ultrabridge/main.go`, wrap CalDAV and (future) web UI handlers with auth middleware. Health endpoint remains unprotected.

```go
	authMW := auth.New(cfg.Username, cfg.PasswordHash)

	mux.Handle("/caldav/", authMW.Wrap(caldavHandler))
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		// well-known also needs auth — CalDAV clients send creds on discovery
		authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
		})).ServeHTTP(w, r)
	})
```

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles.

**Commit:** `feat: add Basic Auth middleware with bcrypt`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Auth middleware tests

**Verifies:** ultrabridge-caldav.AC5.1, ultrabridge-caldav.AC5.2

**Files:**
- Create: `internal/auth/auth_test.go`

**Testing:**

Tests must verify each AC:
- **ultrabridge-caldav.AC5.1:** Request with valid username + password (matching bcrypt hash) passes through to wrapped handler; handler receives the request and can respond.
- **ultrabridge-caldav.AC5.2:** Request with no auth header returns 401 with `WWW-Authenticate` header. Request with wrong username returns 401. Request with wrong password returns 401. Response body contains no credential hints (no username, no hash info).

Use `httptest.NewRecorder` and `httptest.NewRequest`. Generate a bcrypt hash in the test with `bcrypt.GenerateFromPassword`.

Additional tests:
- Timing-safe username comparison (implicit in `subtle.ConstantTimeCompare`)
- Wrapped handler is not called on auth failure

Follow Go standard testing patterns.

**Verification:**

```bash
go test ./internal/auth/ -v
```

Expected: All tests pass.

**Commit:** `test: add auth middleware tests`

<!-- END_TASK_2 -->
