# Note Reprocessing Implementation Plan - Phase 3

**Goal:** Replace `extractNotePaths` stub with a real parser for SPC Engine.IO FILE-SYN/DOWNLOADFILE events.

**Architecture:** Pure function `extractNotePaths(msg []byte, notesPath string) []string` parses Socket.IO `42["ServerMessage","<JSON>"]` frames, extracts file paths from data entries, filters for .note files, and resolves to absolute paths. The `runEngineIOListener` passes `p.notesPath` to the parser.

**Tech Stack:** Go, encoding/json, path/filepath, standard library testing

**Scope:** 3 phases from original design (phase 3 of 3)

**Codebase verified:** 2026-03-22

**Key files:**
- `internal/pipeline/CLAUDE.md` — Pipeline domain contracts
- `internal/sync/CLAUDE.md` — Sync notifier contracts (Engine.IO frame format, Events() channel)
- `internal/sync/notifier_test.go:260-314` — Frame format reference: `42["ServerMessage","<escaped JSON>"]`, second element is JSON string
- `internal/sync/notifier.go:144-151` — STARTSYNC construction shows canonical frame format

---

## Acceptance Criteria Coverage

This phase implements and tests:

### note-reprocessing.AC4: Engine.IO FILE-SYN parser
- **note-reprocessing.AC4.1 Success:** Valid FILE-SYN/DOWNLOADFILE frame with .note file -> returns resolved absolute path
- **note-reprocessing.AC4.2 Success:** Multiple entries in data array -> returns all matching .note paths
- **note-reprocessing.AC4.3 Failure:** Non-FILE-SYN msgType -> returns nil
- **note-reprocessing.AC4.4 Failure:** Non-.note file in DOWNLOADFILE -> filtered out
- **note-reprocessing.AC4.5 Failure:** Malformed frame (bad JSON, truncated, missing fields) -> returns nil, no panic

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
### Task 1: Implement extractNotePaths parser

**Verifies:** note-reprocessing.AC4.1, note-reprocessing.AC4.2, note-reprocessing.AC4.3, note-reprocessing.AC4.4, note-reprocessing.AC4.5

**Files:**
- Modify: `internal/pipeline/engineio.go` (replace entire file — stub becomes real implementation)
- Test: `internal/pipeline/engineio_test.go` (new file — pure function tests)

**Implementation:**

Replace the contents of `internal/pipeline/engineio.go` with the parser. The key structures:

**Frame format** (confirmed from `internal/sync/notifier.go:144-151` and `notifier_test.go:260-314`):
```
42["ServerMessage","{\"code\":\"200\",\"timestamp\":1710915609123,\"msgType\":\"FILE-SYN\",\"data\":[{\"name\":\"MyNote\",\"path\":\"Note/MyNote.note\",\"md5\":\"...\",\"size\":12345}]}"]
```

The raw bytes on the events channel include the `42` prefix. The second JSON array element is a **JSON-encoded string** containing the payload (not a raw object).

**Parsing steps:**
1. Verify `msg` starts with `42` (Socket.IO message prefix)
2. Strip the `42` prefix
3. Unmarshal as `[]json.RawMessage` — expect exactly 2 elements
4. Unmarshal first element as string — verify it's `"ServerMessage"`
5. Unmarshal second element as string (the stringified payload JSON)
6. Unmarshal the payload string into a struct with `msgType` and `data` fields
7. Check `msgType` is `"FILE-SYN"` or `"DOWNLOADFILE"` — return nil otherwise
8. Iterate `data` entries, extract `path` field
9. Filter: only `.note` files (using `notestore.ClassifyFileType`)
10. Resolve to absolute path: `filepath.Join(notesPath, path)`
11. Return collected paths

**Signature change:** `extractNotePaths(msg []byte, notesPath string) []string`

**Update `runEngineIOListener`** to pass `p.notesPath`:
```go
for _, path := range extractNotePaths(msg, p.notesPath) {
```

The function must never panic on malformed input — all JSON errors return nil.

Internal types for unmarshaling (unexported):

```go
type serverPayload struct {
	MsgType string       `json:"msgType"`
	Data    []fileEntry  `json:"data"`
}

type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}
```

**Testing:**

Tests must verify each AC listed above. Use table-driven tests with the following cases:

- note-reprocessing.AC4.1: Construct a valid `42["ServerMessage","..."]` frame with msgType=FILE-SYN, one .note file in data. Verify returns `[filepath.Join(notesPath, "Note/MyNote.note")]`.
- note-reprocessing.AC4.1 (DOWNLOADFILE variant): Same with msgType=DOWNLOADFILE. Verify returns path.
- note-reprocessing.AC4.2: Frame with 3 entries in data (2 .note, 1 .pdf). Verify returns 2 paths.
- note-reprocessing.AC4.3: Frame with msgType="SOME-OTHER-TYPE". Verify returns nil.
- note-reprocessing.AC4.3: Frame with msgType="STARTSYNC" (STARTSYNC events should not enqueue files). Verify returns nil.
- note-reprocessing.AC4.4: Frame with only non-.note files (.pdf, .epub). Verify returns nil.
- note-reprocessing.AC4.5: Empty input `[]byte{}`. Verify returns nil.
- note-reprocessing.AC4.5: Input without `42` prefix (e.g., `3probe`). Verify returns nil.
- note-reprocessing.AC4.5: Truncated JSON `42["ServerMessage"`. Verify returns nil.
- note-reprocessing.AC4.5: Valid outer array but inner payload is not valid JSON. Verify returns nil.
- note-reprocessing.AC4.5: Valid JSON but missing data field. Verify returns nil.

Helper function to build test frames:
```go
func buildFrame(t *testing.T, msgType string, entries []fileEntry) []byte {
    t.Helper()
    payload := serverPayload{MsgType: msgType, Data: entries}
    payloadJSON, _ := json.Marshal(payload)
    payloadStr, _ := json.Marshal(string(payloadJSON))
    return []byte(fmt.Sprintf(`42["ServerMessage",%s]`, payloadStr))
}
```

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/pipeline/ -run TestExtractNotePaths -v
```

Expected: All table-driven test cases pass.

Also update `runEngineIOListener` (line 22) to pass `p.notesPath`:
```go
// Before:
for _, path := range extractNotePaths(msg) {
// After:
for _, path := range extractNotePaths(msg, p.notesPath) {
```

After implementation, verify the build compiles:
```bash
go build -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
```

**Commit:** `feat(pipeline): implement Engine.IO FILE-SYN/DOWNLOADFILE parser`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Run full test suite

**Verifies:** None (regression verification)

**Files:** None (no code changes)

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
go vet -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
```

Expected: All packages pass. No vet warnings.

**Commit:** No commit needed — verification only.

<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->
