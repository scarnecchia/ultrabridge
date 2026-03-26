# NoteBridge Phase 2: Blob Storage + File Sync

**Goal:** Tablet can upload, download, list, delete, move, and copy files.

**Architecture:** BlobStore interface with local filesystem implementation. File metadata in syncdb. Signed URLs via JWT+nonces (from Phase 1 auth service). Sync lock with 10-min TTL. Soft delete to recycle table. Chunked uploads with temp storage and auto-merge.

**Tech Stack:** Go 1.24, SQLite, JWT signed URLs, io/fs, crypto/md5

**Scope:** Phase 2 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC2: File Sync
- **AC2.1 Success:** Tablet acquires sync lock, lists files, uploads new file, sees it in next list
- **AC2.2 Success:** Download via signed URL returns correct file content with Range header support
- **AC2.3 Success:** Chunked upload merges parts into final file on last chunk
- **AC2.4 Success:** File delete moves to recycle (soft delete), no longer appears in list
- **AC2.5 Success:** Move/rename updates file path, autorenames on collision
- **AC2.6 Success:** Copy creates independent duplicate with new Snowflake ID
- **AC2.7 Failure:** Sync lock rejects second device with E0078
- **AC2.8 Failure:** Expired signed URL rejected (upload >15min, download >24hr)
- **AC2.9 Failure:** Reused nonce on signed URL rejected (single-use enforcement)
- **AC2.10 Edge:** Sync lock expires after 10min, allowing retry. Lock refreshed on upload finish.

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: BlobStore

<!-- START_TASK_1 -->
### Task 1: BlobStore interface and local filesystem implementation

**Files:**
- Create: `/home/sysop/src/notebridge/internal/blob/storage.go`
- Create: `/home/sysop/src/notebridge/internal/blob/local.go`

**Implementation:**

**storage.go — Interface definition:**

```go
type BlobStore interface {
    Put(ctx context.Context, key string, r io.Reader) (size int64, md5hex string, err error)
    Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) bool
    Path(key string) string // returns absolute filesystem path for a key
}
```

The `Path` method is needed for `http.ServeContent` (Range support) and pipeline access. Keys are relative paths under the storage root (e.g., `user@email.com/Supernote/Note/file.note`).

**local.go — Local filesystem implementation:**

`LocalStore` struct with `rootDir string` field.

Constructor: `NewLocalStore(rootDir string) *LocalStore`

- `Put`: creates parent directories, writes to temp file in same dir, computes MD5 while writing (io.TeeReader), renames temp to final path. Returns size and MD5 hex.
- `Get`: opens file, stats for size, returns file handle and size. Returns os.ErrNotExist-based error if missing.
- `Delete`: os.Remove. No error if not found.
- `Exists`: os.Stat, return !os.IsNotExist.
- `Path`: filepath.Join(rootDir, key).

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add BlobStore interface and local filesystem implementation`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: BlobStore tests

**Files:**
- Create: `/home/sysop/src/notebridge/internal/blob/local_test.go`

**Testing:**

Tests use `t.TempDir()` for isolated filesystem.

- Put writes file, returns correct size and MD5
- Put creates intermediate directories
- Get returns correct content and size
- Get for non-existent key returns error
- Delete removes file
- Delete for non-existent key does not error
- Exists returns true for existing key, false for missing
- Path returns correct absolute path
- Put with empty reader returns size=0
- Concurrent Put to different keys succeeds (no data corruption)

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/blob/
```

**Commit:** `test: add BlobStore local implementation tests`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Chunked upload temp storage

**Files:**
- Create: `/home/sysop/src/notebridge/internal/blob/chunks.go`

**Implementation:**

`ChunkStore` struct with `rootDir string` (e.g., `/data/chunks`).

Methods:
- `SaveChunk(uploadID string, partNumber int, r io.Reader) (md5hex string, err error)` — saves to `{rootDir}/{uploadID}/part_{partNumber:05d}`, computes MD5 while writing
- `MergeChunks(uploadID string, totalChunks int, destStore BlobStore, destKey string) (size int64, md5hex string, err error)` — reads parts 1..totalChunks in order, streams through BlobStore.Put. Returns final file size and MD5. Cleans up chunk directory after successful merge.
- `Cleanup(uploadID string) error` — removes `{rootDir}/{uploadID}/` directory

