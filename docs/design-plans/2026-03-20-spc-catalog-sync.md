# SPC Catalog Sync Design

## Summary

UltraBridge's OCR pipeline modifies `.note` files server-side by injecting recognized text (RECOGNTEXT) directly into the binary file format. Until now, those changes were invisible to the Supernote device: the Supernote Private Cloud (SPC) MariaDB catalog still referenced the original file's size and MD5 hash, so the device had no reason to re-download the enhanced version. This feature closes that gap by updating the SPC catalog immediately after a successful injection.

The implementation introduces a `CatalogUpdater` interface with a single `AfterInject(ctx, path)` method. After the worker successfully completes an OCR injection job, it calls this method, which re-stats the file, recomputes its MD5, updates the `f_user_file` catalog row, inserts an `f_file_action` audit record, and adjusts `f_capacity` to account for the size change. All three database operations are best-effort: each step runs independently, and any failure is logged but does not mark the job as failed or prevent subsequent steps. The concrete implementation (`spcCatalog`) follows the same optional-interface pattern already used by the search `Indexer` — when the MariaDB connection is present, the updater is wired automatically; no new configuration flag is required.

## Definition of Done

After a successful OCR injection, UltraBridge updates the Supernote Private Cloud MariaDB so the device detects the enhanced file and downloads it on next sync. Specifically:
1. `f_user_file.{size, md5, update_time}` is updated to reflect the new injected file
2. A `f_file_action` row with `action='A'` is inserted to record the server-side modification
3. `f_capacity.used_capacity` is adjusted by the size delta (new_size − old_size)

All three updates are best-effort — failures are logged but do not fail the job. The device then automatically fetches the updated `.note` file (containing injected RECOGNTEXT) on its next sync.

## Acceptance Criteria

### spc-catalog-sync.AC1: f_user_file catalog row updated
- **spc-catalog-sync.AC1.1 Success:** After injection, `f_user_file.size` equals the byte count of the modified file on disk
- **spc-catalog-sync.AC1.2 Success:** After injection, `f_user_file.md5` equals the MD5 hex digest of the modified file
- **spc-catalog-sync.AC1.3 Success:** After injection, `f_user_file.update_time` is updated to the current timestamp
- **spc-catalog-sync.AC1.4 Failure:** If no `f_user_file` row exists for the file's `inner_name`, no update is attempted and the job still completes as done

### spc-catalog-sync.AC2: f_file_action audit row inserted
- **spc-catalog-sync.AC2.1 Success:** A new `f_file_action` row with `action='A'` is inserted, containing the correct `file_id`, `user_id`, `md5`, `size`, `inner_name`, and `file_name`
- **spc-catalog-sync.AC2.2 Success:** The inserted row has a unique `id` and matching `create_time`/`update_time`

### spc-catalog-sync.AC3: f_capacity quota adjusted
- **spc-catalog-sync.AC3.1 Success:** `f_capacity.used_capacity` is updated by `new_size − old_size` (may be positive or negative)
- **spc-catalog-sync.AC3.2 Edge:** If `new_size == old_size`, capacity delta is zero; update proceeds without error

### spc-catalog-sync.AC4: Best-effort — failures do not fail the job
- **spc-catalog-sync.AC4.1 Failure:** If `f_user_file` SELECT fails, remaining steps are skipped; job still completes as done
- **spc-catalog-sync.AC4.2 Failure:** If `f_user_file` UPDATE fails, `f_file_action` INSERT and `f_capacity` UPDATE still execute
- **spc-catalog-sync.AC4.3 Failure:** If `f_file_action` INSERT fails, `f_capacity` UPDATE still executes

### spc-catalog-sync.AC5: Worker calls AfterInject on correct path
- **spc-catalog-sync.AC5.1 Success:** `AfterInject` is called on the success path after `executeJob` returns nil
- **spc-catalog-sync.AC5.2 Failure:** `AfterInject` is not called when `executeJob` returns an error

### spc-catalog-sync.AC6: Nil CatalogUpdater is safe
- **spc-catalog-sync.AC6.1 Edge:** When `CatalogUpdater` is nil in `WorkerConfig`, `processJob` does not panic and behaves identically to before this change

