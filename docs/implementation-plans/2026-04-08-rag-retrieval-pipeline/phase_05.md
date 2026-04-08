# RAG Retrieval Pipeline — Phase 5: Local Chat Tab

**Goal:** Add a chat tab to the web UI that sends questions to a local text generation model (via vLLM), using retrieved note context for answers with citations.

**Architecture:** `POST /chat/ask` orchestrates: hybrid retrieval → prompt assembly → vLLM streaming → SSE proxy to browser. Chat history persisted in SQLite. Browser JS renders streaming markdown and linkifies `[filename, p.N]` citations.

**Tech Stack:** Go stdlib `net/http` + `bufio.Scanner` for SSE proxy, vLLM OpenAI-compatible API, Go `html/template` for chat UI, JavaScript for streaming render + citation links

**Scope:** 6 phases from original design (phase 5 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC5: Local Chat Tab
- **rag-retrieval-pipeline.AC5.1 Success:** New "Chat" tab in web UI with message input, conversation display, and SSE-streamed responses. Verified by: navigate to Chat tab, type question, see streaming response.
- **rag-retrieval-pipeline.AC5.2 Success:** `POST /chat/ask` accepts a question, runs hybrid retrieval, assembles prompt with retrieved context, calls vLLM, and streams response via SSE. Verified by: POST with question returns `text/event-stream` with incremental text chunks.
- **rag-retrieval-pipeline.AC5.3 Success:** Chat system prompt instructs the model to cite notes using `[filename, p.N]` format. Verified by: response includes citations matching retrieved notes.
- **rag-retrieval-pipeline.AC5.4 Success:** Chat UI linkifies `[filename, p.N]` citations as clickable links to `/files/history?path=...`. Verified by: citation in rendered response is a clickable link.
- **rag-retrieval-pipeline.AC5.5 Success:** Chat history persisted in SQLite (`chat_sessions`, `chat_messages` tables). Verified by: refresh page, previous conversation still visible.
- **rag-retrieval-pipeline.AC5.6 Success:** Chat tab is functional when vLLM is unreachable (shows error message, doesn't crash). Verified by: stop vLLM, send question, UI shows error.
- **rag-retrieval-pipeline.AC5.7 Success:** Configurable vLLM endpoint via `UB_CHAT_API_URL` and model via `UB_CHAT_MODEL`. Verified by: setting env vars changes which model/endpoint is used.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
## Subcomponent A: Chat Schema + Config (Infrastructure)

<!-- START_TASK_1 -->
### Task 1: Add chat tables and config fields

**Verifies:** None (infrastructure)

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notedb/schema.go:105` (add chat tables to `stmts` slice)
- Modify: `/home/jtd/ultrabridge/internal/config/config.go` (add chat config fields)

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/notedb/CLAUDE.md` for schema conventions.

**Implementation:**

Add to the `stmts` slice in `migrate()` in `schema.go`:

```go
`CREATE TABLE IF NOT EXISTS chat_sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
)`,
`CREATE TABLE IF NOT EXISTS chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES chat_sessions(id),
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at INTEGER NOT NULL
)`,
`CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id)`,
```

Add to Config struct in `config.go`:

```go
// Chat
ChatEnabled bool
ChatAPIURL  string
ChatModel   string
```

Add to `Load()`:

```go
cfg.ChatEnabled = envBoolOrDefault("UB_CHAT_ENABLED", false)
cfg.ChatAPIURL  = envOrDefault("UB_CHAT_API_URL", "http://localhost:8000")
cfg.ChatModel   = envOrDefault("UB_CHAT_MODEL", "Qwen/Qwen3-8B")
```

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go vet -C /home/jtd/ultrabridge ./...
```

**Commit:** `feat(notedb): add chat_sessions and chat_messages tables and chat config`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Verify chat schema migration

**Verifies:** rag-retrieval-pipeline.AC5.5 (schema part)

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notedb/db_test.go`

**Testing:**

Add test verifying `chat_sessions` and `chat_messages` tables exist after Open() with expected columns. Follow existing schema test patterns.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/notedb/ -run TestOpen -v
```

**Commit:** `test(notedb): verify chat schema migration`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
## Subcomponent B: Chat Store

<!-- START_TASK_3 -->
### Task 3: Create chat store for session/message CRUD

**Verifies:** rag-retrieval-pipeline.AC5.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/chat/store.go`

