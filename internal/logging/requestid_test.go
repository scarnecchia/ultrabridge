package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey, "test-123")
	id := RequestIDFromContext(ctx)
	if id != "test-123" {
		t.Errorf("expected 'test-123', got '%s'", id)
	}
}

func TestRequestIDFromContextEmpty(t *testing.T) {
	ctx := context.Background()
	id := RequestIDFromContext(ctx)
	if id != "" {
		t.Errorf("expected empty string, got '%s'", id)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	var buf bytes.Buffer
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(&buf, opts)
	logger := slog.New(handler)

	// Create a simple handler that logs
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Wrap with middleware
	middleware := RequestID(logger)
	wrappedHandler := middleware(innerHandler)

	// Make a test request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	// Check response
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Parse logged output
	output := buf.String()
	var logEntry map[string]interface{}
	err := json.Unmarshal([]byte(output), &logEntry)
	if err != nil {
		t.Fatalf("failed to parse log output: %v\nOutput: %s", err, output)
	}

	// Verify request_id is present
	if _, ok := logEntry["request_id"]; !ok {
		t.Errorf("expected 'request_id' in log output, got: %v", logEntry)
	}
	requestID, ok := logEntry["request_id"].(string)
	if !ok || requestID == "" {
		t.Errorf("expected non-empty request_id string, got: %v", logEntry["request_id"])
	}

	// Verify method
	if logEntry["method"] != "GET" {
		t.Errorf("expected method='GET', got %v", logEntry["method"])
	}

	// Verify path
	if logEntry["path"] != "/test" {
		t.Errorf("expected path='/test', got %v", logEntry["path"])
	}

	// Verify status
	if logEntry["status"] != float64(http.StatusOK) {
		t.Errorf("expected status=200, got %v", logEntry["status"])
	}

	// Verify duration_ms is present
	if _, ok := logEntry["duration_ms"]; !ok {
		t.Errorf("expected 'duration_ms' in log output")
	}
}

func TestRequestIDInjectedIntoContext(t *testing.T) {
	var buf bytes.Buffer
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(&buf, opts)
	logger := slog.New(handler)

	var capturedID string

	// Inner handler that captures the request ID from context
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestID(logger)
	wrappedHandler := middleware(innerHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	// Verify the request ID was injected into context
	if capturedID == "" {
		t.Errorf("expected request ID to be injected into context")
	}

	// Verify the same ID is in the log output
	output := buf.String()
	var logEntry map[string]interface{}
	json.Unmarshal([]byte(output), &logEntry)

	loggedID := logEntry["request_id"].(string)
	if capturedID != loggedID {
		t.Errorf("context ID '%s' != logged ID '%s'", capturedID, loggedID)
	}
}

func TestRequestIDWithDifferentMethods(t *testing.T) {
	var buf bytes.Buffer
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(&buf, opts)
	logger := slog.New(handler)

	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestID(logger)
	wrappedHandler := middleware(innerHandler)

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		buf.Reset()
		req := httptest.NewRequest(method, "/test", nil)
		rr := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rr, req)

		output := buf.String()
		var logEntry map[string]interface{}
		json.Unmarshal([]byte(output), &logEntry)

		if logEntry["method"] != method {
			t.Errorf("expected method=%s, got %v", method, logEntry["method"])
		}
	}
}

func TestRequestIDWithDifferentStatus(t *testing.T) {
	var buf bytes.Buffer
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(&buf, opts)
	logger := slog.New(handler)

	testCases := []int{http.StatusOK, http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError}

	for _, expectedStatus := range testCases {
		buf.Reset()

		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(expectedStatus)
		})

		middleware := RequestID(logger)
		wrappedHandler := middleware(innerHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rr, req)

		output := buf.String()
		var logEntry map[string]interface{}
		json.Unmarshal([]byte(output), &logEntry)

		if logEntry["status"] != float64(expectedStatus) {
			t.Errorf("expected status=%d, got %v", expectedStatus, logEntry["status"])
		}
	}
}