## Glossary

- **SPC (Supernote Private Cloud)**: The self-hosted server software distributed by Ratta that Supernote devices sync files to and from. UltraBridge runs as a sidecar alongside it.
- **RECOGNTEXT**: A binary layer within the `.note` file format that stores handwriting recognition text. UltraBridge injects OCR results here so the device can index and search handwritten notes.
- **OCR injection**: The pipeline step where UltraBridge writes recognized text back into a `.note` file by modifying the binary structure in place (via the `go-sn` library).
- **`f_user_file`**: MariaDB table in SPC that is the authoritative catalog of files owned by a user, including their current size, MD5 hash, and last-modified timestamp. The device uses this to decide what to download.
- **`f_file_action`**: MariaDB table in SPC that records an audit log of file change events. An `action='A'` row signals a server-side file modification.
- **`f_capacity`**: MariaDB table in SPC that tracks per-user storage quota consumption (`used_capacity`).
- **`inner_name`**: A stable internal identifier for a file in the SPC schema, used to correlate a file on disk with its catalog row in `f_user_file`.
- **`CatalogUpdater`**: The Go interface introduced by this design. Decouples the worker from the MariaDB catalog update logic; a nil value disables catalog sync without changing worker behavior.
- **`spcCatalog`**: The concrete implementation of `CatalogUpdater`, defined in `internal/processor/catalog.go`, that performs the actual MariaDB writes.
- **`WorkerConfig`**: The configuration struct passed to the processor `Store` at startup. Holds optional components (OCR client, indexer, catalog updater) that the worker uses if non-nil.
- **`Indexer`**: An existing optional interface in `WorkerConfig` that the worker calls to push recognized text into the FTS5 full-text search index. `CatalogUpdater` mirrors this pattern exactly.
- **best-effort**: A failure-isolation pattern used here where each sub-operation runs independently — a failure in one does not prevent the others from executing, and none can fail the parent job.
- **Snowflake ID**: A 64-bit integer ID format that encodes a timestamp, datacenter, worker, and sequence number. SPC's Java service generates IDs in this format; UltraBridge uses `time.Now().UnixNano()` as a compatible substitute.
- **FTS5**: SQLite's fifth-generation full-text search extension, used by UltraBridge's search subsystem. Referenced here in the context of the `Indexer` pattern that `CatalogUpdater` mirrors.
- **`go-sn`**: The Go library used by UltraBridge to parse, render, and inject content into the Supernote `.note` binary format.

## Architecture

When `processJob` completes successfully, the worker calls `CatalogUpdater.AfterInject(ctx, path)` after storing the sha256 hash and before calling `markDone`. This is a best-effort call — any error is logged and execution continues.

**`CatalogUpdater` interface** (`internal/processor/processor.go`):

```go
type CatalogUpdater interface {
    // AfterInject updates the SPC MariaDB catalog to reflect a file that
    // was modified server-side by OCR injection. All DB operations are
    // best-effort: errors are logged but do not propagate.
    AfterInject(ctx context.Context, path string) error
}
```

`CatalogUpdater` is an optional field in `WorkerConfig` (nil = SPC sync disabled, following the `Indexer` pattern). `spcCatalog` in `internal/processor/catalog.go` is the concrete implementation. It holds a MariaDB `*sql.DB` and executes these steps in `AfterInject`:

1. `os.Stat(path)` → new file size; `crypto/md5` of file contents → new MD5 hex
2. `SELECT id, user_id, size FROM f_user_file WHERE inner_name = ?` — if no row, log and return
3. `UPDATE f_user_file SET size=?, md5=?, update_time=NOW() WHERE id=?`
4. `INSERT INTO f_file_action (id, user_id, file_id, file_name, inner_name, path, is_folder, size, md5, action, create_time, update_time) VALUES (?, ?, ?, ?, ?, 'NOTE/Note/', 'N', ?, ?, 'A', NOW(), NOW())` — `id` is `time.Now().UnixNano()` (unique, fits bigint)
5. `UPDATE f_capacity SET used_capacity = used_capacity + ?, update_time=NOW() WHERE user_id=?` — delta = new_size − old_size

Each step has its own error check; failures are logged at Warn and do not prevent subsequent steps.

