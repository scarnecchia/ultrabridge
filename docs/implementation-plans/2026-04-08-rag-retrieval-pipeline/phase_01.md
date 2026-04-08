# RAG Retrieval Pipeline — Phase 1: Embedding Infrastructure

**Goal:** Create the embedding pipeline that generates and stores vectors from OCR'd note content via Ollama.

**Architecture:** Ollama HTTP client calls `/api/embed` for each indexed page. Embeddings stored as float32 BLOBs in SQLite `note_embeddings` table. In-memory cache loaded on startup for fast retrieval. Embedder wired into both Boox and Supernote workers as an optional nil-safe dependency.

**Tech Stack:** Go stdlib `net/http` for Ollama client, `encoding/binary` for float32↔BLOB conversion, `modernc.org/sqlite` (existing), nomic-embed-text:v1.5 (768-dim)

**Scope:** 6 phases from original design (phase 1 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC1: Embedding Pipeline
- **rag-retrieval-pipeline.AC1.1 Success:** `note_embeddings` table exists in notedb with columns: `note_path TEXT`, `page INTEGER`, `embedding BLOB`, `model TEXT`, `created_at INTEGER`, `UNIQUE(note_path, page)`.
- **rag-retrieval-pipeline.AC1.2 Success:** After OCR indexing completes for a Boox note page, the worker calls the embedder and stores a 768-dim float32 vector in `note_embeddings`. Verified by: process a .note file, query `SELECT count(*) FROM note_embeddings WHERE note_path = ?` returns page count.
- **rag-retrieval-pipeline.AC1.3 Success:** After OCR indexing completes for a Supernote note page, the same embedding flow runs. Verified by: process a Supernote .note file, check `note_embeddings` row exists.
- **rag-retrieval-pipeline.AC1.4 Success:** On startup, pages in `note_content` without a corresponding `note_embeddings` row are automatically backfilled. Verified by: delete embeddings, restart, embeddings regenerated.
- **rag-retrieval-pipeline.AC1.5 Success:** Backfill can be triggered manually via a Settings UI button or API endpoint. Verified by: trigger endpoint, observe backfill log entries.
- **rag-retrieval-pipeline.AC1.6 Success:** Embeddings are loaded into an in-memory vector cache on startup. Verified by: startup log shows "loaded N embeddings into memory".
- **rag-retrieval-pipeline.AC1.7 Success:** If Ollama is unreachable, embedding failure is logged but does not block OCR indexing. The page proceeds without an embedding. Verified by: stop Ollama, process a file, OCR completes, no embedding row created.
- **rag-retrieval-pipeline.AC1.8 Success:** Embedding generation adds <500ms per page to the OCR pipeline. Verified by: compare pipeline timing with and without embedder.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
## Subcomponent A: Schema + Config (Infrastructure)

<!-- START_TASK_1 -->
### Task 1: Add note_embeddings table and embedding config fields

**Verifies:** None (infrastructure)

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notedb/schema.go:105` (add to `stmts` slice before the closing `}`)
- Modify: `/home/jtd/ultrabridge/internal/config/config.go:70-74` (add fields to Config struct)
- Modify: `/home/jtd/ultrabridge/internal/config/config.go:116-118` (add loading in Load())

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/notedb/CLAUDE.md` for schema conventions.

**Implementation:**

In `internal/notedb/schema.go`, add this `CREATE TABLE` statement to the `stmts` slice (after the `settings` table, before the closing of the slice):

```go
`CREATE TABLE IF NOT EXISTS note_embeddings (
    note_path  TEXT NOT NULL,
    page       INTEGER NOT NULL,
    embedding  BLOB NOT NULL,
    model      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(note_path, page)
)`,
```

No foreign key needed — note_content uses the same `(note_path, page)` pair but without a FK to notes, so follow the same pattern. The backfill will match against note_content rows.

In `internal/config/config.go`, add fields to the Config struct after the Boox section (after line 73):

