# Boox Notes Pipeline — Phase 6: Unified Search

**Goal:** Search results include both Supernote and Boox notes with source indicators.

**Architecture:** Minimal changes — the FTS5 index already contains both Supernote and Boox content via the shared `Indexer` interface. The only changes needed are: (1) add `Source` field to `SearchResult` struct, (2) select the `source` column in the search query, (3) add source badge to the search results template. BM25 scoring is unaffected by source — ranking is consistent across sources by design.

**Tech Stack:** SQLite FTS5 (existing), Go `html/template` (existing).

**Scope:** 7 phases from original design (phase 6 of 7)

**Codebase verified:** 2026-04-05

**Reference files:**
- SearchResult struct: `/home/jtd/ultrabridge/internal/search/model.go`
- Search function: `/home/jtd/ultrabridge/internal/search/index.go:65-103`
- Search handler: `/home/jtd/ultrabridge/internal/web/handler.go:407-430`
- Search template: `/home/jtd/ultrabridge/internal/web/templates/index.html:418-441`
- Source badge pattern: `/home/jtd/ultrabridge/internal/web/templates/index.html:708`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC6: Unified search across note sources
- **boox-notes-pipeline.AC6.1 Success:** Search query returns results from both Supernote and Boox notes
- **boox-notes-pipeline.AC6.2 Success:** Each search result shows correct source badge (Boox or Supernote)
- **boox-notes-pipeline.AC6.3 Success:** Search ranking is consistent across sources (BM25 scoring unaffected by source)

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Verify FTS5 index returns results from both sources (no code changes)

**Verifies:** boox-notes-pipeline.AC6.1, boox-notes-pipeline.AC6.3

**Files:** None — no changes to `internal/search/`. Per design: "No changes to `internal/search/` — FTS5 index already contains both sources via shared `Indexer`."

**Implementation:**

No code changes needed for this task. The shared `Indexer.IndexPage()` already writes Boox OCR results into `note_content` (done in Phase 4), and the FTS5 triggers automatically sync to `note_fts`. The existing `Search()` function returns results from both sources without modification. BM25 scoring is path-agnostic, so ranking is consistent across sources (AC6.3).

Device source detection (Boox vs Supernote) is handled in the web layer via path prefix comparison (see Task 2), not via the `source` column in `note_content` (which tracks OCR provenance — "myScript"/"api" — not device type).

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/search/ -v
```

Expected: Existing tests still pass (no changes made).

**Commit:** No commit needed — this task verifies existing behavior.
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Add source badge to search results template

**Verifies:** boox-notes-pipeline.AC6.2

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add source detection helper)
- Modify: `/home/jtd/ultrabridge/internal/web/templates/index.html` (source badge in search results)

**Implementation:**

The `noteSource` template function was already registered in Phase 5 Task 4. It determines device source from note path prefix (`booxNotesPath`). No additional handler changes needed here.

In `/home/jtd/ultrabridge/internal/web/templates/index.html`, update the search results table (around line 425) to add a source badge column:

```html
<table class="task-table">
  <thead><tr><th>Source</th><th>File</th><th>Page</th><th>Match</th></tr></thead>
  <tbody>
    {{range .searchResults}}
    <tr>
      <td>
        {{$src := noteSource .Path}}
        {{if eq $src "Boox"}}
          <span class="badge badge-boox">B</span>
        {{else}}
          <span class="badge badge-sn">SN</span>
        {{end}}
      </td>
      <td><a href="/files?path={{.Path}}">{{.Path}}</a></td>
      <td>{{.Page}}</td>
      <td>{{.Snippet}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
```

Add CSS for the Supernote badge (Boox badge CSS was added in Phase 5):

```css
.badge-sn {
    display: inline-block;
    padding: 1px 5px;
    font-size: 10px;
    font-weight: bold;
    color: #fff;
    background-color: #3b82f6;
    border-radius: 3px;
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors.

**Commit:** `feat(web): add source badges to unified search results`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-3) -->
<!-- START_TASK_3 -->
### Task 3: Tests for unified search — all AC6 criteria

**Verifies:** boox-notes-pipeline.AC6.1, boox-notes-pipeline.AC6.2, boox-notes-pipeline.AC6.3

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/search/index_test.go` (add Source field test)
- Create: `/home/jtd/ultrabridge/internal/web/search_test.go` (HTTP-level search test)

**Testing:**

`index_test.go` — extend existing search tests:

- **boox-notes-pipeline.AC6.1:** `TestSearch_ReturnsMultipleSources` — index two pages: one with a Supernote path prefix and source="myScript", one with a Boox path prefix and source="api". Search for a term present in both. Verify both results returned.

- **boox-notes-pipeline.AC6.3:** `TestSearch_BM25ConsistentAcrossSources` — index two identical text bodies with different paths (Supernote vs Boox). Search for the same term. Verify both results have the same BM25 score (ranking unaffected by source).

`search_test.go` — HTTP-level test:

- **boox-notes-pipeline.AC6.2:** `TestSearchPage_SourceBadges` — set up handler with in-memory DB containing indexed content from both Supernote and Boox paths. GET /search?q=..., verify response HTML contains both `badge-sn` and `badge-boox` CSS classes.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/search/ ./internal/web/ -v -run TestSearch
```

Expected: All tests pass.

**Commit:** `test(search): add unified search tests covering AC6.1-AC6.3`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_B -->
