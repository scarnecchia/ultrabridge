# RAG Retrieval Pipeline — Phase 6: Deployment and Polish

**Goal:** Add install.sh prompts for new config fields, Settings UI status displays, error state handling, and deployment polish.

**Architecture:** Extends existing install.sh prompt flow, Settings UI card pattern, and error handling patterns. No new system dependencies (Ollama runs on host, vLLM runs separately, both accessed via HTTP).

**Tech Stack:** Bash (install.sh), Go `html/template` (settings UI), existing deployment patterns

**Scope:** 6 phases from original design (phase 6 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC6: Configuration and Deployment
- **rag-retrieval-pipeline.AC6.1 Success:** New config fields: `UB_OLLAMA_URL` (Ollama base URL), `UB_OLLAMA_EMBED_MODEL` (embedding model name), `UB_CHAT_API_URL` (vLLM URL), `UB_CHAT_MODEL` (generation model name), `UB_CHAT_ENABLED` (feature flag), `UB_EMBED_ENABLED` (feature flag). Verified by: config.go loads all fields with sensible defaults.
- **rag-retrieval-pipeline.AC6.2 Success:** `install.sh` prompts for Ollama URL, chat API URL, and model names. Verified by: running install.sh shows new prompts in correct section.
- **rag-retrieval-pipeline.AC6.3 Success:** Embedding pipeline is disabled when `UB_EMBED_ENABLED=false` (default). Workers skip embedding step. Verified by: default config does not attempt Ollama connection.
- **rag-retrieval-pipeline.AC6.4 Success:** Chat tab is hidden when `UB_CHAT_ENABLED=false` (default). Verified by: default config shows no Chat tab in UI.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
## Subcomponent A: install.sh Configuration Prompts

<!-- START_TASK_1 -->
### Task 1: Add RAG pipeline prompts to install.sh

**Verifies:** rag-retrieval-pipeline.AC6.2

**Files:**
- Modify: `/home/jtd/ultrabridge/install.sh:368` (add new section after Boox integration)
- Modify: `/home/jtd/ultrabridge/install.sh:403-464` (add new vars to `.ultrabridge.env` output)

**Implementation:**

Add a new configuration section after the Boox Device Integration section (after line 368). Follow the existing conditional prompt pattern:

```bash
# ── RAG Pipeline (optional) ──
info ""
info "── RAG Pipeline ──"
info "Embedding pipeline generates search vectors via Ollama."
info "Chat tab uses vLLM for local text generation."
info ""

prompt UB_EMBED_ENABLED "Enable embedding pipeline? (true/false)" "${UB_EMBED_ENABLED:-false}"
if [[ "$UB_EMBED_ENABLED" == "true" ]]; then
    prompt UB_OLLAMA_URL "Ollama API URL" "${UB_OLLAMA_URL:-http://localhost:11434}"
    prompt UB_OLLAMA_EMBED_MODEL "Embedding model" "${UB_OLLAMA_EMBED_MODEL:-nomic-embed-text:v1.5}"
fi

prompt UB_CHAT_ENABLED "Enable chat tab? (true/false)" "${UB_CHAT_ENABLED:-false}"
if [[ "$UB_CHAT_ENABLED" == "true" ]]; then
    prompt UB_CHAT_API_URL "vLLM API URL" "${UB_CHAT_API_URL:-http://localhost:8000}"
    prompt UB_CHAT_MODEL "Chat model name" "${UB_CHAT_MODEL:-Qwen/Qwen3-8B}"
fi
```

Add the new variables to the `.ultrabridge.env` output section (around lines 443-464):

```bash
# RAG Pipeline
echo "UB_EMBED_ENABLED=${UB_EMBED_ENABLED}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
if [[ "$UB_EMBED_ENABLED" == "true" ]]; then
    echo "UB_OLLAMA_URL=${UB_OLLAMA_URL}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
    echo "UB_OLLAMA_EMBED_MODEL=${UB_OLLAMA_EMBED_MODEL}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
fi
echo "UB_CHAT_ENABLED=${UB_CHAT_ENABLED}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
if [[ "$UB_CHAT_ENABLED" == "true" ]]; then
    echo "UB_CHAT_API_URL=${UB_CHAT_API_URL}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
    echo "UB_CHAT_MODEL=${UB_CHAT_MODEL}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
fi
```

For unattended mode (`--unattended` / `-y`), the values are read from existing environment variables with the defaults shown above.

**Verification:**

Run install.sh in a test context (or manually verify the prompts appear in the correct section).

```bash
# Quick syntax check
bash -n /home/jtd/ultrabridge/install.sh
```

**Commit:** `feat(deploy): add RAG pipeline prompts to install.sh`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Verify config loading defaults

**Verifies:** rag-retrieval-pipeline.AC6.1, rag-retrieval-pipeline.AC6.3, rag-retrieval-pipeline.AC6.4

**Files:**
- None (verification of Phase 1 config — already implemented)

**Implementation:**

Verify that the config fields added in Phase 1 (Task 1) work correctly:
- `UB_EMBED_ENABLED` defaults to `false`
- `UB_OLLAMA_URL` defaults to `http://localhost:11434`
- `UB_OLLAMA_EMBED_MODEL` defaults to `nomic-embed-text:v1.5`
- `UB_CHAT_ENABLED` defaults to `false`
- `UB_CHAT_API_URL` defaults to `http://localhost:8000`
- `UB_CHAT_MODEL` defaults to `Qwen/Qwen3-8B`

This is a verification step for AC6.1. The config fields were implemented in Phase 1, but the install.sh integration is new in this phase.

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
```

**Commit:** No commit — verification only.
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
## Subcomponent B: Settings UI Polish

<!-- START_TASK_3 -->
### Task 3: Add RAG/Embedding settings card to Settings UI

**Verifies:** rag-retrieval-pipeline.AC6.3, rag-retrieval-pipeline.AC6.4

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/templates/index.html:665` (add RAG settings card after Boox card)
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add template data for RAG status)

