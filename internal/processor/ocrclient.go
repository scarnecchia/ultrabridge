package processor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OCRClient posts JPEG images to a vision API and returns transcribed text.
// Compatible with Anthropic Messages API and OpenRouter (same request format).
type OCRClient struct {
	apiURL string
	apiKey string
	model  string
	client *http.Client
}

// NewOCRClient creates an OCRClient.
// apiURL is the API base (e.g. "https://api.anthropic.com" or "https://openrouter.ai/api").
func NewOCRClient(apiURL, apiKey, model string) *OCRClient {
	return &OCRClient{
		apiURL: apiURL,
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

type visionRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []vMsg `json:"messages"`
}

type vMsg struct {
	Role    string     `json:"role"`
	Content []vContent `json:"content"`
}

type vContent struct {
	Type   string   `json:"type"`
	Text   string   `json:"text,omitempty"`
	Source *vSource `json:"source,omitempty"`
}

type vSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"` // base64-encoded image bytes
}

type visionResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Recognize sends a JPEG page image to the vision API and returns the transcribed text.
func (c *OCRClient) Recognize(ctx context.Context, jpegData []byte) (string, error) {
	encoded := base64.StdEncoding.EncodeToString(jpegData)

	reqBody := visionRequest{
		Model:     c.model,
		MaxTokens: 4096,
		Messages: []vMsg{{
			Role: "user",
			Content: []vContent{
				{
					Type: "text",
					Text: "Transcribe all handwritten text from this page exactly as written. Return only the text, no commentary.",
				},
				{
					Type: "image",
					Source: &vSource{
						Type:      "base64",
						MediaType: "image/jpeg",
						Data:      encoded,
					},
				},
			},
		}},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ocrclient marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ocrclient request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Note: Direct Anthropic API uses "x-api-key" not "Authorization: Bearer".
	// This client targets OpenRouter (https://openrouter.ai/api) which accepts Bearer.
	// Set UB_OCR_API_URL=https://openrouter.ai/api for Anthropic model access via OpenRouter.
	// For direct Anthropic API, swap to: req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ocrclient post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ocrclient API error %d: %s", resp.StatusCode, b)
	}

	var vResp visionResponse
	if err := json.NewDecoder(resp.Body).Decode(&vResp); err != nil {
		return "", fmt.Errorf("ocrclient decode: %w", err)
	}
	if len(vResp.Content) == 0 {
		return "", fmt.Errorf("ocrclient: empty response")
	}
	return vResp.Content[0].Text, nil
}
