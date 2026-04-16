package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthCodeFlow(t *testing.T) {
	handler := newTestHandler()

	t.Run("authorize issues a code and redirects", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/authorize?redirect_uri=https://example.com/callback&state=abc", nil)
		w := httptest.NewRecorder()
		handler.HandleOAuthAuthorize(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("want 302, got %d", w.Code)
		}
		loc, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatalf("bad Location: %v", err)
		}
		code := loc.Query().Get("code")
		if code == "" {
			t.Fatal("redirect missing code parameter")
		}
		if loc.Query().Get("state") != "abc" {
			t.Errorf("state not preserved: %s", loc.Query().Get("state"))
		}
		if len(code) < 20 {
			t.Errorf("code too short to be random: %q", code)
		}
	})

	t.Run("authorize rejects plain HTTP redirect_uri", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/authorize?redirect_uri=http://evil.com/phish", nil)
		w := httptest.NewRecorder()
		handler.HandleOAuthAuthorize(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", w.Code)
		}
	})

	t.Run("authorize allows localhost HTTP", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/authorize?redirect_uri=http://localhost:3000/callback", nil)
		w := httptest.NewRecorder()
		handler.HandleOAuthAuthorize(w, req)

		if w.Code != http.StatusFound {
			t.Errorf("want 302, got %d", w.Code)
		}
	})

	t.Run("token rejects missing code", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/token", strings.NewReader("grant_type=authorization_code"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.HandleOAuthToken(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", w.Code)
		}
	})

	t.Run("token rejects invalid code", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/token", strings.NewReader("code=bogus&grant_type=authorization_code"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.HandleOAuthToken(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", w.Code)
		}
	})

	t.Run("code is single-use", func(t *testing.T) {
		code := handler.generateOAuthCode()

		if !handler.consumeOAuthCode(code) {
			t.Fatal("first consume should succeed")
		}
		if handler.consumeOAuthCode(code) {
			t.Fatal("second consume should fail (single-use)")
		}
	})

	t.Run("expired code is rejected", func(t *testing.T) {
		code := "expired-test-code"
		handler.oauthCodesMu.Lock()
		if handler.oauthCodes == nil {
			handler.oauthCodes = make(map[string]time.Time)
		}
		handler.oauthCodes[code] = time.Now().Add(-1 * time.Minute)
		handler.oauthCodesMu.Unlock()

		if handler.consumeOAuthCode(code) {
			t.Fatal("expired code should be rejected")
		}
	})
}
