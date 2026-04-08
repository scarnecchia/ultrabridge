package rag

// FCIS: Imperative Shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Embedder generates embedding vectors from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// EmbedStore persists embeddings for pages.
// Implemented by *Store.
type EmbedStore interface {
	Save(ctx context.Context, notePath string, page int, embedding []float32, model string) error
}

// OllamaEmbedder calls Ollama's /api/embed endpoint.
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
	logger  *slog.Logger
}

func NewOllamaEmbedder(baseURL, model string, logger *slog.Logger) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
		logger:  logger,
	}
}

// Model returns the model name (used for storing in note_embeddings.model column).
func (e *OllamaEmbedder) Model() string { return e.model }

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("empty embeddings response")
	}

	// Convert float64 (JSON) to float32 (storage)
	f64 := result.Embeddings[0]
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32, nil
}
