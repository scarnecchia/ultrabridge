package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestWrapValidCredentials(t *testing.T) {
	// AC5.1: Valid credentials grant access
	testPassword := "correct_password"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate password hash: %v", err)
	}

	m := New("testuser", string(passwordHash))

	// Track whether wrapped handler was called
	handlerCalled := false
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	handler := m.Wrap(wrappedHandler)

	// Create request with valid credentials
	req := httptest.NewRequest("GET", "/caldav/", nil)
	credentials := "testuser" + ":" + testPassword
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
	req.Header.Set("Authorization", "Basic "+encodedCreds)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify wrapped handler was called
	if !handlerCalled {
		t.Error("expected wrapped handler to be called with valid credentials, but it was not")
	}

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != "success" {
		t.Errorf("expected response 'success', got %q", rec.Body.String())
	}
}

func TestWrapNoAuthHeader(t *testing.T) {
	// AC5.2: Missing auth header returns 401
	testPassword := "password"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate password hash: %v", err)
	}

	m := New("testuser", string(passwordHash))

	handlerCalled := false
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Wrap(wrappedHandler)

	// Request without auth header
	req := httptest.NewRequest("GET", "/caldav/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify handler was not called
	if handlerCalled {
		t.Error("expected wrapped handler not to be called without credentials, but it was")
	}

	// Verify 401 response
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	// Verify WWW-Authenticate header is set
	if rec.Header().Get("WWW-Authenticate") != `Basic realm="UltraBridge"` {
		t.Errorf("expected WWW-Authenticate header, got %q", rec.Header().Get("WWW-Authenticate"))
	}

	// Verify response body contains no credential hints
	body := rec.Body.String()
	if len(body) == 0 {
		t.Error("expected response body")
	}
	// Ensure no sensitive info in response
	if strings.Contains(body, "password") || strings.Contains(body, "hash") || strings.Contains(body, "testuser") {
		t.Errorf("response body contains credential hints: %q", body)
	}
}

func TestWrapWrongUsername(t *testing.T) {
	// AC5.2: Wrong username returns 401
	testPassword := "password"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate password hash: %v", err)
	}

	m := New("testuser", string(passwordHash))

	handlerCalled := false
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Wrap(wrappedHandler)

	// Request with wrong username
	req := httptest.NewRequest("GET", "/caldav/", nil)
	credentials := "wronguser" + ":" + testPassword
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
	req.Header.Set("Authorization", "Basic "+encodedCreds)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify handler was not called
	if handlerCalled {
		t.Error("expected wrapped handler not to be called with wrong username, but it was")
	}

	// Verify 401 response
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	// Verify no credential hints
	body := rec.Body.String()
	if strings.Contains(body, "testuser") || strings.Contains(body, "wronguser") {
		t.Errorf("response body contains credential hints: %q", body)
	}
}

func TestWrapWrongPassword(t *testing.T) {
	// AC5.2: Wrong password returns 401
	testPassword := "correct_password"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate password hash: %v", err)
	}

	m := New("testuser", string(passwordHash))

	handlerCalled := false
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Wrap(wrappedHandler)

	// Request with wrong password
	req := httptest.NewRequest("GET", "/caldav/", nil)
	credentials := "testuser" + ":" + "wrong_password"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
	req.Header.Set("Authorization", "Basic "+encodedCreds)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify handler was not called
	if handlerCalled {
		t.Error("expected wrapped handler not to be called with wrong password, but it was")
	}

	// Verify 401 response
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	// Verify no credential hints (especially no hash info)
	body := rec.Body.String()
	if strings.Contains(body, "hash") || strings.Contains(body, "correct_password") || strings.Contains(body, "wrong_password") {
		t.Errorf("response body contains credential hints: %q", body)
	}
}

func TestTimingSafeUsernameComparison(t *testing.T) {
	// Implicit test for timing-safe comparison via subtle.ConstantTimeCompare
	// If the implementation uses subtle.ConstantTimeCompare, it should reject
	// both short and long username mismatches in constant time
	testPassword := "password"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate password hash: %v", err)
	}

	m := New("admin", string(passwordHash))

	tests := []struct {
		name     string
		username string
	}{
		{"short mismatch", "a"},
		{"partial match", "adm"},
		{"longer mismatch", "administrator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			handler := m.Wrap(wrappedHandler)

			req := httptest.NewRequest("GET", "/caldav/", nil)
			credentials := tt.username + ":" + testPassword
			encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
			req.Header.Set("Authorization", "Basic "+encodedCreds)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 for username %q, got %d", tt.username, rec.Code)
			}
		})
	}
}