**Implementation:**

Create `internal/chat/store.go` with package `chat`:

```go
package chat

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Session struct {
	ID        int64
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID        int64
	SessionID int64
	Role      string // "user", "assistant", "system"
	Content   string
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateSession(ctx context.Context, title string) (*Session, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (title, created_at, updated_at) VALUES (?, ?, ?)`,
		title, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	id, _ := result.LastInsertId()
	return &Session{ID: id, Title: title, CreatedAt: time.Unix(now, 0), UpdatedAt: time.Unix(now, 0)}, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, created_at, updated_at FROM chat_sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var createdAt, updatedAt int64
		if err := rows.Scan(&sess.ID, &sess.Title, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		sess.CreatedAt = time.Unix(createdAt, 0)
		sess.UpdatedAt = time.Unix(updatedAt, 0)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *Store) AddMessage(ctx context.Context, sessionID int64, role, content string) (*Message, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, role, content, now,
	)
	if err != nil {
		return nil, fmt.Errorf("add message: %w", err)
	}
	// Update session's updated_at
	s.db.ExecContext(ctx, `UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, now, sessionID)

	id, _ := result.LastInsertId()
	return &Message{ID: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: time.Unix(now, 0)}, nil
}

func (s *Store) GetMessages(ctx context.Context, sessionID int64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, created_at FROM chat_messages WHERE session_id = ? ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Store) DeleteSession(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = ?`, sessionID)
	return err
}
```

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC5.5: Create session, add messages, retrieve messages in order. Delete session and verify messages also deleted.
- Session list returns sessions ordered by updated_at descending.
- Adding a message updates the session's updated_at.

Use `notedb.Open(context.Background(), ":memory:")`.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/chat/ -run TestStore -v
```

**Commit:** `feat(chat): add chat store for session/message CRUD`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Tests for chat store

**Verifies:** rag-retrieval-pipeline.AC5.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/chat/store_test.go`

**Testing:** (described in Task 3)

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/chat/ -v
```

**Commit:** `test(chat): add chat store tests`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-7) -->
## Subcomponent C: Chat Handler + SSE Streaming

<!-- START_TASK_5 -->
### Task 5: Create chat handler with vLLM SSE proxy

**Verifies:** rag-retrieval-pipeline.AC5.2, rag-retrieval-pipeline.AC5.3, rag-retrieval-pipeline.AC5.6, rag-retrieval-pipeline.AC5.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/chat/handler.go`

**Implementation:**

Create `internal/chat/handler.go` with the chat HTTP handler:

```go
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sysop/ultrabridge/internal/rag"
)

// Handler serves the chat SSE endpoint.
type Handler struct {
	store     *Store
	retriever *rag.Retriever
	apiURL    string
	model     string
	logger    *slog.Logger
}

func NewHandler(store *Store, retriever *rag.Retriever, apiURL, model string, logger *slog.Logger) *Handler {
	return &Handler{
		store:     store,
		retriever: retriever,
		apiURL:    apiURL,
		model:     model,
		logger:    logger,
	}
}