No tests needed for this task — it's exercised through the chunked upload integration tests in Task 9.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add chunked upload temp storage`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-6) -->
## Subcomponent B: SyncDB File + Lock Operations

<!-- START_TASK_4 -->
### Task 4: SyncDB store — file and lock query methods

**Files:**
- Modify: `/home/sysop/src/notebridge/internal/syncdb/store.go`

**Implementation:**

Add file catalog and sync lock methods to the existing Store:

**Sync Lock:**
- `AcquireLock(ctx, userID int64, equipmentNo string) error` — check for existing unexpired lock held by different device → return ErrSyncLocked if conflict. INSERT OR REPLACE with 10-min TTL.
- `ReleaseLock(ctx, userID int64, equipmentNo string) error` — DELETE from sync_locks
- `RefreshLock(ctx, userID int64) error` — UPDATE expires_at to now+10min

**File Catalog:**
- `CreateFile(ctx, f *FileEntry) error` — INSERT into files table
- `GetFile(ctx, id int64, userID int64) (*FileEntry, error)` — SELECT by id and user_id
- `GetFileByPath(ctx, userID int64, directoryID int64, fileName string) (*FileEntry, error)` — SELECT by user_id + directory_id + file_name
- `UpdateFileMD5(ctx, id int64, md5 string, size int64) error` — UPDATE md5, size, updated_at
- `ListFolder(ctx, userID int64, directoryID int64) ([]FileEntry, error)` — SELECT where user_id and directory_id match, ordered: folders first (is_folder DESC), then by file_name
- `ListFolderRecursive(ctx, userID int64, directoryID int64) ([]FileEntry, error)` — recursive listing using a BFS/DFS over folder IDs
- `SoftDelete(ctx, id int64, userID int64) error` — INSERT into recycle_files (copy from files), DELETE from files
- `MoveFile(ctx, id int64, newDirectoryID int64, newFileName string) error` — UPDATE directory_id, file_name, updated_at
- `GetAncestorIDs(ctx, directoryID int64, limit int) ([]int64, error)` — walk parent chain for circular move detection
- `FindByName(ctx, userID int64, directoryID int64, baseName string) ([]string, error)` — find existing names matching pattern for autorename
- `SpaceUsage(ctx, userID int64) (int64, error)` — SUM(size) FROM files WHERE user_id = ? AND is_folder = 'N'

**Chunk tracking:**
- `SaveChunkRecord(ctx, uploadID string, partNumber, totalChunks int, md5, path string) error` — INSERT into chunk_uploads
- `CountChunks(ctx, uploadID string) (int, error)` — COUNT(*) from chunk_uploads for uploadID
- `DeleteChunkRecords(ctx, uploadID string) error` — DELETE from chunk_uploads for uploadID

**Types:**
- `FileEntry` struct: ID int64, UserID int64, DirectoryID int64, FileName, InnerName, StorageKey, MD5 string, Size int64, IsFolder bool, IsActive bool, CreatedAt, UpdatedAt time.Time

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add syncdb file catalog and sync lock methods`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: SyncDB file and lock tests

**Verifies:** AC2.7 (sync lock conflict), AC2.10 (lock expiry and refresh)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/syncdb/store_file_test.go`

**Testing:**

All tests use in-memory SQLite. Create helper `setupTestStore(t) *Store` that opens `:memory:` and bootstraps a test user.

**Sync lock tests:**
- AcquireLock succeeds for first device
- AC2.7: AcquireLock by second device while first holds lock → returns error
- AC2.10: AcquireLock by second device after first lock expires → succeeds
- ReleaseLock removes lock, allows new acquire
- RefreshLock extends expiry

**File catalog tests:**
- CreateFile + GetFile round-trip
- GetFileByPath finds correct file
- ListFolder returns files in correct order (folders first, then by name)
- ListFolderRecursive returns nested files
- UpdateFileMD5 updates md5, size, and updated_at
- SoftDelete moves file to recycle_files, removes from files
- SoftDelete: file no longer appears in ListFolder
- MoveFile updates directory_id and file_name
- GetAncestorIDs returns correct chain, stops at root (directoryID=0)
- FindByName returns existing names for autorename detection
- SpaceUsage sums file sizes correctly