**Wiring** in `main.go`: after `db.Connect(cfg.DSN())`, `processor.NewSPCCatalog(database)` is constructed and assigned to `workerCfg.CatalogUpdater`. No new config flag needed — always enabled when the MariaDB connection exists.

## Existing Patterns

The `CatalogUpdater` interface and its placement in `WorkerConfig` directly mirrors the existing `Indexer` interface:

- `Indexer` defined in `internal/processor/processor.go:13–19`, implemented by `internal/search/index.go`
- `CatalogUpdater` follows the same shape: single-method interface, optional (nil = disabled), called from worker success path

`internal/taskstore/store.go` shows the existing pattern for MariaDB access: raw `*sql.DB` with no additional abstraction layer. `spcCatalog` follows this pattern.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: CatalogUpdater Interface and spcCatalog Implementation

**Goal:** Implement and test the `CatalogUpdater` interface and `spcCatalog` concrete type.

**Components:**
- `internal/processor/processor.go` — add `CatalogUpdater` interface and `CatalogUpdater CatalogUpdater` field to `WorkerConfig`
- `internal/processor/catalog.go` — `spcCatalog` struct, `NewSPCCatalog(*sql.DB) CatalogUpdater`, `AfterInject` implementation (stat, MD5, f_user_file SELECT/UPDATE, f_file_action INSERT, f_capacity UPDATE)
- `internal/processor/catalog_test.go` — tests using in-memory SQLite with subset schema (f_user_file, f_file_action, f_capacity tables); verify all three DB operations succeed, missing f_user_file row is handled gracefully, individual step failures don't prevent subsequent steps

**Dependencies:** None (new file, new interface field)

**Done when:** All catalog tests pass; `go build ./...` and `go vet ./...` succeed; `internal/web/handler_test.go` compiles (mock NoteStore not affected — CatalogUpdater is not on the NoteStore interface)

**Verifies:** spc-catalog-sync.AC1, spc-catalog-sync.AC2, spc-catalog-sync.AC3, spc-catalog-sync.AC4
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Worker Integration and main.go Wiring

**Goal:** Call `CatalogUpdater.AfterInject` from the worker success path and wire the concrete implementation in main.

**Components:**
- `internal/processor/worker.go` — in `processJob` success path, call `s.cfg.CatalogUpdater.AfterInject(ctx, job.NotePath)` after the sha256 store block and before `s.markDone`; nil-guard mirrors `s.cfg.Indexer` pattern
- `cmd/ultrabridge/main.go` — construct `processor.NewSPCCatalog(database)` and assign to `workerCfg.CatalogUpdater`
- `internal/processor/worker_test.go` — add a test verifying that `AfterInject` is called on the success path and not called when the job fails; use a mock `CatalogUpdater`

**Dependencies:** Phase 1 (CatalogUpdater interface and spcCatalog)

**Done when:** Worker calls `AfterInject` after successful injection; nil `CatalogUpdater` causes no panic; full test suite passes; binary builds and starts successfully

**Verifies:** spc-catalog-sync.AC5, spc-catalog-sync.AC6
<!-- END_PHASE_2 -->

## Additional Considerations

**`f_file_action.id` generation:** The Snowflake-format IDs used by the Java SPC service encode a custom-epoch timestamp plus datacenter/worker/sequence bits. UltraBridge uses `time.Now().UnixNano()` instead — this produces a valid `bigint` with nanosecond uniqueness. The IDs won't be in the Snowflake sequence but are unique and correctly ordered for audit purposes.

**`f_file_action.path` hardcoded:** The `path` column stores the SPC directory path (e.g. `NOTE/Note/`). All `.note` files processed by UltraBridge live in the `Note/` subdirectory of the `NOTE` root. Hardcoding `"NOTE/Note/"` avoids a directory tree traversal query while remaining correct for the current file layout.

**`f_capacity` underflow:** The delta is `new_size − old_size` — not the full file size — so underflow is not a risk in practice. MariaDB's `used_capacity` is `bigint unsigned`; if the column is somehow already less than the absolute delta (should not occur), the UPDATE will produce a MariaDB error that is caught, logged, and skipped.
