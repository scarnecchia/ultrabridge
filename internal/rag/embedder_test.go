package rag

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOllamaEmbedder_ConnectionError verifies AC1.7:
// When Ollama server is unreachable, Embed() returns an error (not a panic).
func TestOllamaEmbedder_ConnectionError(t *testing.T) {
	logger := slog.Default()

	// Use a port that is unlikely to be listening
	embedder := NewOllamaEmbedder("http://127.0.0.1:59999", "test-model", logger)

	ctx := context.Background()
	_, err := embedder.Embed(ctx, "test text")

	if err == nil {
		t.Fatal("expected error on connection failure, got nil")
	}
	// Should contain a meaningful error message
	t.Logf("Got expected error: %v", err)
}

// TestOllamaEmbedder_NonOKStatus verifies AC1.7:
// When Ollama returns non-200 status (e.g., 404), Embed() returns a descriptive error.
func TestOllamaEmbedder_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("model not found"))
	}))
	defer server.Close()

	logger := slog.Default()
	embedder := NewOllamaEmbedder(server.URL, "missing-model", logger)

	ctx := context.Background()
	_, err := embedder.Embed(ctx, "test text")

	if err == nil {
		t.Fatal("expected error on non-200 status, got nil")
	}
	// Should mention the status code
	t.Logf("Got expected error: %v", err)
}

// TestOllamaEmbedder_HappyPath verifies happy path:
// Mock HTTP server returns valid embedding response, Embed() returns correct []float32.
func TestOllamaEmbedder_HappyPath(t *testing.T) {
	// Create mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		if req.Model != "test-model" {
			t.Errorf("expected model 'test-model', got %q", req.Model)
		}

		// Return valid embedding response
		// Return 3-dim vector for testing (768 dim would be real)
		resp := embedResponse{
			Embeddings: [][]float64{
				{0.1, 0.2, 0.3},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.Default()
	embedder := NewOllamaEmbedder(server.URL, "test-model", logger)

	ctx := context.Background()
	vec, err := embedder.Embed(ctx, "test text")

	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != 3 {
		t.Errorf("expected 3-dim vector, got %d", len(vec))
	}

	// Verify float32 conversion
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("expected [0.1, 0.2, 0.3], got %v", vec)
	}
}

// TestOllamaEmbedder_EmptyResponse verifies error on empty embeddings response.
func TestOllamaEmbedder_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := embedResponse{
			Embeddings: [][]float64{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.Default()
	embedder := NewOllamaEmbedder(server.URL, "test-model", logger)

	ctx := context.Background()
	_, err := embedder.Embed(ctx, "test text")

	if err == nil {
		t.Fatal("expected error on empty embeddings response, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

// BenchmarkEmbed measures round-trip time for a single Embed() call against a mock server.
// Establishes baseline for AC1.8.
func BenchmarkEmbed(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := embedResponse{
			Embeddings: [][]float64{
				{0.1, 0.2, 0.3},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.Default()
	embedder := NewOllamaEmbedder(server.URL, "test-model", logger)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := embedder.Embed(ctx, "test text for embedding")
		if err != nil {
			b.Fatalf("Embed failed: %v", err)
		}
	}
}