**Chunk tracking tests:**
- SaveChunkRecord + CountChunks round-trip
- DeleteChunkRecords removes all chunks for uploadID

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/syncdb/
```

**Commit:** `test: add syncdb file and lock operation tests`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Autorename and circular move helpers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/fileutil.go`
- Create: `/home/sysop/src/notebridge/internal/sync/fileutil_test.go`

**Implementation:**

Pure functions (no database dependency) for file operation helpers:

`AutoRename(baseName string, existingNames []string) string` — given a filename like "notes.pdf" and existing names at the destination, generates "notes(1).pdf", "notes(2).pdf", etc. until a non-colliding name is found. For folders (no extension), appends "(N)" directly.

`IsCircularMove(movingID int64, ancestorIDs []int64) bool` — checks if movingID appears in the ancestor chain.

`SplitNameExt(filename string) (name, ext string)` — splits "notes.pdf" → ("notes", ".pdf"), handles no extension.

**Testing:**

- AutoRename with no collisions returns original name
- AutoRename with collision returns "name(1).ext"
- AutoRename with "name(1)" taken returns "name(2)"
- AutoRename with folder (no extension) appends "(1)"
- IsCircularMove detects cycle
- IsCircularMove returns false for safe move
- SplitNameExt handles various cases: with ext, no ext, multiple dots

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/
```

**Commit:** `feat: add autorename and circular move detection helpers`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 7-10) -->
## Subcomponent C: File Sync HTTP Handlers

<!-- START_TASK_7 -->
### Task 7: Sync start/end and folder handlers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_sync.go`
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_folders.go`
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — register new routes

**Implementation:**

**handlers_sync.go:**

`handleSyncStart(w, r)`:
- Parse body: equipmentNo
- Get userID from context (auth middleware)
- Call store.AcquireLock(ctx, userID, equipmentNo)
- On conflict: return jsonError with E0078 (AC2.7)
- On success: return jsonSuccess with equipmentNo, synType

`handleSyncEnd(w, r)`:
- Parse body: equipmentNo
- Call store.ReleaseLock(ctx, userID, equipmentNo)
- Return jsonSuccess

**handlers_folders.go:**

`handleCreateFolder(w, r)`:
- Parse body: equipmentNo, path, autorename
- Parse path to extract parent directory and folder name
- Check for name collision, autorename if requested
- Create folder entry in files table (is_folder='Y') with Snowflake ID
- Return jsonSuccess with metadata (tag, id, name, path_display)

`handleListFolderV3(w, r)`:
- Parse body: equipmentNo, id (directory Snowflake ID), recursive
- Call store.ListFolder or ListFolderRecursive
- For each file entry, verify file exists on disk via blobStore.Exists (skip stale entries)
- Format entries as: tag, id (string), name, path_display, content_hash, size, lastUpdateTime, is_downloadable, parent_path
- Return jsonSuccess with entries array

`handleListFolderV2(w, r)`:
- Path-based version for backward compatibility
- Resolve path to directory ID, then delegate to same listing logic

**Register routes in server.go:**
- `POST /api/file/2/files/synchronous/start`
- `POST /api/file/2/files/synchronous/end`
- `POST /api/file/2/files/create_folder_v2`
- `POST /api/file/3/files/list_folder_v3`
- `POST /api/file/2/files/list_folder`

All require AuthMiddleware.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add sync lock and folder listing handlers`
<!-- END_TASK_7 -->

<!-- START_TASK_8 -->
### Task 8: Upload handlers (apply, full upload, chunked, finish)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_upload.go`
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — register upload routes

**Implementation:**

**handlers_upload.go:**

`handleUploadApply(w, r)`:
- Parse body: equipmentNo, path, fileName
- Resolve parent directory, create it if needed
- Generate signed upload URLs (via authService.GenerateSignedURL with 15-min TTL for uploads)
- Return jsonSuccess with innerName, fullUploadUrl, partUploadUrl

