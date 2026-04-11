# Platform-Neutral Configuration Implementation Plan

**Goal:** When no auth credentials exist in DB or env vars, serve an unauthenticated setup page for initial configuration. Setup mode only exposes credential setup — no data endpoints accessible.

**Architecture:** New `appconfig.IsSetupRequired(ctx, db)` function checks whether auth credentials exist. The auth middleware in `internal/auth/` gains a setup-mode bypass that redirects all requests to the setup page except the setup endpoints themselves. After credentials are saved, setup mode ends and Basic Auth is enforced for all subsequent requests.

**Tech Stack:** Go stdlib, `golang.org/x/crypto/bcrypt`, existing `internal/auth`, `internal/appconfig`

**Scope:** 8 phases from original design (this is phase 7 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC3: Simplified installer and first-boot
- **platform-neutral-config.AC3.3 Success:** Fresh container with no auth in DB shows setup page without requiring auth
- **platform-neutral-config.AC3.4 Success:** Saving credentials on setup page ends setup mode and enforces Basic Auth
- **platform-neutral-config.AC3.5 Success:** Existing install with .ultrabridge.env works on upgrade — env vars provide all config
- **platform-neutral-config.AC3.6 Failure:** Setup mode only exposes credential setup — no data endpoints accessible without auth

---

## Codebase Verification Findings

- ✓ `internal/auth/auth.go:10-39` — Middleware struct with `username`, `passwordHash` fields. `New(username, passwordHash)` constructor. `Wrap(next)` handler returns 401 with WWW-Authenticate header.
- ✓ Auth middleware created at `cmd/ultrabridge/main.go:321` — `auth.New(cfg.Username, cfg.PasswordHash)`
- ✓ Password hash loaded from env var or secrets file at `internal/config/config.go:138-144`
- ✓ `appconfig.Load()` falls back to env vars when DB has no values (design says AC1.8)
- ✓ bcrypt already a dependency (`golang.org/x/crypto/bcrypt`) used by auth middleware

**Testing approach:** Real in-memory SQLite, stdlib testing. Test setup mode detection, middleware bypass, credential save, and transition to normal mode. See `/home/sysop/src/ultrabridge/internal/auth/CLAUDE.md` if it exists for auth testing patterns.

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
<!-- START_TASK_1 -->
### Task 1: Add `IsSetupRequired()` to appconfig package

**Verifies:** platform-neutral-config.AC3.3, platform-neutral-config.AC3.5

**Files:**
- Modify: `internal/appconfig/config.go` (add IsSetupRequired function)
- Test: `internal/appconfig/appconfig_test.go` (add setup detection tests)

**Implementation:**

```go
// IsSetupRequired returns true when no auth credentials exist in either
// the settings DB or environment variables. This indicates first-boot setup
// is needed before the application can enforce authentication.
func IsSetupRequired(ctx context.Context, db *sql.DB) bool {
    // Check DB first
    username, _ := notedb.GetSetting(ctx, db, KeyUsername)
    hash, _ := notedb.GetSetting(ctx, db, KeyPasswordHash)
    if username != "" && hash != "" {
        return false
    }
    // Check env vars (backward compatibility for existing installs)
    if os.Getenv("UB_USERNAME") != "" && os.Getenv("UB_PASSWORD_HASH") != "" {
        return false
    }
    // Also check password hash file
    if os.Getenv("UB_USERNAME") != "" {
        hashPath := os.Getenv("UB_PASSWORD_HASH_PATH")
        if hashPath == "" {
            hashPath = "/run/secrets/ub_password_hash"
        }
        if data, err := os.ReadFile(hashPath); err == nil && strings.TrimSpace(string(data)) != "" {
            return false
        }
    }
    return true
}
```

**Testing:**

- platform-neutral-config.AC3.3: Empty DB + no env vars → `IsSetupRequired` returns `true`
- platform-neutral-config.AC3.5: Empty DB + UB_USERNAME + UB_PASSWORD_HASH env vars set → `IsSetupRequired` returns `false`
- Additional: DB has auth keys set → returns `false` regardless of env vars
- Additional: DB has username but no password hash → returns `true`

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/appconfig/`
Expected: All tests pass

**Commit:** `feat(appconfig): add IsSetupRequired for first-boot detection`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Add setup mode middleware and setup page handler

**Verifies:** platform-neutral-config.AC3.3, platform-neutral-config.AC3.4, platform-neutral-config.AC3.6

**Files:**
- Modify: `internal/web/handler.go` (add setup page handler, setup mode middleware)
- Modify: `internal/web/templates/index.html` (add setup page template section)
- Test: `internal/web/routes_test.go` (add setup mode tests)

**Implementation:**

**Setup mode middleware** — wraps the entire handler. Uses an `atomic.Bool` to cache the setup-required state, avoiding a DB query on every request after setup completes:

```go
// SetupMiddleware redirects all requests to /setup when no credentials exist,
// except for the setup endpoints themselves and static assets.
// Uses an atomic flag to cache setup state — once credentials are saved,
// subsequent requests skip the DB query entirely.
func SetupMiddleware(db *sql.DB, next http.Handler) http.Handler {
    var setupDone atomic.Bool
    // Check once at startup
    if !appconfig.IsSetupRequired(context.Background(), db) {
        setupDone.Store(true)
    }
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Allow setup page and setup save endpoint through
        if r.URL.Path == "/setup" || r.URL.Path == "/setup/save" {
            next.ServeHTTP(w, r)
            return
        }
        // Fast path: setup already complete
        if setupDone.Load() {
            next.ServeHTTP(w, r)
            return
        }
        // Slow path: check DB (only during setup mode)
        if appconfig.IsSetupRequired(r.Context(), db) {
            http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
            return
        }
        // Setup just completed (credentials saved via /setup/save)
        setupDone.Store(true)
        next.ServeHTTP(w, r)
    })
}
```

**Setup page handler** — `GET /setup` serves a credentials form; `POST /setup/save` hashes and saves:

```go
// handleSetup renders the credential setup page.
// Only accessible when IsSetupRequired is true.
func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
    if !appconfig.IsSetupRequired(r.Context(), h.noteDB) {
        http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
        return
    }
    h.tmpl.ExecuteTemplate(w, "setup.html", nil)
}