```go
// Embedding pipeline
EmbedEnabled   bool
OllamaURL      string
OllamaEmbedModel string
```

In the `Load()` function, after the Boox config loading (after line 118):

```go
cfg.EmbedEnabled     = envBoolOrDefault("UB_EMBED_ENABLED", false)
cfg.OllamaURL        = envOrDefault("UB_OLLAMA_URL", "http://localhost:11434")
cfg.OllamaEmbedModel = envOrDefault("UB_OLLAMA_EMBED_MODEL", "nomic-embed-text:v1.5")
```

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go vet -C /home/jtd/ultrabridge ./...
```

Expected: Both succeed with no errors.

**Commit:** `feat(notedb): add note_embeddings table and embedding config fields`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Verify schema migration creates table

**Verifies:** rag-retrieval-pipeline.AC1.1

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notedb/db_test.go` (add test case)

**Implementation:**

Add a test to `db_test.go` that opens an in-memory database and verifies the `note_embeddings` table exists with the expected columns. Follow the existing `TestOpen_CreatesSchema` pattern in the same file.

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.1: `note_embeddings` table exists after `Open()` with columns `note_path`, `page`, `embedding`, `model`, `created_at`
- The `UNIQUE(note_path, page)` constraint is enforced (insert two rows with same note_path+page, second should fail or replace depending on INSERT mode)

Follow the existing pattern in `db_test.go` which uses `notedb.Open(context.Background(), ":memory:")` and queries `pragma_table_info`.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/notedb/ -run TestOpen -v
```

Expected: All tests pass.

**Commit:** `test(notedb): verify note_embeddings schema migration`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
## Subcomponent B: Ollama Embedder Client

<!-- START_TASK_3 -->
### Task 3: Create Ollama embedder HTTP client

**Verifies:** rag-retrieval-pipeline.AC1.7, rag-retrieval-pipeline.AC1.8

**Files:**
- Create: `/home/jtd/ultrabridge/internal/rag/embedder.go`

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/processor/CLAUDE.md` for how the OCR client pattern works (similar HTTP client design).

**Implementation:**

Create `internal/rag/embedder.go` with package `rag`. The embedder is a raw HTTP client (no Ollama SDK dependency — matches the OCR client pattern in `internal/processor/`).

Define the `Embedder` interface and implementation:

```go
package rag

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
```

Key design decisions:
- 30-second timeout: embedding is fast (~150ms) but allows for model cold-start
- Returns `[]float32`: halves storage vs float64 (768×4=3072 bytes per embedding)
- Ollama returns float64 in JSON; we convert to float32 for storage efficiency
- `Model()` method exposes model name for the `model` column in note_embeddings

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.7: When Ollama server is unreachable (connection refused), `Embed()` returns an error (not a panic). The worker code calling this will log the error and continue — that's tested in Task 7.
- rag-retrieval-pipeline.AC1.7: When Ollama returns non-200 status (e.g., 404 model not found), `Embed()` returns a descriptive error.
- Happy path: Mock HTTP server returns valid embedding response, `Embed()` returns correct []float32 of expected length.

Use `httptest.NewServer` to mock the Ollama endpoint (same pattern as `mockOCRServer` in `/home/jtd/ultrabridge/internal/booxpipeline/processor_test.go:51-72`).

- rag-retrieval-pipeline.AC1.8: Add a benchmark test (`BenchmarkEmbed`) that measures round-trip time for a single `Embed()` call against the mock server. This establishes a baseline. Actual <500ms verification requires a real Ollama instance (~150ms per page typical), which is validated during manual testing. The benchmark ensures the Go HTTP client overhead is negligible.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/rag/ -run TestOllamaEmbedder -v
```

Expected: All tests pass.

**Commit:** `feat(rag): add Ollama embedder HTTP client`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Create embedding store with in-memory cache

**Verifies:** rag-retrieval-pipeline.AC1.1, rag-retrieval-pipeline.AC1.6

**Files:**
- Create: `/home/jtd/ultrabridge/internal/rag/store.go`

**Implementation:**

Create `internal/rag/store.go` with package `rag`. The store handles CRUD for `note_embeddings` and maintains an in-memory vector cache.

```go
package rag

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// EmbeddingRecord represents one stored embedding.
type EmbeddingRecord struct {
	NotePath  string
	Page      int
	Embedding []float32
}

