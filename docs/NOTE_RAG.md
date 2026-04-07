# Note RAG: Retrieval-Augmented Generation over Handwritten Notes

Brainstorming document for building insight generation from the collected
OCR'd note content in UltraBridge's SQLite database.

## Goal

Let users ask natural language questions about their notes and get
synthesized answers grounded in actual note content. Examples:

- "What were the key decisions from my steering committee meetings?"
- "Summarize what happened with the CCC project in Q4 2025"
- "What open items did I discuss with Deepak in the last 3 months?"
- "Find all mentions of budget concerns across my meeting notes"

## What We Have

### Data Layer (already built)
- `note_content` table: per-page OCR text with `note_path`, `page`, `body_text`, `title_text`, `keywords`, `source`
- FTS5 index (`note_fts`): keyword search with BM25 scoring and snippet extraction
- `boox_notes` / `notes` tables: metadata (device model, folder, timestamps, page count)
- Unified across Supernote and Boox pipelines
- ~12,000+ pages of handwritten meeting notes, personal notes, etc.

### Infrastructure (already running)
- Qwen3-VL-8B on RTX 5060 Ti (16GB) — local, no API costs, ~10s/page for OCR
- SQLite with WAL mode, single-writer
- Web UI with tabs, Settings, SSE log streaming

## Architecture Options

### Option A: Hybrid FTS5 + LLM (No Embeddings)

Leverage existing FTS5 index for retrieval, LLM for query understanding and synthesis.

```
User Question
    |
    v
[Query Expansion LLM Call]
    "What decisions were made in steering committee?"
    -> generates: ["steering committee decision", "HPC steering action items",
                   "steering committee resolution", "committee approved"]
    |
    v
[FTS5 Search] x N queries
    -> top-K pages per query, deduplicated, ranked by BM25
    |
    v
[Context Assembly]
    - Page text + metadata (date, folder, device, title)
    - Sorted by date or relevance
    - Truncated to fit context window
    |
    v
[Synthesis LLM Call]
    "Given these note excerpts, answer the user's question..."
    -> Grounded answer with citations (note title, date, page)
```

**Pros:**
- No new infrastructure (FTS5 already exists)
- No embedding model needed
- Works today with minimal code
- Keyword search is actually decent for meeting notes (people write topic names)

**Cons:**
- Misses semantic matches ("budget concerns" won't find "cost overruns")
- OCR noise reduces keyword recall
- Query expansion adds an extra LLM round-trip

### Option B: Vector Embeddings + FTS5 Hybrid

Add semantic search alongside keyword search.

```
[Indexing Pipeline]
    On OCR completion:
        body_text -> embedding model -> vector stored in DB

[Query Time]
    User question -> embedding -> vector similarity search
                  -> FTS5 keyword search (parallel)
                  -> merge & re-rank results
                  -> synthesis LLM call
```

**Embedding model options:**
- Local: `nomic-embed-text` or `bge-small-en` via Ollama/vLLM (~100M params, runs on CPU)
- API: OpenAI `text-embedding-3-small` (cheap but external)

**Vector storage options:**
- `sqlite-vec` (pure C, single-file, integrates with existing DB)
  - Caveat: need to verify compatibility with modernc.org/sqlite (pure-Go)
  - May need CGO or a separate process
- Separate SQLite DB with a Go vector search library
- Qdrant/ChromaDB (separate service, more ops burden)

**Pros:**
- Semantic matching ("budget" finds "cost", "funding", "expenditure")
- More tolerant of OCR noise
- Industry-standard RAG approach

**Cons:**
- Embedding model infrastructure (even small ones need hosting)
- Reindex all existing content
- More complex query pipeline
- Diminishing returns for meeting notes (which tend to use literal topic names)

### Option C: Full-Context Approach (No Retrieval)

For a small enough corpus, skip retrieval entirely — stuff everything into a
large-context LLM.

At ~200 tokens/page average (handwritten notes are sparse), 12,000 pages is
~2.4M tokens. That's within Gemini 2.5 Pro's context window (1M+) if you
summarize first, or could work with a two-stage approach:

1. Pre-compute per-note summaries (stored in DB)
2. Load all summaries (~50 tokens each = 600K tokens) into context
3. LLM identifies relevant notes, then load full pages for those

**Pros:**
- No retrieval pipeline to build or tune
- LLM sees everything, no recall issues
- Simplest code

**Cons:**
- Requires large-context model (not local Qwen)
- API costs per query
- Slow (processing millions of tokens per question)
- Not practical for real-time chat

## Recommended Approach: Start with A, Add B Later

### Phase 1: Hybrid FTS5 + LLM (Option A)

Minimal new code. Could be built in a day:

1. New `internal/rag/` package:
   - `QueryExpander` — takes user question, returns N search queries
   - `Retriever` — runs FTS5 queries, deduplicates, ranks, assembles context
   - `Synthesizer` — sends context + question to LLM, returns answer

2. New web routes:
   - `GET /chat` — chat UI tab
   - `POST /chat/ask` — accepts question, returns streamed answer (SSE)
   - `GET /chat/history` — previous Q&A pairs (stored in SQLite)

3. LLM routing:
   - Query expansion: local Qwen (fast, cheap, good enough for keyword generation)
   - Synthesis: configurable — local Qwen for simple queries, or external API
     (Claude, GPT-4) for complex multi-note synthesis
   - Could use same OCR client infrastructure (OpenAI-compatible API)

### Phase 2: Add Embeddings (Option B)

Once Phase 1 is working and we understand retrieval gaps:

1. Add embedding generation to the OCR pipeline (after indexing)
2. Store embeddings alongside note_content
3. Hybrid retrieval: FTS5 + vector similarity, merge results
4. Evaluate: does semantic search actually improve answers?

## Open Questions

- **LLM for synthesis**: Local Qwen3-8B can do simple Q&A but may struggle
  with complex multi-document synthesis. Is an external API acceptable for
  this use case, or must everything stay local?

- **Context window management**: With dense meeting notes, top-20 pages could
  be 4000+ tokens. Local 8B model with 16K context can handle ~12K tokens of
  note content + prompt. Is that enough, or do we need summarization?

- **Chat history**: Should the system remember previous questions in a session
  to enable follow-ups ("tell me more about the second point")?

- **Date awareness**: Many queries will be time-scoped ("last month", "Q4 2025").
  The retriever needs to filter by note date. FTS5 doesn't support date
  filters natively — need to JOIN with boox_notes/notes and filter in SQL.

- **Folder/source scoping**: "What did I write on my Palma about work?"
  needs device + folder filtering. Already have this metadata.

- **Incremental updates**: New notes get indexed automatically. RAG index
  stays current with no extra work (FTS5 triggers handle it). Embeddings
  would need a similar auto-index hook.

## Data Quality Considerations

- OCR of handwriting is inherently noisy. Expect 80-95% accuracy depending
  on handwriting quality and pen type.
- Red ink / light strokes may OCR poorly.
- Abbreviations and shorthand are common in meeting notes — the LLM needs
  to handle "SC" = "Scientific Computing", "RIT" = "Research IT", etc.
- Could add a user-configurable glossary/abbreviation map to the prompt.
- Pages with diagrams/drawings will produce garbage OCR — consider a
  "content type" classifier to skip non-text pages.