**Implementation:**

Add a new settings card after the Boox card in `index.html`. Follow the existing card pattern with conditional `settings-inactive` class:

```html
<!-- RAG Pipeline Card -->
<div class="card{{if not .EmbedEnabled}} settings-inactive{{end}}">
    <h2>RAG Search</h2>
    {{if not .EmbedEnabled}}
        <p class="settings-inactive-note">Embedding pipeline not configured. Set UB_EMBED_ENABLED=true to enable.</p>
    {{else}}
        <div style="margin-bottom:0.75rem;">
            <label style="font-weight:bold;">Embedding Status</label>
            <p style="padding:0.4rem; background:#f8f8f8; border-radius:3px;">
                {{.EmbeddingCount}} embeddings loaded
            </p>
        </div>
        <div style="margin-bottom:0.75rem;">
            <label style="font-weight:bold;">Ollama</label>
            <p style="padding:0.4rem; background:#f8f8f8; border-radius:3px;">
                {{.OllamaURL}} ({{.OllamaModel}})
            </p>
        </div>
        <hr style="margin:1rem 0; border:none; border-top:1px solid #e5e7eb;">
        <form method="POST" action="/settings/backfill-embeddings" style="margin:0;">
            <button type="submit" onclick="return confirm('Recompute embeddings for all indexed notes? This may take several minutes.')">
                ⟳ Backfill Embeddings
            </button>
        </form>
    {{end}}
</div>

{{if .ChatEnabled}}
<div class="card">
    <h2>Chat</h2>
    <div style="margin-bottom:0.75rem;">
        <label style="font-weight:bold;">Model</label>
        <p style="padding:0.4rem; background:#f8f8f8; border-radius:3px;">
            {{.ChatModel}} @ {{.ChatAPIURL}}
        </p>
    </div>
</div>
{{end}}
```

In the handler, populate template data in `handleSettings()`:

```go
data["EmbedEnabled"] = h.embedder != nil
if h.embedStore != nil {
    data["EmbeddingCount"] = len(h.embedStore.AllEmbeddings())
}
data["OllamaURL"] = /* from config or handler field */
data["OllamaModel"] = /* from config or handler field */
data["ChatEnabled"] = h.chatHandler != nil
data["ChatModel"] = /* from config or handler field */
data["ChatAPIURL"] = /* from config or handler field */
```

Add string fields to the Handler struct for display purposes: `ollamaURL`, `ollamaModel`, `chatAPIURL`, `chatModel`. Populate from config in `NewHandler`.

**Testing:**

Tests must verify:
- rag-retrieval-pipeline.AC6.3: When embedder is nil (disabled), settings page shows "not configured" message
- rag-retrieval-pipeline.AC6.4: When chatHandler is nil (disabled), Chat card is not shown
- When both are enabled, status info and backfill button are visible

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./internal/web/ -run TestSettings -v
```

**Commit:** `feat(web): add RAG and Chat settings cards to Settings UI`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Final build, test, and vet

**Verifies:** None (final verification checkpoint)

**Files:** None

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
bash -n /home/jtd/ultrabridge/install.sh
```

Expected: All commands succeed. All tests pass. install.sh has valid syntax.

**Commit:** No commit — verification only.
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->
