package main // FCIS: Imperative Shell

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	httpAddr := flag.String("http", "", "HTTP SSE address (e.g., :8081). If empty, uses stdio transport.")
	flag.Parse()

	apiURL := os.Getenv("UB_MCP_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8443"
	}
	apiUser := os.Getenv("UB_MCP_API_USER")
	apiPass := os.Getenv("UB_MCP_API_PASS")

	client := newAPIClient(apiURL, apiUser, apiPass)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ultrabridge-notes",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	if *httpAddr != "" {
		// HTTP SSE transport
		handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("ub-mcp listening on %s (HTTP SSE)", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, handler); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	} else {
		// stdio transport (default)
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("stdio server failed: %v", err)
		}
	}
}

// apiClient is an HTTP client for calling UltraBridge API endpoints.
type apiClient struct {
	baseURL  string
	user     string
	pass     string
	http     *http.Client
}

// newAPIClient creates a new API client.
func newAPIClient(baseURL, user, pass string) *apiClient {
	return &apiClient{
		baseURL: baseURL,
		user:    user,
		pass:    pass,
		http:    &http.Client{},
	}
}

// get performs a GET request to the UltraBridge API with Basic Auth.
func (c *apiClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	return c.http.Do(req)
}