// handleSetupSave processes the setup form: validates, hashes password, saves credentials.
func (h *Handler) handleSetupSave(w http.ResponseWriter, r *http.Request) {
    if !appconfig.IsSetupRequired(r.Context(), h.noteDB) {
        http.Error(w, "Setup already complete", http.StatusForbidden)
        return
    }
    username := r.FormValue("username")
    password := r.FormValue("password")
    // Validate: both non-empty
    // Hash password with bcrypt
    hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    // Save via notedb.SetSetting for both username and password hash
    notedb.SetSetting(r.Context(), h.noteDB, appconfig.KeyUsername, username)
    notedb.SetSetting(r.Context(), h.noteDB, appconfig.KeyPasswordHash, string(hash))
    // Redirect to login (now enforced by auth middleware)
    http.Redirect(w, r, "/", http.StatusSeeOther)
}
```

**Middleware chain in main.go:** Setup middleware wraps auth middleware which wraps the handler:
```go
setupMw := web.SetupMiddleware(noteDB, authMw.Wrap(handler))
http.ListenAndServe(listenAddr, setupMw)
```

This means:
1. Request arrives at SetupMiddleware
2. If setup required → redirect to /setup (no auth needed)
3. If setup complete → pass to auth middleware → handler

**Setup page template:** Add a `setup.html` template (or a `setup` block within `index.html`) with a minimal form: username, password, confirm password, and a submit button. Styled consistently with the existing UI.

**Testing:**

- platform-neutral-config.AC3.3: GET `/` with empty DB → 307 redirect to `/setup`; GET `/setup` returns 200 with HTML setup form
- platform-neutral-config.AC3.4: POST `/setup/save` with username + password → credentials saved to DB → GET `/` now requires Basic Auth (returns 401)
- platform-neutral-config.AC3.6: GET `/api/search`, `/api/notes/pages`, `/settings` all redirect to `/setup` when in setup mode; `/setup/save` POST without valid username/password returns error
- Additional: GET `/setup` after credentials exist → redirects to `/` (can't re-enter setup)
- Additional: POST `/setup/save` after credentials exist → 403 Forbidden

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/web/`
Expected: All tests pass

**Commit:** `feat(web): add first-boot setup mode with credential setup page`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->
