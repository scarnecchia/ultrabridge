package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The service worker must not precache HTML pages (specifically "/"), must bump
// its cache name so existing installs drop stale entries, must use a
// network-first strategy for navigation requests, must delete old caches on
// activate, and must activate immediately via skipWaiting + clients.claim.
// Regression guard for the "phantom task" bug where / was precached with a
// cache-first fetch handler.
func TestServiceWorkerCacheStrategy(t *testing.T) {
	raw, err := os.ReadFile("static/sw.js")
	if err != nil {
		t.Fatalf("read sw.js: %v", err)
	}
	src := string(raw)

	t.Run("does not precache root HTML", func(t *testing.T) {
		precache := extractPrecacheList(t, src)
		for _, entry := range precache {
			if entry == "/" {
				t.Errorf("sw.js precache list must not include '/' (HTML pages go stale): got %v", precache)
			}
		}
	})

	t.Run("cache name bumped past v1", func(t *testing.T) {
		re := regexp.MustCompile(`const\s+CACHE_NAME\s*=\s*['"]([^'"]+)['"]`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("sw.js must define CACHE_NAME")
		}
		if m[1] == "ultrabridge-v1" {
			t.Errorf("CACHE_NAME must be bumped past ultrabridge-v1 so existing installs invalidate, got %q", m[1])
		}
	})

	t.Run("navigation requests use network-first", func(t *testing.T) {
		if !strings.Contains(src, "request.mode") && !strings.Contains(src, "req.mode") {
			t.Errorf("sw.js fetch handler must branch on request.mode to detect navigation requests")
		}
		if !regexp.MustCompile(`['"]navigate['"]`).MatchString(src) {
			t.Errorf("sw.js fetch handler must special-case mode === 'navigate'")
		}
	})

	t.Run("activate handler deletes old caches", func(t *testing.T) {
		if !strings.Contains(src, "activate") {
			t.Errorf("sw.js must register an activate handler")
		}
		if !strings.Contains(src, "caches.keys") || !strings.Contains(src, "caches.delete") {
			t.Errorf("sw.js activate handler must enumerate caches.keys() and caches.delete() stale caches")
		}
	})

	t.Run("activates immediately on new install", func(t *testing.T) {
		if !strings.Contains(src, "skipWaiting") {
			t.Errorf("sw.js must call self.skipWaiting() so fixes reach users without closing every tab")
		}
		if !strings.Contains(src, "clients.claim") {
			t.Errorf("sw.js must call self.clients.claim() so new SW takes over open tabs")
		}
	})
}

// extractPrecacheList parses the ASSETS/PRECACHE_ASSETS array out of sw.js so
// the test asserts against the real list, not a substring match.
func extractPrecacheList(t *testing.T, src string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)const\s+(?:ASSETS|PRECACHE_ASSETS)\s*=\s*\[(.*?)\]`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("sw.js must define a precache array named ASSETS or PRECACHE_ASSETS")
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "'\"")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