// HandleAsk handles POST /chat/ask. Orchestrates retrieval → prompt → vLLM → SSE proxy.
func (h *Handler) HandleAsk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID int64  `json:"session_id"`
		Question  string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Create session if needed
	if req.SessionID == 0 {
		sess, err := h.store.CreateSession(ctx, truncateTitle(req.Question, 50))
		if err != nil {
			h.logger.Error("create session", "err", err)
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}
		req.SessionID = sess.ID
	}

	// Save user message
	if _, err := h.store.AddMessage(ctx, req.SessionID, "user", req.Question); err != nil {
		h.logger.Error("save user message", "err", err)
	}

	// Retrieve relevant context
	results, err := h.retriever.Search(ctx, rag.SearchRequest{
		Query: req.Question,
		Limit: 5,
	})
	if err != nil {
		h.logger.Error("retrieval failed", "err", err)
		// Continue without context rather than failing
		results = nil
	}

	// Assemble prompt
	messages := h.buildPrompt(ctx, req.SessionID, req.Question, results)

	// Stream from vLLM to client
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send session_id as first event so client knows which session to display
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]interface{}{
		"type":       "session",
		"session_id": req.SessionID,
	}))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	fullResponse, err := h.streamFromVLLM(ctx, w, messages)
	if err != nil {
		h.logger.Error("vllm stream failed", "err", err)
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{
			"type":  "error",
			"error": "Failed to generate response. Is the chat model running?",
		}))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Save assistant response
	if _, err := h.store.AddMessage(ctx, req.SessionID, "assistant", fullResponse); err != nil {
		h.logger.Error("save assistant message", "err", err)
	}

	// Send done event
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": "done"}))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) buildPrompt(ctx context.Context, sessionID int64, question string, results []rag.SearchResult) []map[string]string {
	var messages []map[string]string

	// System prompt with citation instructions
	systemPrompt := `You are a helpful assistant that answers questions about handwritten notes. Use the provided note excerpts to answer the question. Always cite your sources using the format [filename, p.N] where filename is the note file name and N is the page number.

If the provided notes don't contain enough information to answer the question, say so clearly.`

	// Add retrieved context
	if len(results) > 0 {
		var contextBuilder strings.Builder
		contextBuilder.WriteString("\n\n--- Retrieved Notes ---\n")
		for _, r := range results {
			filename := filepath.Base(r.NotePath)
			contextBuilder.WriteString(fmt.Sprintf("\n[%s, p.%d]", filename, r.Page))
			if r.Device != "" {
				contextBuilder.WriteString(fmt.Sprintf(" (Device: %s", r.Device))
				if r.Folder != "" {
					contextBuilder.WriteString(fmt.Sprintf(", Folder: %s", r.Folder))
				}
				contextBuilder.WriteString(")")
			}
			contextBuilder.WriteString(":\n")
			contextBuilder.WriteString(r.BodyText)
			contextBuilder.WriteString("\n")
		}
		systemPrompt += contextBuilder.String()
	}

	messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})

	// Add conversation history (last 10 messages for context window management)
	history, _ := h.store.GetMessages(ctx, sessionID)
	// Skip the just-added user message (last one) and limit history
	if len(history) > 1 {
		start := 0
		if len(history) > 11 {
			start = len(history) - 11
		}
		for _, m := range history[start : len(history)-1] {
			messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
		}
	}

	// Add current question
	messages = append(messages, map[string]string{"role": "user", "content": question})

	return messages
}

func (h *Handler) streamFromVLLM(ctx context.Context, w http.ResponseWriter, messages []map[string]string) (string, error) {
	reqBody := map[string]interface{}{
		"model":       h.model,
		"messages":    messages,
		"stream":      true,
		"temperature": 0.7,
		"max_tokens":  2048,
	}
	body, _ := json.Marshal(reqBody)

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", h.apiURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vllm connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vllm returned %d: %s", resp.StatusCode, string(respBody))
	}

	flusher, _ := w.(http.Flusher)
	var fullResponse strings.Builder

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if line == "data: [DONE]" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			content := chunk.Choices[0].Delta.Content
			fullResponse.WriteString(content)

			// Forward as SSE to browser
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{
				"type":    "content",
				"content": content,
			}))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}

	return fullResponse.String(), scanner.Err()
}

func truncateTitle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
```

Key design decisions:
- SSE proxy pattern: reads vLLM SSE line by line, extracts content deltas, re-formats as our own SSE events to browser
- Custom SSE event types: `session` (initial), `content` (streaming tokens), `error` (failure), `done` (complete)
- Conversation history limited to last 10 messages to manage context window
- System prompt includes retrieved notes with citation format instructions (AC5.3)
- vLLM unreachable → error event sent to browser (AC5.6)
- Model and API URL from config (AC5.7)
- Full response accumulated for persistence in chat_messages (AC5.5)

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC5.2: Mock vLLM server that streams SSE chunks. POST to HandleAsk, verify response is `text/event-stream` with content chunks.
- rag-retrieval-pipeline.AC5.3: Verify system prompt in the messages sent to vLLM includes citation instructions and retrieved note text.
- rag-retrieval-pipeline.AC5.6: Mock vLLM server that is unreachable. Verify error event is sent (not a crash).
- rag-retrieval-pipeline.AC5.7: Verify handler uses configured apiURL and model.

Use `httptest.NewServer` for mock vLLM and `httptest.NewRecorder` for the handler.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/chat/ -run TestHandler -v
```

**Commit:** `feat(chat): add vLLM SSE streaming handler with retrieval context`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Tests for chat handler

**Verifies:** rag-retrieval-pipeline.AC5.2, rag-retrieval-pipeline.AC5.3, rag-retrieval-pipeline.AC5.6