`handleOssUpload(w, r)`:
- Verify signed URL from query params (signature verification via authService.VerifySignedURL)
- Parse multipart form, get "file" part
- Decode path from base64 query param
- Determine storage key from user email + path
- Call blobStore.Put(ctx, storageKey, fileReader)
- Return HTTP 200

`handleOssUploadPart(w, r)`:
- Verify signed URL
- Parse form: file, partNumber, totalChunks, uploadId
- If uploadId empty, generate UUID
- Save chunk via chunkStore.SaveChunk
- Record in DB via store.SaveChunkRecord
- Check if all chunks received (store.CountChunks >= totalChunks)
- If complete: merge via chunkStore.MergeChunks, clean up DB records
- Return JSON with uploadId, partNumber, totalChunks, chunkMd5, status

`handleUploadFinish(w, r)`:
- Parse body: equipmentNo, path, fileName, content_hash, size
- Find or create file entry in syncdb
- If file exists: update MD5 and size
- If new: create file entry with Snowflake ID
- Refresh sync lock (store.RefreshLock)
- Return jsonSuccess with path_display, id, size, name, content_hash

**Register routes in server.go:**
- `POST /api/file/3/files/upload/apply` (auth required)
- `POST /api/oss/upload` (signature-verified, no auth middleware)
- `POST /api/oss/upload/part` (signature-verified, no auth middleware)
- `POST /api/file/2/files/upload/finish` (auth required)

Note: `/api/oss/*` routes use signed URL verification instead of JWT auth (the signature IS the auth).

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add file upload handlers with chunked support`
<!-- END_TASK_8 -->

<!-- START_TASK_9 -->
### Task 9: Download, delete, query, move, copy, space handlers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_download.go`
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_fileops.go`
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — register remaining routes

**Implementation:**

**handlers_download.go:**

`handleDownloadV3(w, r)`:
- Parse body: equipmentNo, id
- Get file entry from store
- If not found or not on disk: return error
- Generate signed download URL (24-hr TTL)
- Return jsonSuccess with id, url, name, path_display, content_hash, size, is_downloadable

`handleOssDownload(w, r)`:
- Verify signed URL from query params
- Decode path from base64
- Open file via blobStore.Path (need absolute path for ServeContent)
- Use `http.ServeContent(w, r, filename, modtime, file)` for Range support (AC2.2)
- Return 404 if file missing

**handlers_fileops.go:**

`handleDeleteV3(w, r)`:
- Parse body: equipmentNo, id
- Get file entry
- If folder: recursively collect all child IDs
- Call store.SoftDelete for each (moves to recycle_files) (AC2.4)
- Delete from disk via blobStore.Delete
- Refresh sync lock
- Return jsonSuccess with metadata

`handleQueryV3(w, r)`:
- Parse body: equipmentNo, id
- Get file entry, format as entriesVO
- Return jsonSuccess

`handleQueryByPathV3(w, r)`:
- Parse body: equipmentNo, path
- Resolve path to file/folder entry
- Return jsonSuccess with entriesVO

`handleMoveV3(w, r)`:
- Parse body: equipmentNo, id, to_path, autorename
- Validate: source exists, destination parent exists
- Circular move detection for folders (store.GetAncestorIDs + IsCircularMove) → E0358
- Collision detection: check if name exists at destination
- If collision and autorename: AutoRename
- If collision and !autorename: return E0322
- Move on disk (os.Rename via blob paths)
- Update DB (store.MoveFile)
- Refresh sync lock (AC2.5)
- Return jsonSuccess with updated entriesVO

`handleCopyV3(w, r)`:
- Parse body: equipmentNo, id, to_path, autorename
- Create new Snowflake ID
- Copy file on disk (read from blob, write to new key)
- Create new file entry in DB with new ID (AC2.6)
- For folders: recursive deep copy with new IDs for all children
- Autorename on collision
- Refresh sync lock
- Return jsonSuccess with new entriesVO

`handleSpaceUsage(w, r)`:
- Parse body: equipmentNo
- Call store.SpaceUsage(ctx, userID)
- Return jsonSuccess with used bytes and allocation info

**Register routes in server.go:**
- `POST /api/file/3/files/download_v3` (auth required)
- `GET /api/oss/download` (signature-verified)
- `POST /api/file/3/files/delete_folder_v3` (auth required)
- `POST /api/file/3/files/query_v3` (auth required)
- `POST /api/file/3/files/query/by/path_v3` (auth required)
- `POST /api/file/3/files/move_v3` (auth required)
- `POST /api/file/3/files/copy_v3` (auth required)
- `POST /api/file/3/files/space_usage` (auth required)

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add download, delete, move, copy, and query handlers`
<!-- END_TASK_9 -->