// Store manages note_embeddings CRUD and an in-memory vector cache.
type Store struct {
	db     *sql.DB
	logger *slog.Logger

	mu    sync.RWMutex
	cache []EmbeddingRecord // all embeddings loaded into memory
}

func NewStore(db *sql.DB, logger *slog.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// Save inserts or replaces an embedding for (note_path, page).
// Also updates the in-memory cache.
func (s *Store) Save(ctx context.Context, notePath string, page int, embedding []float32, model string) error {
	blob := float32sToBytes(embedding)
	now := time.Now().Unix()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO note_embeddings (note_path, page, embedding, model, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(note_path, page) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		notePath, page, blob, model, now,
	)
	if err != nil {
		return fmt.Errorf("save embedding: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update or append in cache
	found := false
	for i := range s.cache {
		if s.cache[i].NotePath == notePath && s.cache[i].Page == page {
			s.cache[i].Embedding = embedding
			found = true
			break
		}
	}
	if !found {
		s.cache = append(s.cache, EmbeddingRecord{
			NotePath:  notePath,
			Page:      page,
			Embedding: embedding,
		})
	}

	return nil
}

// LoadAll reads all embeddings from the database into the in-memory cache.
// Call this on startup.
func (s *Store) LoadAll(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT note_path, page, embedding FROM note_embeddings`)
	if err != nil {
		return 0, fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	var records []EmbeddingRecord
	for rows.Next() {
		var rec EmbeddingRecord
		var blob []byte
		if err := rows.Scan(&rec.NotePath, &rec.Page, &blob); err != nil {
			return 0, fmt.Errorf("scan embedding: %w", err)
		}
		rec.Embedding = bytesToFloat32s(blob)
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate embeddings: %w", err)
	}

	s.mu.Lock()
	s.cache = records
	s.mu.Unlock()

	return len(records), nil
}

// AllEmbeddings returns a snapshot of all cached embeddings.
// Used by the retriever for vector similarity search.
func (s *Store) AllEmbeddings() []EmbeddingRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EmbeddingRecord, len(s.cache))
	copy(out, s.cache)
	return out
}

// UnembeddedPages returns (note_path, page, body_text) for pages in note_content
// that have no corresponding note_embeddings row.
func (s *Store) UnembeddedPages(ctx context.Context) ([]struct {
	NotePath string
	Page     int
	BodyText string
}, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT nc.note_path, nc.page, nc.body_text
		 FROM note_content nc
		 LEFT JOIN note_embeddings ne ON nc.note_path = ne.note_path AND nc.page = ne.page
		 WHERE ne.note_path IS NULL AND nc.body_text != ''`)
	if err != nil {
		return nil, fmt.Errorf("query unembedded: %w", err)
	}
	defer rows.Close()

	var pages []struct {
		NotePath string
		Page     int
		BodyText string
	}
	for rows.Next() {
		var p struct {
			NotePath string
			Page     int
			BodyText string
		}
		if err := rows.Scan(&p.NotePath, &p.Page, &p.BodyText); err != nil {
			return nil, fmt.Errorf("scan unembedded: %w", err)
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

// float32sToBytes converts a float32 slice to a byte slice (little-endian).
func float32sToBytes(fs []float32) []byte {
	buf := make([]byte, len(fs)*4)
	for i, f := range fs {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// bytesToFloat32s converts a byte slice to a float32 slice (little-endian).
func bytesToFloat32s(b []byte) []float32 {
	n := len(b) / 4
	fs := make([]float32, n)
	for i := range fs {
		fs[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return fs
}

// CosineSimilarity computes the cosine similarity between two vectors.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(normA)*float64(normB)))
}
```

Key design decisions:
- `LoadAll()` returns count for startup logging (AC1.6: "loaded N embeddings into memory")
- `UnembeddedPages()` is the query for backfill (AC1.4) — finds note_content rows without embeddings
- `Save()` updates in-memory cache immediately after DB write (no stale cache)
- `AllEmbeddings()` returns a copy (concurrent-safe for retriever)
- Little-endian float32 BLOB format: 768 dims × 4 bytes = 3072 bytes per embedding
- `CosineSimilarity` exposed as package function for Phase 2 retriever

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.1: `Save()` writes to note_embeddings table and can be read back with correct values (round-trip float32→BLOB→float32)
- rag-retrieval-pipeline.AC1.1: `Save()` with same (note_path, page) updates existing row (UPSERT behavior)
- rag-retrieval-pipeline.AC1.6: `LoadAll()` returns correct count and populates cache. `AllEmbeddings()` returns the loaded records.
- `UnembeddedPages()` returns only pages without embeddings (insert note_content rows, some with embeddings, some without — verify correct set returned)
- `CosineSimilarity` returns 1.0 for identical vectors, 0.0 for orthogonal vectors

Use `notedb.Open(context.Background(), ":memory:")` for the database (same pattern as existing tests).

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/rag/ -run TestStore -v
go test -C /home/jtd/ultrabridge ./internal/rag/ -run TestCosine -v
```

Expected: All tests pass.

**Commit:** `feat(rag): add embedding store with in-memory cache`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-6) -->
## Subcomponent C: Worker Integration

<!-- START_TASK_5 -->
### Task 5: Wire embedder into Boox worker

**Verifies:** rag-retrieval-pipeline.AC1.2, rag-retrieval-pipeline.AC1.7

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/booxpipeline/worker.go:40-49` (add Embedder field to WorkerConfig)
- Modify: `/home/jtd/ultrabridge/internal/booxpipeline/worker.go:141` (call embedder after IndexPage in executeNoteJob)
- Modify: `/home/jtd/ultrabridge/internal/booxpipeline/worker.go:227` (call embedder after IndexPage in executePDFJob)

**Implementation:**

Add `Embedder` field to `WorkerConfig` struct in `worker.go`:

```go
type WorkerConfig struct {
	Indexer        processor.Indexer
	ContentDeleter ContentDeleter
	OCR            OCRer
	OCRPrompt      func() string
	TodoEnabled    func() bool
	TodoPrompt     func() string
	OnTodosFound   func(ctx context.Context, notePath string, todos []TodoItem)
	CachePath      string
	Embedder       rag.Embedder // nil = embedding disabled
	EmbedModel     string       // model name for note_embeddings.model column
	EmbedStore     *rag.Store   // nil = embedding disabled
}
```

After each `IndexPage` call, add the embedding step. In `executeNoteJob()` after line 141 (the IndexPage call for .note files):

```go
if err := p.cfg.Indexer.IndexPage(ctx, notePath, i, "api", ocrText, titleText, keywords); err != nil {
    return fmt.Errorf("index page %d: %w", i, err)
}
// Embed page text if embedder is available
if p.cfg.Embedder != nil && p.cfg.EmbedStore != nil && ocrText != "" {
    vec, err := p.cfg.Embedder.Embed(ctx, ocrText)
    if err != nil {
        p.logger.Warn("embedding failed", "path", notePath, "page", i, "err", err)
    } else if err := p.cfg.EmbedStore.Save(ctx, notePath, i, vec, p.cfg.EmbedModel); err != nil {
        p.logger.Warn("save embedding failed", "path", notePath, "page", i, "err", err)
    }
}
```

Apply the same pattern in `executePDFJob()` after line 227 (the IndexPage call for .pdf files).

Note the nil-safety pattern: check both `Embedder` and `EmbedStore` for nil. Embedding errors are logged as warnings and do NOT fail the job (AC1.7 graceful degradation). Empty text is skipped (no point embedding empty string).

The import for `rag` package: add `"github.com/sysop/ultrabridge/internal/rag"` to the imports.

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.2: Process a .note file with a mock embedder, verify `EmbedStore.Save()` was called for each page with the OCR text. Use an in-memory DB and check `SELECT count(*) FROM note_embeddings WHERE note_path = ?` returns expected page count.
- rag-retrieval-pipeline.AC1.7: Process a .note file with an embedder that returns an error. Verify the job still completes successfully (no error returned from `executeNoteJob`). Verify no embedding row was created.

Follow existing test patterns in `/home/jtd/ultrabridge/internal/booxpipeline/processor_test.go` — uses mock OCR server, `testutil.BuildTestNoteFile`, and in-memory DB.

For the mock embedder, create a simple struct:
```go
type mockEmbedder struct {
    calls []string // track what text was embedded
    err   error    // if set, Embed returns this error
}
func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    if m.err != nil { return nil, m.err }
    m.calls = append(m.calls, text)
    return make([]float32, 768), nil // return zero vector
}
```

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/booxpipeline/ -run TestEmbed -v
```

Expected: All tests pass.

**Commit:** `feat(boox): wire embedder into Boox worker pipeline`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Wire embedder into Supernote worker

**Verifies:** rag-retrieval-pipeline.AC1.3, rag-retrieval-pipeline.AC1.7

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/processor/processor.go:32-40` (add Embedder fields to WorkerConfig)
- Modify: `/home/jtd/ultrabridge/internal/processor/worker.go:205-209` (call embedder after the "api" IndexPage call)

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/processor/CLAUDE.md` for processor conventions.

**Implementation:**

Add fields to `WorkerConfig` in `processor.go`:

```go
type WorkerConfig struct {
	OCREnabled     bool
	BackupPath     string
	MaxFileMB      int
	OCRClient      *OCRClient
	OCRPrompt      func() string
	Indexer        Indexer
	CatalogUpdater CatalogUpdater
	Embedder       rag.Embedder // nil = embedding disabled
	EmbedModel     string       // model name for note_embeddings.model column
	EmbedStore     *rag.Store   // nil = embedding disabled
}
```

In `worker.go`, after the "api" IndexPage call (line 205-209), add embedding:

```go
if s.cfg.Indexer != nil {
    if err := s.cfg.Indexer.IndexPage(ctx, job.NotePath, pageIdx, "api", text, "", ""); err != nil {
        s.logger.Error("failed to index page", "path", job.NotePath, "page", pageIdx, "err", err)
    }
}
// Embed the OCR'd text
if s.cfg.Embedder != nil && s.cfg.EmbedStore != nil && text != "" {
    vec, err := s.cfg.Embedder.Embed(ctx, text)
    if err != nil {
        s.logger.Warn("embedding failed", "path", job.NotePath, "page", pageIdx, "err", err)
    } else if err := s.cfg.EmbedStore.Save(ctx, job.NotePath, pageIdx, vec, s.cfg.EmbedModel); err != nil {
        s.logger.Warn("save embedding failed", "path", job.NotePath, "page", pageIdx, "err", err)
    }
}
```

Note: Only embed after the "api" (OCR) IndexPage call, NOT after the "myScript" (existing RECOGNTEXT) call at line 119. Rationale: the OCR text is higher quality and overwrites the myScript text in the search index anyway. The backfill (Task 8) handles pages that only have myScript text.

Add the import: `"github.com/sysop/ultrabridge/internal/rag"`

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.3: Process a Supernote .note file (with mock OCR client and mock embedder), verify embeddings are created for pages that went through OCR.
- rag-retrieval-pipeline.AC1.7: Process with an embedder that returns errors, verify the job completes successfully and OCR text is still indexed.

Follow existing test patterns in `/home/jtd/ultrabridge/internal/processor/worker_test.go`.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/processor/ -run TestEmbed -v
```

Expected: All tests pass.

**Commit:** `feat(processor): wire embedder into Supernote worker pipeline`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_C -->

<!-- START_SUBCOMPONENT_D (tasks 7-10) -->
## Subcomponent D: Backfill, Manual Trigger, and Main Wiring

<!-- START_TASK_7 -->
### Task 7: Add backfill logic to embedder package

**Verifies:** rag-retrieval-pipeline.AC1.4

**Files:**
- Create: `/home/jtd/ultrabridge/internal/rag/backfill.go`

**Implementation:**

Create `internal/rag/backfill.go` with a `Backfill` function that processes all unembedded pages:

```go
package rag

import (
	"context"
	"log/slog"
)

// Backfill embeds all pages in note_content that don't have a corresponding
// note_embeddings row. Returns the number of pages embedded.
func Backfill(ctx context.Context, store *Store, embedder Embedder, model string, logger *slog.Logger) (int, error) {
	pages, err := store.UnembeddedPages(ctx)
	if err != nil {
		return 0, err
	}

	if len(pages) == 0 {
		return 0, nil
	}

	logger.Info("starting embedding backfill", "pages", len(pages))

	embedded := 0
	for _, p := range pages {
		if ctx.Err() != nil {
			return embedded, ctx.Err()
		}

		vec, err := embedder.Embed(ctx, p.BodyText)
		if err != nil {
			logger.Warn("backfill embed failed", "path", p.NotePath, "page", p.Page, "err", err)
			continue
		}
		if err := store.Save(ctx, p.NotePath, p.Page, vec, model); err != nil {
			logger.Warn("backfill save failed", "path", p.NotePath, "page", p.Page, "err", err)
			continue
		}
		embedded++
	}

	logger.Info("embedding backfill complete", "embedded", embedded, "total", len(pages))
	return embedded, nil
}
```

Key design decisions:
- Continues on individual page failures (logs warning, moves to next)
- Respects context cancellation (allows graceful shutdown during backfill)
- Returns count for logging/monitoring
- Reuses `store.UnembeddedPages()` query from Task 4

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.4: Insert several note_content rows without embeddings. Call `Backfill()`. Verify note_embeddings rows now exist for each page. Verify `UnembeddedPages()` returns empty slice after backfill.
- Partial failure: Set mock embedder to fail on specific pages. Verify other pages are still embedded. Verify failed pages remain in `UnembeddedPages()`.
- Context cancellation: Cancel context mid-backfill. Verify some pages embedded, function returns without error.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/rag/ -run TestBackfill -v
```

Expected: All tests pass.

**Commit:** `feat(rag): add embedding backfill logic`
<!-- END_TASK_7 -->

<!-- START_TASK_8 -->
### Task 8: Add manual backfill trigger endpoint

**Verifies:** rag-retrieval-pipeline.AC1.5

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add embedder/store fields, backfill route)

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/web/CLAUDE.md` if it exists for handler conventions.

**Implementation:**

Add fields to the Handler struct for embedding support:

```go
// In Handler struct
embedder    rag.Embedder
embedStore  *rag.Store
embedModel  string
```

Add these as parameters to `NewHandler()`. They are nil-safe — if nil, the backfill endpoint is not registered or returns 404.

Add a new route in `NewHandler()`:

```go
if h.embedder != nil && h.embedStore != nil {
    mux.HandleFunc("POST /settings/backfill-embeddings", h.handleBackfillEmbeddings)
}
```

Implement the handler:

```go
func (h *Handler) handleBackfillEmbeddings(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx := context.Background() // independent of request lifecycle
		n, err := rag.Backfill(ctx, h.embedStore, h.embedder, h.embedModel, h.logger)
		if err != nil {
			h.logger.Error("backfill failed", "err", err)
			return
		}
		h.logger.Info("backfill triggered via settings", "embedded", n)
	}()

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
```

The backfill runs in a background goroutine so the HTTP response returns immediately. Progress is visible via log entries.

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC1.5: POST to `/settings/backfill-embeddings` returns 303 redirect. (The actual backfill runs async, so the test verifies the endpoint exists and responds correctly.)
- When embedder is nil (embedding disabled), the route is not registered (POST returns 404).

Follow handler test patterns in `/home/jtd/ultrabridge/internal/web/handler_test.go`.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/web/ -run TestBackfill -v
```

Expected: All tests pass.

**Commit:** `feat(web): add manual embedding backfill trigger endpoint`
<!-- END_TASK_8 -->

<!-- START_TASK_9 -->
### Task 9: Wire everything in main.go

**Verifies:** rag-retrieval-pipeline.AC1.4, rag-retrieval-pipeline.AC1.6

**Files:**
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go:161-237` (add embedder initialization and wiring)

**Implementation:**

After the `si := search.New(noteDB)` line (line 163) and before the Supernote WorkerConfig (line 164), add embedding initialization:

```go
// Initialize embedding pipeline if enabled
var embedder *rag.OllamaEmbedder
var embedStore *rag.Store
if cfg.EmbedEnabled {
    embedder = rag.NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaEmbedModel, logger)
    embedStore = rag.NewStore(noteDB, logger)

    // Load existing embeddings into memory (AC1.6)
    n, err := embedStore.LoadAll(context.Background())
    if err != nil {
        logger.Warn("failed to load embeddings into cache", "err", err)
    } else {
        logger.Info("loaded embeddings into memory", "count", n)
    }

    // Startup backfill (AC1.4) — runs in background
    go func() {
        n, err := rag.Backfill(context.Background(), embedStore, embedder, cfg.OllamaEmbedModel, logger)
        if err != nil {
            logger.Warn("startup backfill failed", "err", err)
        } else if n > 0 {
            logger.Info("startup backfill complete", "embedded", n)
        }
    }()
}
```

Wire into Supernote WorkerConfig (existing code at line 164-173):

```go
workerCfg := processor.WorkerConfig{
    OCREnabled: cfg.OCREnabled,
    BackupPath: cfg.BackupPath,
    MaxFileMB:  cfg.OCRMaxFileMB,
    Indexer:    si,
    Embedder:   embedder,   // new
    EmbedModel: cfg.OllamaEmbedModel, // new
    EmbedStore: embedStore,  // new
    OCRPrompt: func() string {
        v, _ := notedb.GetSetting(context.Background(), noteDB, "sn_ocr_prompt")
        return v
    },
}
```

Wire into Boox WorkerConfig (existing code at line 201-223):

```go
booxCfg := booxpipeline.WorkerConfig{
    Indexer:        si,
    ContentDeleter: si,
    CachePath:      filepath.Join(cfg.BooxNotesPath, ".cache"),
    Embedder:       embedder,   // new
    EmbedModel:     cfg.OllamaEmbedModel, // new
    EmbedStore:     embedStore,  // new
    // ... (keep existing OCRPrompt, TodoEnabled, etc.)
}
```

Wire into web.NewHandler (existing code at line 310):

Add `embedder`, `embedStore`, `cfg.OllamaEmbedModel` to the `NewHandler` call. The exact position depends on the current signature — append after `broadcaster`.

Add the import: `"github.com/sysop/ultrabridge/internal/rag"`

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go vet -C /home/jtd/ultrabridge ./...
```

Expected: Both succeed with no errors.

**Commit:** `feat: wire embedding pipeline into main`
<!-- END_TASK_9 -->

<!-- START_TASK_10 -->
### Task 10: Final build and full test suite verification

**Verifies:** None (verification checkpoint)

**Files:** None (verification only)

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: All commands succeed. All existing tests still pass (no regressions). New tests in `internal/rag/`, `internal/notedb/`, `internal/booxpipeline/`, `internal/processor/`, and `internal/web/` all pass.

**Commit:** No commit needed — this is a verification step only.
<!-- END_TASK_10 -->
<!-- END_SUBCOMPONENT_D -->