**Files:**
- Create: `/home/jtd/ultrabridge/internal/chat/handler_test.go`

**Testing:** (described in Task 5)

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/chat/ -v
```

**Commit:** `test(chat): add chat handler tests`
<!-- END_TASK_6 -->

<!-- START_TASK_7 -->
### Task 7: Wire chat routes and add Chat tab UI

**Verifies:** rag-retrieval-pipeline.AC5.1, rag-retrieval-pipeline.AC5.4

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add chat fields, routes, chat page handler)
- Modify: `/home/jtd/ultrabridge/internal/web/templates/index.html` (add Chat tab button and content)
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go` (wire chat handler)

**Implementation:**

**Handler changes:**

Add `chatHandler *chat.Handler` and `chatStore *chat.Store` fields to Handler struct (nil-safe). Add to `NewHandler` parameters.

Register routes (nil-check for chatHandler):

```go
if h.chatHandler != nil {
    h.mux.HandleFunc("GET /chat", h.handleChat)
    h.mux.HandleFunc("POST /chat/ask", h.chatHandler.HandleAsk)
    h.mux.HandleFunc("GET /chat/sessions", h.handleChatSessions)
    h.mux.HandleFunc("GET /chat/messages", h.handleChatMessages)
}
```

Add `handleChat` to render the chat page:

```go
func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
    data := h.baseTemplateData(r.Context())
    data["activeTab"] = "chat"
    data["chatEnabled"] = h.chatHandler != nil
    if h.chatStore != nil {
        sessions, _ := h.chatStore.ListSessions(r.Context())
        data["chatSessions"] = sessions
    }
    h.tmpl.ExecuteTemplate(w, "index.html", data)
}
```

**Template changes (index.html):**

Add Chat tab button (conditionally, when chatEnabled):

```html
{{if .chatEnabled}}
<a class="tab-button {{if eq .activeTab "chat"}}active{{end}}" href="/chat" style="text-decoration:none;">Chat</a>
{{end}}
```

Add Chat tab content div with:
- Session sidebar (list of past conversations)
- Message display area
- Input form
- JavaScript for SSE handling and citation linkification

The JavaScript should:
1. POST to `/chat/ask` with `{question, session_id}` via `fetch`
2. Read SSE events via `EventSource` or manual fetch+stream reading
3. Append content chunks to message display area
4. On `done` event, parse the full response for `[filename, p.N]` citations
5. Replace citations with `<a href="/files/history?path=...">` links using note paths from the search results

**Citation linkification (AC5.4):**

The retrieved context passes note_paths alongside the response. The browser JS can use a regex to find `[filename, p.N]` patterns and linkify them. Since the system prompt tells the model to cite with filenames, and we know the mapping from filename to note_path (from the retrieval context), the JS can create clickable links.

A simpler approach: include the search results (note_path + filename) in the SSE `session` event, so the browser has the mapping. Then regex-replace `[filename, p.N]` with `<a href="/files/history?path=NOTE_PATH">filename, p.N</a>`.

**Main wiring:**

Create chat handler if enabled:

```go
var chatHandler *chat.Handler
var chatStore *chat.Store
if cfg.ChatEnabled {
    chatStore = chat.NewStore(noteDB)
    chatHandler = chat.NewHandler(chatStore, retriever, cfg.ChatAPIURL, cfg.ChatModel, logger)
}
```

Pass `chatHandler` and `chatStore` to `NewHandler`.

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC5.1: GET /chat returns 200 with Chat tab visible when chatEnabled
- rag-retrieval-pipeline.AC5.1: GET / does NOT show Chat tab when chatHandler is nil
- rag-retrieval-pipeline.AC5.4: Verify the JavaScript citation regex pattern correctly matches `[filename, p.N]` format (test via inline script or manually)

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./internal/web/ -run TestChat -v
go test -C /home/jtd/ultrabridge ./...
```

**Commit:** `feat(web): add Chat tab with SSE streaming UI and citation links`
<!-- END_TASK_7 -->
<!-- END_SUBCOMPONENT_C -->

<!-- START_TASK_8 -->
### Task 8: Final build and full test suite verification

**Verifies:** None (verification checkpoint)

**Files:** None

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: All binaries build. All tests pass.

**Commit:** No commit — verification only.
<!-- END_TASK_8 -->