<!-- START_TASK_10 -->
### Task 10: File sync integration tests

**Verifies:** AC2.1 (full sync cycle), AC2.2 (Range download), AC2.3 (chunked upload), AC2.4 (soft delete), AC2.5 (move/rename), AC2.6 (copy), AC2.7 (sync lock conflict), AC2.8 (expired signed URL), AC2.9 (reused nonce), AC2.10 (lock expiry/refresh)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_file_test.go`

**Testing:**

Integration tests using `httptest.NewServer` with full server handler (real SQLite, real blob store using `t.TempDir()`, real auth). Build on the `setupTestServer` helper from Phase 1, extended to include BlobStore and ChunkStore.

Test helper additions: `loginAndGetToken(t, server)` — performs challenge-response flow, returns JWT token. `authRequest(t, method, url, body, token)` — creates request with Bearer token.

**Test cases:**

- AC2.1 full sync cycle:
  1. Login, get token
  2. POST sync/start → success
  3. POST list_folder_v3 (root) → empty entries
  4. POST upload/apply → get signed upload URL
  5. POST oss/upload with file content to signed URL
  6. POST upload/finish with filename, content_hash, size
  7. POST list_folder_v3 → file appears with correct MD5 and size
  8. POST sync/end → success

- AC2.2 Range download:
  1. Upload a file
  2. POST download_v3 → get signed download URL
  3. GET oss/download → full file content matches uploaded
  4. GET oss/download with `Range: bytes=0-9` header → returns first 10 bytes, status 206

- AC2.3 chunked upload:
  1. Upload/apply to get part upload URL
  2. POST oss/upload/part with part 1 of 3
  3. POST oss/upload/part with part 2 of 3
  4. POST oss/upload/part with part 3 of 3 (triggers auto-merge)
  5. Upload/finish
  6. Download and verify full content is concatenation of all parts

- AC2.4 soft delete:
  1. Upload a file
  2. POST delete_folder_v3 with file ID
  3. POST list_folder_v3 → file no longer listed
  4. Verify: recycle_files table has the entry (query DB directly)

- AC2.5 move/rename:
  1. Upload file "test.note"
  2. Create destination folder
  3. POST move_v3 to move file into folder
  4. List original folder → file gone
  5. List destination folder → file present with same content_hash
  6. Test autorename: create collision, move with autorename=true → "test(1).note"

- AC2.6 copy:
  1. Upload file
  2. POST copy_v3 to same folder with autorename
  3. List folder → two files (original + copy with different ID)
  4. Download both → same content
  5. Modify original → copy unchanged (independent)

- AC2.7 sync lock conflict:
  1. Login as device A, sync/start
  2. Login as device A again (different equipmentNo), sync/start
  3. Assert error E0078

- AC2.8 expired signed URL:
  1. Upload/apply to get signed URL
  2. Manually set nonce expiry to past in DB
  3. Attempt upload with expired URL
  4. Assert rejection

- AC2.9 reused nonce:
  1. Upload a file successfully (consumes nonce)
  2. Replay same signed URL with same nonce
  3. Assert rejection (nonce already consumed)

- AC2.10 lock expiry and refresh:
  1. Login, sync/start
  2. Manually set lock expiry to past in DB
  3. Login as different device, sync/start → succeeds (expired lock)
  4. Test refresh: upload/finish extends lock expiry

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestFile
```

Expected: All tests pass.

**Commit:** `test: add file sync integration tests`
<!-- END_TASK_10 -->
<!-- END_SUBCOMPONENT_C -->
