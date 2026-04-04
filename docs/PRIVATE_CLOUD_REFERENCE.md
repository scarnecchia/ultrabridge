# Supernote Private Cloud — Implementation Reference

A technical reference for reimplementing the Supernote Private Cloud sync service. Covers the complete REST API, database schema, file sync protocol, and client behavior as observed through packet captures and database analysis.

---

## 1. Architecture Overview

### Stack

| Container | Image | Ports | Role |
|---|---|---|---|
| **mariadb** | 10.6.19 | 3306 | Metadata catalog, user accounts, task/digest storage |
| **redis** | 7.0.12 | 6379 | Session tokens, JWT auth, upload tracking (13 keys total) |
| **notelib** | latest | 6000 | `.note` to PNG/PDF conversion for web UI preview |
| **supernote-service** | latest | 8080 (nginx), 19071 (java), 9888 (node), 18072 (socket.io) | Core sync service (opaque Java binary) + web UI |

Images are from Alibaba Cloud registry. All containers run via Docker Compose.

### Request Routing

```
Client (device / browser / Partner app)
  → HTTPS (TLS terminated by nginx inside supernote-service, port 8080)
    → nginx proxies to:
       /api/*        → java:19071  (main backend)
       /socket.io/*  → java:18072  (real-time push)
       /             → node:9888   (web UI static assets)
```

All three services run inside the **same container**. Traffic between nginx and backends is on loopback (127.0.0.1), never crosses the Docker bridge network.

### Storage Model

1. **Database** (`supernotedb` in MariaDB) — metadata catalog: file index, user accounts, tasks, digests, audit logs
2. **Filesystem** (`supernote_data/`) — actual file content: notes, PDFs, epubs, etc.
3. **Redis** — ephemeral: JWT tokens, socket counters, upload tracking

The server is a **dumb file store with a metadata catalog**. All intelligence (handwriting recognition, search, task management UI) lives on the device. The server receives files, tracks them in the database, and serves them back.

### Two Sync Channels

| Channel | What Syncs | Storage |
|---|---|---|
| **File sync** | `.note`, `.pdf`, `.epub`, `.mark`, etc. as opaque blobs | `f_user_file` table + `supernote_data/` on disk |
| **Data sync** ("My Account > Data Sync") | Structured records: tasks, digests | `t_schedule_task`, `t_summary` tables |

### On-Disk File Storage Layout

```
supernote_data/
└── <user_email>/
    └── Supernote/
        ├── Note/           ← .note files
        ├── Document/       ← PDFs, EPUBs, etc.
        ├── Export/
        ├── Screenshot/
        └── Inbox/
```

Files are stored with their original names. The `inner_name` field in `f_user_file` maps to the on-disk filename.

Digest handwriting annotations are stored separately:
```
sndata/digest/          ← {md5}.mark files for digest handwriting annotations
sndata/logs/web/        ← nginx access logs
/home/supernote/convert/ ← notelib PNG output (inside container)
```

### Redis (7.0.12)

Only 13 keys. No search functionality:

| Key Pattern | Type | Purpose |
|---|---|---|
| `*_token` | string | JWT auth tokens |
| `email_public_key` / `email_private_key` | string | Email encryption keys |
| `socket_connected_count` / `socket_disconnected_count` | string | WebSocket counters |
| `upload_*` | zset | Upload tracking (temp file paths + timestamps) |
| `nullredisKey` / `token_null` | string | Sentinel/null markers |
| `<userid>_<timestamp>_<timestamp>` | string | Session tracking |

---

## 2. Database Schema

Database name: `supernotedb`. MariaDB 10.6.19, user `enote`.

### Conventions

- **IDs**: Snowflake format (e.g., `1184673925533868032`), except task IDs which are MD5 hashes
- **Timestamps**: Millisecond UTC unix timestamps. `0` = unset
- **Table prefixes**: `b_` = base/backend admin, `e_` = equipment/device, `f_` = file operations, `t_` = tasks/data, `u_` = user

### `f_user_file` — File Catalog

The complete device filesystem tree, mirrored. Self-referential via `directory_id`.

- **Root folders** (directory_id=0): `DOCUMENT`, `NOTE`, `EXPORT`, `SCREENSHOT`, `INBOX`
- Each root has a display subfolder (e.g., `NOTE` contains `Note`)
- Epubs are NOT exploded by the service. If you import a Calibre library with `.opf`/`.jpg` sidecar files, each one gets its own row.
- IDs are Snowflake format. Timestamps are ms UTC.

### `f_file_action` — Audit Log

Every file operation is logged with an action code:

| Code | Meaning |
|---|---|
| `A` | Add (file created/uploaded) |
| `C` | Copy (file duplicated — `new_file_name` = copy name, `new_path` = destination) |
| `M` | Modify (rename — same directory, new filename recorded in `new_file_name`) |
| `R` | Relocate (moved between folders — old path + new path recorded) |
| `D` | Delete (single file) |
| `DR` | Delete Recursive (folder) |
| `DM` | Delete-Move (rare, possibly move-to-trash) |

Tracks: `file_id`, `file_name`, `new_file_name`, `path`, `new_path`, `md5`, `inner_name`, `is_folder`, `size`, timestamps.

Note: the same file can appear multiple times with action `A` at the same timestamp with decreasing sizes — this is the compaction behavior (see section 4, Note Compaction Behavior).

### `f_capacity` — Storage Quota

Fields: `user_id`, `used_capacity`, `total_capacity` (bytes).

### `f_sync_record` — Sync Statistics

Fields: `success_count`, `fail_count`, `total_time`. AUTO_INCREMENT = 4.

### `f_file_server_change` — Server Migration

Tracks when users are moved between storage backends. Fields: `equipment_number`, `user_id`, `old_file_server`, `new_file_server`, `change_time`.

### `f_terminal_file_convert` — File Conversion Jobs (notelib)

Fields: `equipment_number`, `file_type` (1=PDF, 2=PNG), `file_name`, `convert_type`, `share_id`, `page_no`, `url`.

| `convert_type` | Operation |
|---|---|
| `1` | note to PDF |
| `2` | PDF + mark to PDF (merge annotations) |
| `3` | note to PNG |

### `f_terminal_share_file` — Device-Initiated Sharing

Fields: `equipment_number`, `inner_name`, `file_name`. Linked from `f_terminal_file_convert` via `share_id`.

### `f_share_record` — Sharing Audit Log

Fields: `user_id`, `file_id`, `share_way`.

### `f_recycle_file` — Trash/Recycle Bin

Same structure as `f_user_file`. Files moved here on deletion before permanent removal. (May not be actively used — hard deletes were observed in practice.)

### `f_file_his_sync` — Historical Sync Records

Empty on private cloud. May only be used on the public cloud.

### `e_equipment` — Device Registry

Fields: `equipment_number` (serial, e.g., `SN078C10034074`), `firmware_version`, `update_status` (0=initial, 1=not updated, 2=updated), `equipment_model`.

### `e_user_equipment` / `e_user_equipment_record` — Device Binding

User-to-device binding. Fields include serial number, device name, user ID. `e_user_equipment_record` tracks binding history with a `type` field.

### `e_equipment_authorize` — OAuth Authorization

Fields: `equipment_number`, `authorize` (token), `app_name` (default: `'Dropbox'`), `random` (challenge code), `status` (`N`=authorizing, `Y`=authorized, `Z`=timeout). Stores Calendar (Google/Microsoft) and cloud storage (Dropbox) bindings.

### `e_task` / `e_task_his` — Device Command Queue

**NOT user tasks.** Remote device management commands. `task_code`: `01` = lock device, `02` = unlock device, `03` = firmware update. `e_task` is the pending queue, `e_task_his` is execution history with a `result` field (0=not executed, 1=success).

### `e_equipment_log` — Device Activity Logging

Heavily used on production (AUTO_INCREMENT = 54,632).

### `e_equipment_manual` / `e_equipment_warranty` — Docs and Warranty

### `e_language` — Language Packs

Fields: `file_name`, `country_code`, `md5`, `size`.

### `e_temp` / `e_temp_now` — Staging Tables

Minimal tables with just a `user_id` column. Likely used for batch operations.

### `u_user` — User Account

Main user table for device users (not admins).

### `u_login_record` — Login History

Fields: `user_id`, `login_method` (1=phone, 2=email, 3=WeChat), `ip`, `browser`, `equipment`.

Equipment types: `1` = web browser, `2` = mobile app, `3` = main account login, `4` = terminal (device).

### `u_email_config` — SMTP Configuration

Fields: `smtp_server`, `port`, `user_name`, `password`, `encryption` (ssl/tls), `flag` (Y=active), `test_email`.

**Security note**: Passwords are stored in plaintext.

### `u_sensitive_record` — Security Event Audit

`operate_record` codes:

| Code | Event |
|---|---|
| `01` | Password recovery |
| `02` | Password change |
| `03` | Phone number change |
| `04` | Email change |
| `05` | Device lock |
| `06` | Device unlock |
| `07` | Remote/unusual login |

### `u_commonly_area` — Login Location Tracking

Fields: `user_id`, `country_code`, `area_code`, `count`.

### `u_commonly_equipment` — Frequent Devices

Fields: `user_id`, `equipment_number`.

### `u_data_migration_record` — Data Migration

Fields: `user_id`, `equipment_number`, contact info, `count`, `state` (0=migrating, 1=success, 2=failed).

### `u_user_history` — User Profile Archive

Full demographic profile including `file_server` field (0=ufile, 1=AWS — reveals storage backend options).

### `u_user_sold_out` — Deactivated Accounts

User records are moved here on account deletion.

### `t_machine_id` — Server Instance Identifier

1 row with a unique string. Used to identify this installation, likely for Snowflake ID generation (worker ID component).

### `b_*` — Backend Administration (Ratta Internal)

These tables power Ratta's internal admin panel. On a private cloud deployment they are entirely unused.

#### `b_user` / `b_role` / `b_role_tresource` / `b_resource` / `b_user_trole`

Classic RBAC for Ratta's admin console. `b_user` holds admin accounts (not device users). `b_resource` defines menu items and API endpoints, `b_role` defines roles, join tables wire them together.

#### `b_pwd_his`

Admin password history to prevent reuse.

#### `b_dictionary`

Enum/lookup table: `name`, `value`, `value_cn` (Chinese), `value_en` (English). Empty on private cloud — populated at runtime by the Java service on the public cloud.

#### `b_schedule_task` / `b_schedule_log`

System-level cron job scheduler (NOT user tasks). Fields: `name`, `remark`, `cron`, `status` (0=enabled, 1=disabled), `bzcode`. Log fields: `ksrq` (start time), `jsrq` (end time), `task_id`, `result` (0=success, 1=failure). 19 scheduled tasks defined.

#### `b_reference`

System configuration (48 rows). See section 8 for details.

### DB vs File Storage Summary

| Feature | In Database? | In `.note`/`.mark` File? | Sync Channel |
|---|---|---|---|
| Tasks | Yes (`t_schedule_task`) | No | Data Sync |
| Digests/Highlights | Yes (`t_summary`) | Annotation in `.mark` | Data Sync + File Sync |
| Headings | **No** | Yes (`TITLE*` tags) | File Sync only |
| Keywords | **No** | Yes (`KEYWORD*` tags) | File Sync only |
| Stars | **No** | Yes (`FIVESTAR` tag) | File Sync only |
| Links | **No** | Yes (`LINK*` tags) | File Sync only |
| Recognized text | **No** | Yes (`RECOGNTEXT`) | File Sync only |
| Text boxes | **No** | Yes (`PAGETEXTBOX`) | File Sync only |
| File catalog | Yes (`f_user_file`) | N/A | File Sync |
| File audit log | Yes (`f_file_action`) | N/A | File Sync |

### Production Scale Indicators

AUTO_INCREMENT values from the schema (exported from Ratta's production system):

| Table | AUTO_INCREMENT | Implies |
|---|---|---|
| `e_equipment` | 3,535 | ~3,500 registered devices |
| `e_equipment_authorize` | 3,351 | ~3,350 OAuth authorizations |
| `e_equipment_log` | 54,632 | ~55K device activity events |
| `b_schedule_log` | 200,640 | ~200K cron job executions |
| `u_login_record` | 20,721 | ~21K logins |
| `f_terminal_file_convert` | 6,035 | ~6K file conversions via notelib |
| `f_terminal_share_file` | 1,895 | ~1,900 shared files |
| `f_capacity` | 54,320 | ~54K quota records |
| `b_user` | 148 | ~147 Ratta admin accounts |
| `b_dictionary` | 508 | ~507 enum/lookup entries |

---

## 3. Authentication & Identity

### Password Hashing

All login flows use a challenge-response password hashing scheme:

1. Client requests a `randomCode` and `timestamp` from the server
2. Client hashes the password with SHA-256 mixed with the `randomCode` and `timestamp`
3. The resulting hash is sent as a 64-character hex string (different each attempt)

### Device Login Flow (5-Step)

```
1. POST /api/terminal/equipment/unlink
   → {"equipmentNo":"SN078C10034074","version":"202407"}
   ← {"success":true}

2. POST /api/official/user/check/exists/server
   → {}
   ← {"success":true,"dms":"ALL","userId":1184673925533868032,
      "uniqueMachineId":"98IdWznd3q5y5qVY7cyqtaXFicQn1W0g"}

3. POST /api/official/user/query/random/code
   → {"countryCode":null,"account":"user@example.com"}
   ← {"success":true,"randomCode":"...","timestamp":1773451606340}

4. POST /api/official/user/account/login/equipment
   → {"account":"user@example.com","equipment":3,"equipmentNo":"SN078C10034074",
      "loginMethod":"2","password":"dd55870d...","timestamp":1773451606340,"version":"202407"}
   ← {"success":true,"token":"eyJ...","counts":"0","userName":"user@example.com",
      "isBind":"N","isBindEquipment":"N","soldOutCount":0}

5. POST /api/terminal/user/bindEquipment
   → {"account":"user@example.com","equipmentNo":"SN078C10034074","flag":"1",
      "name":"My Device","totalCapacity":"25485312","version":"202407",
      "label":["DOCUMENT/Document","NOTE/Note","NOTE/MyStyle","EXPORT","SCREENSHOT","INBOX"]}
   ← {"success":true}
```

Key observations:
- Step 1 **unlinks** the device first, even before login. Every failed attempt also starts with unlink.
- `equipment: 3` = e-ink tablet (vs `"1"` for web browser)
- `loginMethod: "2"` = device login (vs `"1"` for web)
- `bindEquipment` registers the device with its name, storage capacity (in KB), and the **default directory structure** the server should create.
- The `label` array defines the top-level directories: `DOCUMENT/Document`, `NOTE/Note`, `NOTE/MyStyle`, `EXPORT`, `SCREENSHOT`, `INBOX`
- Error code `E0019` = wrong password, `counts` tracks failed attempts (lockout at 6 per `MAX_ERR_COUNTS`)

### Web UI Login Flow (2-Step)

```
1. POST /api/official/user/query/random/code
   → {"countryCode":null,"account":"user@example.com"}
   ← {"success":true,"randomCode":"tOiJKwhW","timestamp":1773336349503}

2. POST /api/official/user/account/login/new
   → {"countryCode":null,"account":"user@example.com",
      "password":"d67d7e843eba25b9...","browser":"Chrome144",
      "equipment":"1","loginMethod":"1","timestamp":1773336349503,"language":"en"}
   ← {"success":true,"token":"eyJ...","isBind":"Y","soldOutCount":0}
```

### Device vs Web UI Login Comparison

| Aspect | Device | Web UI |
|---|---|---|
| Endpoint | `login/equipment` | `login/new` |
| Pre-login steps | `equipment/unlink` + `check/exists/server` | `query/random/code` only |
| Post-login steps | `bindEquipment` | None |
| `equipment` field | `3` (tablet) | `"1"` (browser) |
| `loginMethod` | `"2"` | `"1"` |
| Extra fields | `equipmentNo`, `version` | `browser`, `language` |
| JWT expiry | Never (no `exp` field) | 30 days |
| JWT payload | Includes `equipmentNo` | No `equipmentNo` |

### JWT Format & Lifetime

JWT tokens are passed in the `x-access-token` header on every API request.

**Device JWT payload:**
- `createTime`, `equipmentNo` (device serial), `userId` (snowflake ID), `key` (composite string)
- **No `exp` field** — never expires. No refresh flow needed.

**Web UI JWT payload:**
- `createTime`, `exp` (30 days from creation), `userId`, `key` (format: `userId_createTime_lastUpdateTime`)

The `channel` header value = JWT + `_` + equipmentNo + `_` + timestamp — used for socket.io channel identification.

**Partner app JWT lifetime**: ~10 years (e.g., created 2026-03-04, expires 2036-01-11). Effectively permanent.

### Equipment Binding / Unlinking

- `POST /api/terminal/user/bindEquipment` — binds a device to an account, registers device name, storage capacity, and directory structure
- `POST /api/terminal/equipment/unlink` — unlinks a device. Called before every login attempt and on logout.
- `POST /api/equipment/bind/status` — heartbeat/presence check. Returns `{"success":true,"bindStatus":true}`. The Partner app calls this constantly (every 3-5 seconds).

### RSA Public Key Endpoint (Vestigial)

`GET /api/query/email/publickey` returns a 2048-bit RSA public key (PKCS#1). Despite the `/email/` in the path, this was intended for web UI password encryption. However, the actual web login flow does NOT use RSA encryption — the password is sent as a hex-encoded SHA-256 hash. The endpoint may be vestigial or used only in specific registration flows. The web UI loads `jsencrypt.min.js` but does not appear to use it for login.

### Error Codes

| Code | Meaning |
|---|---|
| `E0019` | Wrong password |

Failed attempts are counted in `counts`. Lockout occurs at 6 attempts (`MAX_ERR_COUNTS` in `b_reference`).

---

## 4. File Sync API

### Signed URL Scheme

All file upload/download URLs use HMAC signatures:
- `signature` — HMAC signature
- `timestamp` — request timestamp
- `nonce` — random value
- `path` — **base64-encoded** file path (e.g., `L05PVEUvTm90ZQ` = `/NOTE/Note`)
- `pathId` — Snowflake ID of the `f_user_file` row (on downloads)

Signatures are generated server-side and returned to the client. The `xamzDate` field name in responses suggests the protocol was originally designed for S3-compatible storage.

### Upload Protocol

#### Device Upload (4-Step)

**Step 1: Check if file exists** — `POST /api/file/3/files/query/by/path_v3`

```json
→ {"path":"/NOTE/Note/example-note.note","equipmentNo":"SN078C10034074"}
← {"success":true,"errorCode":null,"errorMsg":null,"equipmentNo":null,"entriesVO":null}
```

`entriesVO: null` means the file does not exist on the server. If it exists, this returns the current metadata (content_hash, size, etc.) so the device can decide whether to upload.

**Step 2: Request signed upload URL** — `POST /api/file/3/files/upload/apply`

```json
→ {"path":"/NOTE/Note/example-note.note",
   "fileName":"example-note.note",
   "equipmentNo":"SN078C10034074",
   "size":239886}

← {"success":true,
   "equipmentNo":"SN078C10034074",
   "bucketName":"example-note.note",
   "innerName":"example-note.note",
   "authorization":"d71ea717d2d2d421...",
   "fullUploadUrl":"https://cloud.example.com/api/oss/upload?signature=...&timestamp=...&nonce=...&path=L05PVEUvTm90ZQ",
   "partUploadUrl":"https://cloud.example.com/api/oss/upload/part?signature=...&timestamp=...&nonce=...&path=L05PVEUvTm90ZQ",
   "xamzDate":"1773292687879"}
```

- **Single-shot upload** (`fullUploadUrl`): Used for files < 8MB
- **Chunked upload** (`partUploadUrl`): Used for files >= 8MB, with additional params `uploadId`, `totalChunks`, `partNumber`
- Chunk size: **8MB** (8,388,608 bytes)
- `uploadId` format: `{timestamp}_{uuid}` (e.g., `1771564346811_bfb11589-81b8-4580-9ee9-953144da9cc5`)

**Step 3: Upload file data** — `POST /api/oss/upload` (multipart/form-data)

```
Content-Disposition: form-data; name="file"; filename="example-note.note"
```

Response: `{"success":true,"errorCode":null,"errorMsg":null,"innerName":null,"md5":null}`

**Step 4: Confirm upload** — `POST /api/file/3/files/upload/confirm`

```json
→ {"content_hash":"33d1faaf58f0d0edc56f1be35868fe80",
   "equipmentNo":"SN078C10034074",
   "fileName":"example-note.note",
   "innerName":"example-note.note",
   "path":"/NOTE/Note/",
   "size":"239886"}

← {"success":true,
   "equipmentNo":"SN078C10034074",
   "path_display":"NOTE/Note/example-note.note",
   "id":"819922212654940160",
   "size":239886,
   "content_hash":"33d1faaf58f0d0edc56f1be35868fe80"}
```

The `content_hash` is an **MD5** of the file contents. The server returns the snowflake `id` for the new `f_user_file` row. This also creates an `f_file_action` entry with action=`A`.

#### Web UI Upload (3-Step)

```
1. POST /api/file/upload/apply
   → {"size":499257,"fileName":"example-document.pdf","directoryId":"812654303247335424",
      "md5":"6f493055a6d6e36e2be751a4da903200"}
   ← {"success":true,"fullUploadUrl":"https://cloud.example.com/api/oss/upload?signature=...&path={base64}",
      "partUploadUrl":"https://cloud.example.com/api/oss/upload/part?...","innerName":"example-document.pdf"}

2. POST /api/oss/upload?signature=...&path={base64}
   (multipart/form-data with file body)
   ← {"success":true}

3. POST /api/file/upload/finish
   → {"directoryId":"...","fileName":"example-document.pdf","fileSize":499257,
      "innerName":"example-document.pdf","md5":"6f493055a6d6e36e2be751a4da903200"}
   ← {"success":true}
```

#### Upload Comparison Table

| Aspect | Device (v3) | Web UI |
|---|---|---|
| Pre-check | `query/by/path_v3` (by path) | None |
| Apply endpoint | `/api/file/3/files/upload/apply` | `/api/file/upload/apply` |
| Apply request key | `path`, `equipmentNo` | `directoryId`, `md5` |
| Upload endpoint | `/api/oss/upload` (same) | `/api/oss/upload` (same) |
| Confirm endpoint | `/api/file/3/files/upload/confirm` | `/api/file/upload/finish` |
| Confirm request | `content_hash`, `equipmentNo`, `path` | `directoryId`, `md5` |
| Confirm response | Returns `id`, `content_hash`, `path_display` | Returns `success` only |

There is also a v2 confirm endpoint (`POST /api/file/2/files/upload/finish`) used by devices in some flows, with the same request shape as the v3 confirm.

### Download Protocol

#### Device Download

`GET /api/oss/download?path={base64}&signature=...&timestamp=...&nonce=...&pathId={snowflake_id}`

- `path`: Base64-encoded full path (e.g., `NOTE/Note//example-note.note`)
- `pathId`: Snowflake ID of the `f_user_file` row
- Returns HTTP 200 (full file) or 206 (partial/range request)

#### Partner App Download (3-Step)

```json
→ POST /api/file/3/files/query_v3
  {"equipmentNo":"ANDROID-c077360c-...","id":"812673121772371968"}
← {"success":true,"entriesVO":null}  // or file metadata if found

→ POST /api/file/3/files/download_v3
  {"id":"812673121772371968"}
← {"success":true,
   "url":"https://cloud.example.com/api/oss/download?path={base64}&signature=...",
   "name":"example-book.pdf",
   "path_display":"DOCUMENT/Document/Books/example-book.pdf",
   "content_hash":"1e8f7ba58e0c0fdee0512b2f5b280a55",
   "size":8423565,
   "_downloadable":true}

→ GET /api/oss/download?path={base64}&signature=...&pathId=...
  Range: bytes=0-
← HTTP 206 (Partial Content) — file data
```

Even full downloads use `Range: bytes=0-` and get 206 responses, enabling resume support.

#### Web UI Download

**Raw file download:**
```json
→ POST /api/file/download/url
  {"id":"820492422952779776","type":0}
← {"success":true,
   "url":"https://cloud.example.com/api/oss/download?path={base64}&signature=...",
   "md5":"c9723f1e534ea6cc164fa6da3b817ede"}
```

`type: 0` = raw download. Different from `pdfwithmark/to/pdf` (annotated PDF) and `note/to/png` (preview).

**Annotated PDF download:**
```json
→ POST /api/file/pdfwithmark/to/pdf
  {"id":"812673014813425664"}
← {"success":true,"url":"https://cloud.example.com/api/oss/download?path={base64}&signature=...&pathId=..."}
```

### File Listing

#### v3 — Device Firmware

`POST /api/file/3/files/list` with `{"path":"/","recursive":true}`

Returns the **entire file catalog** as a flat JSON array:

```json
{
  "success": true,
  "entries": [
    {"tag":"folder","id":"812654303226363904","name":"DOCUMENT","path_display":"DOCUMENT","size":0},
    {"tag":"file","id":"812673127468236800","name":"example-book.epub",
     "path_display":"DOCUMENT/Document/Books/example-book.epub",
     "content_hash":"4cb677c108293e592b5b09c7f5847816","size":1907667,
     "lastUpdateTime":1771564372000,"parent_path":"/DOCUMENT/Document/Books",
     "_downloadable":true}
  ]
}
```

#### v2 — Partner App

`POST /api/file/2/files/list_folder` — same response format as v3.

#### Web UI

| Endpoint | Purpose |
|---|---|
| `POST /api/file/list/query` | File listing by folder (paginated browsing) |
| `POST /api/file/path/query` | Resolve folder ID to path breadcrumb |
| `POST /api/file/folder/list/query` | List folders containing a file (for move dialog) |

### Move / Rename

#### Device — `POST /api/file/3/files/move_v3`

Move and rename use the **same endpoint**. The `to_path` field determines the result.

```
1. POST /api/file/3/files/query_v3          — verify source exists
   {"equipmentNo":"SN078C10034074","id":"819922212654940160"}

2. POST /api/file/3/files/query/by/path_v3  — verify destination doesn't exist
   {"path":"/NOTE/Note/Subfolder/example-note.note","equipmentNo":"SN078C10034074"}

3. POST /api/file/3/files/move_v3           — execute move/rename
   {"to_path":"/NOTE/Note/Subfolder/example-note.note",
    "equipmentNo":"SN078C10034074","id":"819922212654940160","autorename":false}
← {"success":true,"entriesVO":{"tag":"file","id":"...","name":"...","path_display":"...",
   "content_hash":"...","size":239886,"lastUpdateTime":1773292688000,"_downloadable":true}}
```

The `autorename` flag (always `false` in observed traffic) presumably handles filename collisions if set to `true`.

DB audit: Move = action `R` (relocate) with `path` and `new_path`. Rename = action `M` (modify) with `file_name` and `new_file_name`.

#### Web UI — Separate Endpoints

**Move:** `POST /api/file/move`
```json
→ {"directoryId":"812654303301861376","goDirectoryId":"819918630668992512",
   "idList":["819921481638084608"]}
← {"success":true}
```

Moves one or more files (by ID) from `directoryId` to `goDirectoryId`.

**Rename:** `POST /api/file/rename`
```json
→ {"newName":"renamed-note.note","id":"820280574034837504"}
← {"success":true}
```

### Copy (Web UI Only)

`POST /api/file/copy`
```json
→ {"directoryId":"812654303301861376","goDirectoryId":"820491861012512768",
   "idList":["820280574034837504"]}
← {"success":true}
```

Same shape as `file/move`. The server creates a new copy with a new file ID. Recorded in `f_file_action` as action `C`. Not observed on the device — may be web-UI-only.

### Delete

#### Device — `POST /api/file/3/files/delete_folder_v3`

Two-step: query then delete.

```json
→ POST /api/file/3/files/query_v3
  {"equipmentNo":"...","id":"<snowflake_id>"}
← full file metadata (tag, name, path_display, content_hash, size)

→ POST /api/file/3/files/delete_folder_v3
  {"equipmentNo":"...","id":"<snowflake_id>"}
← {"metadata":{"tag":"file","id":"...","name":"...","path_display":"..."}}
```

Despite the endpoint name `delete_folder_v3`, it works for both files and folders. The server performs a **hard delete** — removed from `f_user_file`, not soft-deleted. Note locking (password protection) does not prevent deletion.

#### Web UI — `POST /api/file/delete`

```json
→ {"idList":["820494974054301696","820494914730065920"],"directoryId":"812654303301861376"}
← {"success":true}
```

Supports bulk deletion (multiple IDs in one call). After web deletion, the device's next sync calls `query_v3` for each deleted file and gets `entriesVO: null`.

### Folder Creation

#### Device

Folders are uploaded as file entries with `is_folder=Y` in `f_user_file`. Appears as a small upload (~32KB) to the parent path.

#### Web UI — `POST /api/file/folder/add`

```json
→ {"fileName":"New Folder","directoryId":"812662700021645312"}
← {"success":true}
```

### Sync Locking

Used by the Partner app (v2 API). Not observed during device firmware syncs.

```
POST /api/file/2/files/synchronous/start  → {"synType":true}
  ... sync operations ...
POST /api/file/2/files/synchronous/end    → {"flag":"Y"}
```

May be used for transactional consistency or to signal the server to hold off on pushing changes during a sync.

### Sync Diff Algorithm

After uploading, the device performs a full metadata sync:

1. **Sync type check** — `{"equipmentNo":"SN078C10034074"}` → `{"synType":true}`
2. **Summary (digest) groups** — Fetches all digest group metadata
3. **Summary items** — Fetches all individual digest entries (md5Hash, handwriteMd5, etc.)
4. **Schedule tasks** — Fetches all task groups
5. **Full recursive file listing** — `POST /api/file/3/files/list` with `{"path":"/","recursive":true}`

Step 5 returns the **entire file catalog**. The device compares this against its local state to determine what needs uploading or downloading. This is the "scanning" behavior visible on the device.

**Not bandwidth-intensive** (the JSON is compact) but O(n) in total files.

### Note Compaction Behavior

When a new note is created, previously-synced notes sometimes shrink dramatically (e.g., 8.3MB to 961KB). The `f_file_action` table shows multiple uploads of the same note with decreasing sizes and different MD5 hashes at the same timestamp. This appears to be the device re-uploading after compacting — possibly stripping undo history, flattening layers, or compressing RATTA_RLE data.

Example from `f_file_action`:
```
example-meeting-notes.note  A  16134230  2026-03-12 15:14:40.000
example-meeting-notes.note  A   5662419  2026-03-12 15:14:40.000
example-meeting-notes.note  A    968348  2026-03-12 15:14:40.703
```

Same file, same timestamp (within 1 second), three uploads: 16.1MB to 5.6MB to 968KB. Compaction may only trigger under specific conditions (e.g., low storage, full sync after restart).

### General Observations

- **No delta/incremental sync**: Files are uploaded whole. Editing a note re-uploads the entire `.note` file.
- **Content-hash based dedup**: The `content_hash` (MD5) is used to detect unchanged files during sync.
- **S3-like design**: The upload protocol (signed URLs, multipart, `xamzDate`) was designed for S3-compatible storage. The private cloud uses local disk but kept the same API shape.
- **No compression**: Files are uploaded as-is, no gzip/deflate on the wire.
- **Full tree sync is O(n)**: The recursive file listing returns every file on the server.

---

## 5. Task Sync API

Tasks are synced as structured data via dedicated REST endpoints, NOT as file uploads.

### Sync Flow

**Step 1: Fetch all task groups** — `POST /api/file/schedule/group/all`
```json
← {"success":true,"scheduleTaskGroup":[]}
```

**Step 2: Fetch all tasks** — `POST /api/file/schedule/task/all`
```json
← {"success":true,
   "nextSyncToken":1773293270969,
   "scheduleTask":[
     {"taskId":"2ce75593...","title":"Example task","status":"needsAction",
      "dueTime":1773544273649,"links":"<base64>","isDeleted":"N","sort":1,...}
   ]}
```

Returns all tasks with a `nextSyncToken` for incremental sync.

**Step 3: Create new task** — `POST /api/file/schedule/task`
```json
→ {"taskId":"18ca36bf4ce3b0a71e71e4b201c1b894",
   "title":"Example task",
   "status":"needsAction",
   "dueTime":1773466064705,
   "completedTime":1773293267351,
   "lastModified":1773293267351,
   "isReminderOn":"N",
   "isDeleted":"N",
   "links":"<base64>",
   "sort":0,"sortCompleted":0,"sortTime":1773293267351,
   "planerSort":0,"planerSortTime":1773293267351}

← {"success":true,"taskId":"18ca36bf4ce3b0a71e71e4b201c1b894"}
```

The `taskId` is an **MD5 hash** generated client-side (not a snowflake ID).

**Step 4: Bulk update existing tasks** — `PUT /api/file/schedule/task/list`
```json
→ {"updateScheduleTaskList":[
     {"taskId":"534b0bdc...","title":"...","sort":1,"planerSort":2,...},
     {"taskId":"2ce75593...","title":"Example task","sort":2,"planerSort":1,...}
   ]}
← {"success":true}
```

After creating a new task, the device re-uploads all existing tasks with updated sort positions.

### Task Field Reference

| Field | Value | Notes |
|---|---|---|
| `task_id` | `2ce75593c0bcdc2746e82a299dec9968` | MD5-style hash, client-generated |
| `title` | Text | Recognized handwriting or typed text |
| `detail` | NULL | Description body (always null in observed traffic) |
| `status` | `needsAction` / `completed` | Active or completed |
| `due_time` | ms UTC timestamp | End-of-day in user's timezone |
| `completed_time` | ms UTC timestamp | **Misleading**: set to creation time, not actual completion time |
| `last_modified` | ms UTC timestamp | True completion time when status=completed |
| `recurrence` | NULL | Always null. `t_schedule_recur_task` table always empty. May be vestigial. |
| `importance` | NULL | Priority (not used in observed traffic) |
| `task_list_id` | NULL | Task group (null = Inbox) |
| `links` | base64 JSON | Bidirectional link to source note page |
| `isReminderOn` | `"Y"` / `"N"` | Reminder flag |
| `isDeleted` | `"N"` / `"Y"` | Soft delete flag |
| `sort` | integer | Sort position in active list |
| `sortCompleted` | integer | Sort position in completed list |
| `planerSort` | integer | Sort position in planner view |

### Task Links (Base64 JSON)

```json
{
    "appName": "note",
    "fileId": "F2026031122291380958659R7GmUC9vfx",
    "filePath": "/storage/emulated/0/Note/example-note.note",
    "page": 1,
    "pageId": "P20260311222913815035au2jySWCy4ZC"
}
```

- `fileId` matches the `FILE_ID` tag inside the `.note` file (not the `f_user_file` snowflake ID)
- `pageId` matches the `PAGEID` tag for that page
- `filePath` is the on-device Android filesystem path (`/storage/emulated/0/...`)
- `page` is 1-indexed

### CalDAV VTODO Mapping

| Task field | VTODO equivalent | Notes |
|---|---|---|
| `taskId` | `UID` | MD5 hash, client-generated |
| `title` | `SUMMARY` | |
| `detail` | `DESCRIPTION` | Always null in tests |
| `status` | `STATUS` | `needsAction` → `NEEDS-ACTION`, `completed` → `COMPLETED` |
| `dueTime` | `DUE` | ms UTC timestamp (divide by 1000) |
| `completedTime` | — | Do NOT map to `COMPLETED` (it's the creation time) |
| `lastModified` | `LAST-MODIFIED` / `COMPLETED` | Use as `COMPLETED` when status=completed (divide by 1000) |
| `isReminderOn` | `VALARM` | "Y"/"N" |
| `recurrence` | `RRULE` | Always null; pass through if set |
| `importance` | `PRIORITY` | null in tests |
| `links` | `URL` or `ATTACH` | Base64 JSON with note back-link |
| `isDeleted` | — | Soft delete flag, no VTODO equivalent |
| `sort`, `planerSort` | — | UI ordering, no VTODO equivalent |

### Link Repair After Rename

When a note is renamed, tasks that link to it have stale `filePath` values. The device handles this gracefully:

1. Device detects broken link and shows a "search results" panel listing renamed/copied files
2. User selects the correct file and hits "Replace"
3. Device updates the task's `links` field with the new path

The `fileId` and `pageId` are **unchanged** — the device matches on these internal IDs (stable across renames and copies) and updates only the `filePath`.

Before repair:
```json
{"appName":"note","fileId":"F20260312220131752455AYTcdQK7whsi",
 "filePath":"/storage/emulated/0/Note/20260312_220105 Original Name.note",
 "page":1,"pageId":"P20260312220131758123iqELvAhAHkiQ"}
```

After repair:
```json
{"appName":"note","fileId":"F20260312220131752455AYTcdQK7whsi",
 "filePath":"/storage/emulated/0/Note/20260312_220105 Renamed Note.note",
 "page":1,"pageId":"P20260312220131758123iqELvAhAHkiQ"}
```

### Completion Protocol

Uses `PUT /api/file/schedule/task/list` bulk update. Key field changes:
- `status`: `"needsAction"` → `"completed"`
- `sortCompleted`: Gets an ordering value
- `lastModified`: Updated to completion time
- `completedTime`: Does NOT change (retains creation timestamp)

For CalDAV: use `lastModified` (not `completedTime`) as `COMPLETED` when `status` = `"completed"`.

### Deletion Protocol

`DELETE /api/file/schedule/task/{taskId}`

```
DELETE /api/file/schedule/task/2ce75593c0bcdc2746e82a299dec9968
← {"success":true}
```

**Hard delete** — task removed from `t_schedule_task` entirely. Sent by a different HTTP client (`okhttp/4.8.0`) on a delayed schedule, suggesting the to-do subsystem runs its own sync loop.

### Task Groups

Fetched via `POST /api/file/schedule/group/all`. Returns `{"scheduleTaskGroup":[]}`. Task groups are organizational containers. Tasks without a group (`task_list_id: null`) are in the Inbox.

### Cross-Device Task Lifecycle

```
Device A creates task:
  POST /api/file/schedule/task
  {"taskId": "993d972ff0665a338edf94b36ebfb231",
   "title": "example task",
   "status": "needsAction",
   "dueTime": 1773979200000,
   "completedTime": 0,
   "lastModified": 1773334235314}

Device B picks up task:
  POST /api/file/schedule/task/all → returns all tasks including the new one

Device B completes it:
  PUT /api/file/schedule/task/list
  {"updateScheduleTaskList": [{
    "taskId": "993d972ff0665a338edf94b36ebfb231",
    "status": "completed",
    "completedTime": 1773334721520,
    "lastModified": 1773334721520,
    "sortCompleted": 2
  }]}

Device A refreshes:
  POST /api/file/schedule/task/all → sees status: "completed"
```

The server is a **dumb store** — it does not validate completion logic or enforce state transitions.

---

## 6. Digest (Summary) Sync API

Digests are synced as structured data via dedicated REST endpoints with a create-then-update pattern.

### Record Types

| `is_summary_group` | Purpose |
|---|---|
| `Y` | **Category/group** — container with a `name`, no content |
| `N` | **Digest item** — extracted text with source reference and optional annotations |

### Linking Model

- Groups have a `unique_identifier` (MD5-format hash)
- Items point to their group via `parent_unique_identifier`
- Items with `parent_unique_identifier = NULL` or empty string are uncategorized

### Creation Flow

**Step 1: Create digest entry** — `POST /api/file/add/summary`
```json
→ {"content":"Example highlighted text",
   "creationTime":1773293315777,
   "lastModifiedTime":1773293315777,
   "md5Hash":"ca055a657650fc93c1b2927e1400eb56",
   "metadata":"{\"author\":\"Author Name\",\"note_page\":\"1\",\"note_fileId\":\"F20260312012642265681qXTsFTugCLR0\",\"note_pageId\":\"P20260312012642270515uC3KyanT22qD\",\"unique_identifier\":\"56672a0bcc5e4955552e2e45b18c0dfc\"}",
   "parentUniqueIdentifier":"d3f4db0e87cc6c3d693aafb28defc489",
   "sourcePath":"Note/example-note.note",
   "sourceType":2,
   "commentHandwriteName":"",
   "commentStr":"",
   "handwriteMD5":""}

← {"success":true,"id":819924853086748672}
```

The `id` returned is a snowflake ID (server-generated, unlike task IDs).

**Step 2: Upload handwriting annotation** — `POST /api/file/upload/apply/summary`

Returns a signed URL pointing to `/home/supernote/data//digest/` with an `innerName` like `2eedb233d3937b0336121304effef635.mark`. The `.mark` file is uploaded via `POST /api/oss/upload`.

```json
← {"fullUploadUrl":"https://cloud.example.com/api/oss/upload?...&path=L2hvbWUvc3VwZXJub3RlL2RhdGEvL2RpZ2VzdC8",
   "innerName":"2eedb233d3937b0336121304effef635.mark"}
```

The base64 path decodes to `/home/supernote/data//digest/` — note the double slash (minor path construction bug). Digest handwriting goes to `sndata/digest/`, not `supernote_data/`.

**Step 3: Update digest with handwriting info** — `PUT /api/file/update/summary`
```json
→ {"id":819924853086748672,
   "content":"Example highlighted text",
   "commentStr":"Typed annotation",
   "commentHandwriteName":"2eedb233d3937b0336121304effef635.mark",
   "handwriteInnerName":"2eedb233d3937b0336121304effef635.mark",
   "handwriteMD5":"afdba7597b7aa2a989a6b4140d21e5fa",
   "md5Hash":"a183a563a0262ccb62fec08c3416f2d9",
   "lastModifiedTime":1773293348128,
   "metadata":"{...}",
   "parentUniqueIdentifier":"d3f4db0e87cc6c3d693aafb28defc489",
   "sourcePath":"Note/example-note.note",
   "sourceType":2}
← {"success":true}
```

### Digest Item Fields

| Field | Purpose |
|---|---|
| `content` | The highlighted/bracketed text |
| `source_path` | Source document path (relative) |
| `source_type` | `1` = document (PDF/epub), `2` = note |
| `metadata` | JSON with precise location (see below) |
| `comment_str` | Typed text annotation |
| `comment_handwrite_name` | Filename of handwritten annotation `.mark` file |
| `handwrite_inner_name` | Server-side name for handwriting file (same as above) |
| `handwrite_md5` | Checksum of handwriting data |
| `md5Hash` | MD5 of the overall digest entry (changes on update) |
| `parent_unique_identifier` | Links to digest group UUID |
| `is_deleted` | `Y` = soft deleted (retained for sync propagation) |
| `file_id` | Always NULL — source link is via `source_path`, not foreign key |

### Three Annotation Layers

A single digest can have up to three layers:
1. **Extracted text** (`content`) — the selected text from the source document
2. **Typed annotation** (`comment_str`) — keyboard-entered commentary
3. **Handwritten annotation** (`comment_handwrite_name`) — stored in a `.mark` file

### Document Digests vs Note Digests

| Aspect | Note Digest | Document Digest |
|---|---|---|
| `sourceType` | `2` | `1` |
| Page addressing | `note_page` (1-indexed) | `page` (0-indexed) |
| ID references | `note_fileId`, `note_pageId` | None (uses `source_size` + path) |
| Chapter support | No | Yes (for epubs) |
| Position data | No | `startPosition`, `endPosition` |

#### Note Digest Metadata

```json
{
  "author": "Author Name",
  "note_page": "1",
  "note_fileId": "F20260312012642265681qXTsFTugCLR0",
  "note_pageId": "P20260312012642270515uC3KyanT22qD",
  "unique_identifier": "56672a0bcc5e4955552e2e45b18c0dfc"
}
```

#### Document Digest Metadata

```json
{
  "document_location_data": "[{\"chapter\":2,\"endPosition\":995,\"page\":0,\"startPosition\":940}]",
  "source_size": 3344196,
  "unique_identifier": "f6444dce52bc2460c932d42aba0f7133",
  "author": "Author Name"
}
```

- `document_location_data`: JSON array (stringified) with `chapter`, `startPosition`, `endPosition`, `page`
- `source_size`: File size in bytes of the source document

**Addressing model by document type:**

| Document type | `chapter` field | `page` field | Addressing model |
|---|---|---|---|
| epub | Chapter index (1+) | Always 0 | By chapter |
| PDF | Always 0 | Page number (0-indexed) | By page |

`startPosition` and `endPosition` are character offsets within the addressed unit.

#### Document Digest Creation Example

```json
→ POST /api/file/add/summary
{
  "content": "Example text selected from a document",
  "sourcePath": "Document/Books/example-book.epub",
  "sourceType": 1,
  "metadata": "{\"document_location_data\":\"[{\\\"chapter\\\":2,\\\"endPosition\\\":995,\\\"page\\\":0,\\\"startPosition\\\":940}]\",\"source_size\":3344196,\"unique_identifier\":\"f6444dce52bc2460c932d42aba0f7133\"}",
  "parentUniqueIdentifier": "",
  "creationTime": 1773293993856,
  "lastModifiedTime": 1773293993856,
  "md5Hash": "945db7e72f3aba4783b74f8d38c1a82e",
  "commentHandwriteName": "",
  "commentStr": "",
  "handwriteMD5": ""
}

← {"success":true,"id":819927694266335232}
```

Note: `parentUniqueIdentifier` is empty string (no category assigned initially). When the user assigns a category, it gets set via `PUT /api/file/update/summary`.

### `.mark` Sidecar Files

When annotating a document (PDF/epub), the device creates a `.mark` file:
- Named `<original_filename>.mark` (e.g., `example-book.pdf.mark`)
- Tracked in `f_user_file` as a regular file alongside the source document
- Contains all handwritten annotation layers for that document
- Individual digest handwritten annotations reference specific content by MD5 hash

**Storage locations for annotation data:**

| Annotation type | Storage location | Filename pattern |
|---|---|---|
| Document sidecar (pen strokes, stars, highlights) | `supernote_data/.../filename.epub.mark` | Same as document + `.mark` |
| Digest handwriting (note digests) | `sndata/digest/` | `{md5}.mark` |
| Digest handwriting (document digests) | `sndata/digest/` | `{md5}.mark` |

Stars on documents are stored in the `.mark` sidecar file — there is no separate API call for star creation.

### Progressive Update Pattern

The device sends multiple `PUT /api/file/update/summary` calls as the user fills in fields. Each call sends the full record. Observed sequence:

1. Create — empty handwriting, empty comment, no category
2. Update — handwriting `.mark` uploaded, `commentHandwriteName` set
3. Update — typed comment added (`commentStr`)
4. Update — category assigned (`parentUniqueIdentifier` set), author changed

### Category Move

Moving a digest to a different category uses the same `PUT /api/file/update/summary` endpoint. The `parentUniqueIdentifier` field is changed to the target category's UUID. The full record is sent:

```json
→ PUT /api/file/update/summary
{
  "id": 819894265432768512,
  "parentUniqueIdentifier": "d3f4db0e87cc6c3d693aafb28defc489",
  "content": "Example highlighted text from a document...",
  "sourcePath": "Document/Books/example-book.pdf",
  "sourceType": 1,
  "commentHandwriteName": "747fbdfa656fa6c5397e120f9c231283.mark",
  "handwriteInnerName": "747fbdfa656fa6c5397e120f9c231283.mark",
  "handwriteMD5": "2584073c11a37b283b003a214ec98088",
  "lastModifiedTime": 1773334571606,
  "md5Hash": "7455e428dd7aba416d740b80b50f0e6e",
  ...
}
```

### Querying Digests

| Endpoint | Purpose |
|---|---|
| `POST /api/file/query/summary/group` | Fetch all digest category groups |
| `POST /api/file/query/summary/id` | Fetch digest items by ID list |
| `POST /api/file/query/summary/hash` | Fetch digest items by hash (change detection) |

### Sync Session Bracketing

Document syncs are wrapped in explicit session markers:

```
POST /api/file/2/files/synchronous/start  → {"synType":true}
  ... sync operations ...
POST /api/file/2/files/synchronous/end    → {"flag":"Y"}
```

### Document Annotation Sidecar Upload

When annotations are made on a document, the device uploads a `.mark` sidecar file as a **normal file** alongside the document using the standard 4-step file upload protocol. This goes to `supernote_data/` alongside the document. This is distinct from digest handwriting annotations, which go to `sndata/digest/`.

### Observations

- Deleted digests are soft-deleted (`is_deleted: Y`) and still synced so other devices know to remove their local copy
- A failed bracket gesture still creates a record, just immediately soft-deleted
- Pages are zero-indexed in document digest metadata (page 3 in JSON = 4th page visually)
- Creating digests on a PDF triggers creation of a `.mark` sidecar file

---

## 7. Real-Time Sync (Socket.io)

### Connection

- The auto-sync daemon (`okhttp/3.12.12`) maintains a persistent WebSocket connection to port 18072
- Connection upgrades via `GET /socket.io/` → HTTP 101 (Switching Protocols)
- Connections stay alive for 1-10+ minutes, then reconnect
- EIO=3 (Engine.IO protocol version 3)
- Proxied through nginx on the same external port as the rest of the API

### Connection Parameters

```
GET /socket.io/?sign={hmac}&random={timestamp}&EIO=3&transport=websocket&type={equipmentNo}&token={jwt}
```

The `channel` header value = JWT + `_` + equipmentNo + `_` + timestamp — used for channel identification.

### Protocol Messages

Standard Engine.IO framing:

| Prefix | Type |
|---|---|
| `0{...}` | Session open (server → client, returns `sid`, `pingInterval`, `pingTimeout`) |
| `2` | Ping (server → client) |
| `3` | Pong (client → server) |
| `40` | Socket.IO connect |
| `42[...]` | Socket.IO event (the actual application messages) |
| `43[...]` | Socket.IO ack |

### Keepalive

- Server sends `42["ratta_ping"]` every ~5 seconds
- Device responds `42["ratta_ping","Received"]`

### Server Push Notifications (FILE-SYN)

**The server actively pushes sync notifications to connected devices.** When a file operation occurs (from the web UI, another device, or the Partner app), the server sends a `ServerMessage` event with a `FILE-SYN` payload:

```json
42["ServerMessage","{
  \"code\": \"200\",
  \"timestamp\": 1773428590835,
  \"msgType\": \"FILE-SYN\",
  \"data\": [{
    \"messageType\": \"MODIFYFILE\",
    \"equipmentNo\": \"null-null\",
    \"fileType\": \"FILE\",
    \"id\": 820280574034837504,
    \"originalName\": \"example-note.note\",
    \"newName\": \"renamed-note.note\",
    \"md5\": \"c9723f1e534ea6cc164fa6da3b817ede\",
    \"size\": 1310298,
    \"directoryId\": 812654303301861376,
    \"originalPath\": \"NOTE/Note/\",
    \"newPath\": \"NOTE/Note/\",
    \"timestamp\": 1773428586842
  }]
}"]
```

The device acknowledges with `42["ClientMessage","Received"]`.

#### Observed `messageType` Values

| `messageType` | Trigger | Fields |
|---|---|---|
| `MODIFYFILE` | File renamed (web UI) | `id`, `originalName`, `newName`, `md5`, `size`, `directoryId`, `originalPath`, `newPath` |
| `COPYFILE` | File copied (web UI) | Same as MODIFYFILE plus `newId`, `goDirectoryId` |
| `STARTSYNC` | Another device started syncing | `equipmentNo` (of the syncing device), `timestamp` |

Note: `equipmentNo` is `"null-null"` for web UI operations (the web UI has no equipment number).

#### Implications for Third-Party Integration

This is critical for building services (e.g., CalDAV bridges) that need to trigger device syncs after making changes:

1. **A sidecar service can connect to the socket.io endpoint** on port 18072 (proxied through nginx) as if it were another client
2. **After writing changes to the DB** (e.g., creating a task via CalDAV), it can send a `FILE-SYN` / `STARTSYNC` message
3. **The device receives the push** and initiates a sync, picking up the changes immediately

This means server-side changes do NOT have to wait for the device's next poll cycle — push-triggered sync is fully supported by the existing protocol. The socket.io service is accessible from outside the container via the standard nginx proxy.

---

## 8. System Configuration

### `/api/official/system/base/param`

`POST` with empty body `{}`. **No authentication required** — the web UI calls this before login.

| Parameter | Value | Meaning |
|---|---|---|
| `COPY_MAX` | `1000` | Max files per copy operation |
| `DOWNLOAD_MAX_NUMBER` | `50` | Max concurrent downloads |
| `EMAIL_CODE_TIME` | `5,5` | Email verification code: 5 min expiry, 5 max attempts |
| `FILE_MAX` | `1073741824` | Max single file size: 1 GB |
| `FILE_TYPE` | (see below) | Allowed file extensions for upload |
| `MAX_ERR_COUNTS` | `6` | Login lockout after 6 failed password attempts |
| `UPLOAD_MAX` | `500` | Max concurrent uploads |

The response also includes `"random": "SN100"` — likely a server version or instance identifier.

**Allowed file types** (from `FILE_TYPE`):
`otf, ttf, mark, note, read, epub, zz, tar.gz, ttc, eot, snstk, snbak, webp, spd, cbz, fb2, xps, dfont, woff, gz, apk, ppt, tif, tga, psd, jpeg, bmp, gif, png, jpg, doc, txt, rar, zip, xlsx, docx, pptx, rtf, chm, pdf, xls`

Notable entries:
- `.apk` — Android APK files can be synced through the cloud
- `.snstk`, `.snbak` — Supernote sticker packs and device backups
- `.read` — unknown format, possibly reading progress/state data
- `.mark` — handwriting annotation sidecar files
- `.spd` — unknown Supernote-specific format
- `.zz` — unknown compressed format
- Font files (`otf, ttf, ttc, eot, dfont, woff`) — custom font sync
- Comic book archives (`.cbz`), FictionBook (`.fb2`), XPS — additional document formats

Only `POST` works; `GET` returns HTTP 500.

### `b_reference` Table Contents

48 rows including:
- **File type whitelist**: 40+ entries mapping extensions to MIME types
- **Limits**: as listed above
- **Email**: `EMAIL_CODE_TIME=5` (verification code validity in minutes)
- **Operators**: entries created by Ratta dev team members

---

## 9. Client Behavior Reference

### Device Firmware HTTP Client Inventory

The device runs at least 4 distinct HTTP clients:

| User Agent | Role | Observed Operations |
|---|---|---|
| `okhttp/5.1.0` | Primary file sync | Upload, download, file delete, task bulk update |
| `okhttp/4.12.0` | Metadata queries | `user/query`, `file/query/server` |
| `okhttp/4.8.0` | Task deletion | `DELETE /api/file/schedule/task/{id}` |
| `okhttp/3.12.12` | Auto-sync daemon | WebSocket keepalive (`socket.io`) |

The different okhttp versions suggest the firmware bundles multiple Android services/libraries built at different times.

### Partner App (Dart/3.8)

The Supernote Partner mobile app is a Flutter/Dart client — separate codebase from the device firmware.

| Aspect | Device (okhttp) | Partner App (Dart/3.8) |
|---|---|---|
| File listing | `POST /api/file/3/files/list` (v3) | `POST /api/file/2/files/list_folder` (v2) |
| Sync locking | Not observed | `synchronous/start` → operations → `synchronous/end` |
| Heartbeat | Socket.io keepalive | `POST /api/equipment/bind/status` (constant polling, every 3-5s) |
| Socket.io | okhttp/3.12.12, reconnects every 1-10 min | Dart/3.8, same protocol |
| Upload/Download | okhttp/5.1.0 | Dart/3.8 (same endpoints, uses Range requests) |

#### Partner App Sync Flow

```
1. POST /api/user/query                       — verify user session
2. POST /api/file/capacity/query              — check storage quota
3. POST /api/equipment/bind/status            — heartbeat (repeated constantly)
4. POST /api/file/query/summary/group         — fetch digest categories
5. POST /api/file/query/summary/id            — fetch digest items by ID
6. POST /api/file/query/summary/hash          — fetch digest hashes for diff
7. POST /api/file/2/files/synchronous/start   — lock sync session
8. POST /api/file/2/files/list_folder         — full recursive file listing
9. POST /api/file/2/files/synchronous/end     — release sync lock
10. POST /api/file/schedule/group/all         — fetch task groups
11. POST /api/file/schedule/task/all          — fetch all tasks
12. GET  /socket.io/                          — establish WebSocket connection
```

#### Multi-Device Identity

Each device gets a unique `equipmentNo`:
- E-ink tablets: `SN078C10034074` (hardware serial)
- Android devices (Partner app): `ANDROID-{uuid}` (e.g., `ANDROID-c077360c-31f3-4c58-9604-fa78d8e2988b`)

All authenticate as the same `userId` with separate JWT tokens. The `equipmentNo` is passed in every sync call so the server knows which device is talking.

#### Second Device File Queries

When a new device syncs, it queries `query_v3` for file IDs that may return `entriesVO: null`. These are files the device has **locally** that were never uploaded to this server. The device checks if the server knows about them before deciding whether to upload. The `equipmentNo` scoping means different devices can have different file catalogs on the server.

### Web UI (Vue.js)

Tech stack:
- **Vue.js** SPA (code-split: `0.js` through `19.js`, `app.js`, `about.js`, etc.)
- **Element UI** (UI component library)
- **Font Awesome** (icons)
- **spark-md5** (client-side MD5 hashing for file dedup)
- **jsencrypt.min.js** (RSA encryption library — loaded but not used in observed login flow)
- **crypto-js** (AES/hashing)
- **ufile-token.js** (upload token management — suggests original cloud used UCloud's UFile storage)

Served by Node.js (port 9888) inside the supernote-service container.

### What's NOT Synced

| Feature | Synced? | Details |
|---|---|---|
| Reading progress/bookmarks | **No** | Stored locally on each device only |
| Favorites/stars on notes | **No** | Favoriting does not trigger any API call |
| Calendar events | **No** | Syncs directly to Google/Microsoft via OAuth |

Favoriting a note stores metadata in `.note` file `PAGETAG` entries, so it may sync incidentally when the file is re-uploaded for other reasons.

### Word Document Application

The device has a built-in "Word doc" app for creating `.doc` files (actual Microsoft `.doc` format, not `.docx`) with handwriting:
- Files created at `/NOTE/Note/{timestamp}.doc`
- Small size: 7,680-9,728 bytes for simple handwritten content
- Synced via the standard upload protocol
- May exhibit compaction behavior similar to notes

### Note Export (Device-Local)

Exporting a note (to PNG, PDF, TXT, DOCX) is **entirely device-side**. The `notelib` container is NOT involved:
- Device performs the conversion locally
- Uploads results to `/EXPORT/` via standard upload protocol
- No conversion API calls, no `f_terminal_file_convert` entries

The `notelib` container and `f_terminal_file_convert` table are for **server-initiated conversions** — when conversion is requested through the web UI (e.g., `POST /api/file/note/to/png` for preview).

WebDAV uploads also use the standard file sync protocol — from the server's perspective they are indistinguishable from any other file upload.

### Note Preview via Web UI (notelib)

```json
→ POST /api/file/note/to/png
  {"id":"819924630713139200"}
← {"success":true,"pngPageVOList":[
    {"pageNo":1,"url":"https://cloud.example.com/api/oss/download?path={base64-of-/home/supernote/convert/userId_fileId_size_hash_pageNo.png}&signature=..."}
  ]}
```

PNG filename format: `{userId}_{fileId}_{size}_{hash}_{pageNo}.png`, stored in `/home/supernote/convert/`.

**Important**: This conversion fails if the `X-Forwarded-Port` header is wrong (see section 11), because the returned download URL will have the wrong port.

### Email Architecture

The device's built-in IMAP email client connects **directly to mail servers** — zero private cloud involvement. No email traffic observed during device email operations.

The `u_email_config` table stores **server-side SMTP credentials** for transactional emails (account registration, password reset), not user email credentials. The `/api/query/email/config` endpoint serves this config during registration flows.

---

## 10. `.note` File Format

### Overview

Binary format with readable metadata tags. Magic bytes: `noteSN_FILE_VER_20230015`.

Metadata is stored as angle-bracket tags: `<KEY:value>`. The file is structured as a header followed by per-page data blocks.

### File Header Tags

| Tag | Example Value | Purpose |
|---|---|---|
| `FILE_TYPE` | `NOTE` | File type identifier |
| `APPLY_EQUIPMENT` | `N6` | Device model (N6 = Nomad) |
| `FILE_ID` | `F2026031122291380958659R7GmUC9vfx` | Unique file identifier |
| `FILE_RECOGN_TYPE` | `1` | 1 = real-time recognition enabled, 0 = standard note |
| `FILE_RECOGN_LANGUAGE` | `en_US` | Recognition language |
| `FINALOPERATION_PAGE` | `1` | Last viewed page |
| `FINALOPERATION_LAYER` | `1` | Last active layer |

### Per-Page Structure

Each page contains:

| Tag | Purpose |
|---|---|
| `PAGESTYLE` | Template name (e.g., `style_8mm_ruled_line`) |
| `PAGEID` | Unique page ID (e.g., `P20260311222913815035au2jySWCy4ZC`) |
| `LAYERINFO` | JSON array of layer definitions (uses `#` instead of `:` as separator) |
| `LAYERSEQ` | Layer ordering (e.g., `MAINLAYER,BGLAYER`) |
| `MAINLAYER` | Offset to main handwriting layer data |
| `BGLAYER` | Offset to background/template layer data |
| `TOTALPATH` | Offset to stroke path data |
| `RECOGNSTATUS` | Whether recognition has been performed |
| `RECOGNTEXT` | Offset to recognized text blob |
| `RECOGNFILE` | Offset to MyScript iink recognition data |
| `PAGETEXTBOX` | Count of text boxes on this page |
| `DISABLE` | Text box bounding rectangles (x,y,w,h) |
| `ORIENTATION` | Page orientation |
| `EXTERNALLINKINFO` | Offset to link data (0 = no links) |

### Layer Data

- **Protocol**: `RATTA_RLE` — custom run-length encoding for ink/bitmap data
- **Layer types**: `MAINLAYER` (user handwriting), `BGLAYER` (template), `Layer 1-3` (additional layers)
- **Layer JSON** uses `#` instead of `:` as key-value separator: `{"layerId"#0,"name"#"Main Layer",...}`
- Older files may use `SN_ASA_COMPRESS` protocol (zlib-compressed) or embed layers as PNG

**Important**: RATTA_RLE encodes **rasterized bitmaps**, not vector strokes. The layers contain pre-rendered pixel data, not pen coordinates or pressure data. Actual stroke vectors live in the MyScript iink data (`RECOGNFILE`).

#### RATTA_RLE Encoding (decoded by [pysn-digest/supernote-tool](https://gitlab.com/mmujynya/pysn-digest))

The encoding is a byte-pair RLE scheme:

```
[colorcode, length] [colorcode, length] ...
```

**Color codes:**

| Code | Meaning | X2-series Code |
|---|---|---|
| `0x61` | Black | `0x61` (same) |
| `0x62` | Background/transparent | `0x62` (same) |
| `0x63` | Dark gray | `0x9D` |
| `0x64` | Gray | `0xC9` |
| `0x65` | White | `0x65` (same) |
| `0x66` | Marker black | `0x66` (same) |
| `0x67` | Marker dark gray | `0x9E` |
| `0x68` | Marker gray | `0xCA` |

**Length decoding:**
- `length < 0x80`: run of `length + 1` pixels
- `length == 0xFF`: run of 16,384 pixels (`0x4000`), or 1,024 (`0x400`) for blank pages
- `length & 0x80 != 0` (bit 7 set): multi-byte length — hold this pair and combine with the next pair: `1 + next_length + ((length & 0x7F) + 1) << 7`

**Page dimensions:**
- Standard (A6X, Nomad): 1404 x 1872 pixels
- A5X2: 1920 x 2560 pixels

**Other protocols:**
- `SN_ASA_COMPRESS`: zlib-compressed uint16 bitmap at 1404x1888, rotated 90 degrees clockwise, bottom 16 rows deleted. Color codes: `0x0000` = black, `0xFFFF` = background, `0x2104` = dark gray, `0xE1E2` = gray.
- Some layers are stored as plain **PNG**

### Recognized Text (`RECOGNTEXT`)

Base64-encoded JSON powered by **MyScript iink 3.0.3**:

```json
{
  "elements": [
    {
      "type": "Text",
      "label": "Recognized text with\nline breaks",
      "words": [
        {
          "label": "Recognized",
          "bounding-box": {"x": 10.91, "y": 9.39, "width": 11.97, "height": 10.79}
        },
        {"label": " "},
        {
          "label": "text",
          "bounding-box": {"x": 23.76, "y": 11.59, "width": 15.18, "height": 12.39}
        }
      ]
    },
    {
      "type": "Raw Content"
    }
  ]
}
```

- `type: "Text"` — recognized text block with per-word bounding boxes
- `type: "Raw Content"` — unrecognized/drawing content
- Newlines (`\n`) separate recognized lines

### Embedded MyScript iink Data (`RECOGNFILE`)

A full MyScript iink document embedded in the `.note` file:
- `meta.json` — format version, iink version (3.0.3), language, last modification date
- `rel.json` — page references and version tracking
- `index.bdom` — binary DOM structure
- Ink stroke data (binary)

### Stroke Data (`TOTALPATH`)

The `TOTALPATH` tag points to raw stroke vector data — the actual pen coordinates, pressure, and timing that make up the handwriting. This format has **not been publicly decoded**. The pysn-digest library preserves `TOTALPATH` as an opaque blob during reconstruct/merge operations but does not parse its internal structure.

This is the key distinction between the three data representations in a `.note` file:

| Data | Format | Editable? | Purpose |
|---|---|---|---|
| `MAINLAYER` / `BGLAYER` | RATTA_RLE bitmap | No (pixels) | Visual rendering of strokes |
| `TOTALPATH` | Unknown binary | Yes (on device) | Stroke vectors — enables selection, erasing, undo |
| `RECOGNFILE` | MyScript iink | No (proprietary) | Handwriting recognition engine data |
| `RECOGNTEXT` | Base64 JSON | Read-only | Recognition results (text + bounding boxes) |

**Implications for .note file creation:**

The pysn-digest library ([gitlab.com/mmujynya/pysn-digest](https://gitlab.com/mmujynya/pysn-digest), Apache 2.0) can:
- Parse `.note` files into structured objects (metadata, pages, layers, keywords, titles, links)
- Reconstruct the binary format from parsed objects (round-trip)
- Merge two notebooks into one
- Decode RATTA_RLE bitmaps to pixel data for rendering

It **cannot**:
- Generate new `TOTALPATH` data (synthesize editable strokes)
- Encode new RATTA_RLE bitmaps (no encoder, only decoder)
- Create MyScript iink `RECOGNFILE` data

To create a `.note` file from scratch that looks correct but isn't device-editable, you could inject pre-rendered RATTA_RLE bitmaps (the encoding is simple enough to reverse from the decoder). But without `TOTALPATH` data, the strokes cannot be selected, erased individually, or recognized — they'd be flat images, like screenshots pasted onto the page.

For use cases going `.note` → text/markdown (e.g., Obsidian vault export), `RECOGNTEXT` provides everything needed. For `.note` ← text (injecting content into notes), `TOTALPATH` decoding is the remaining frontier.

### Text Boxes (`PAGETEXTBOX`)

- Count in `PAGETEXTBOX` tag
- Bounding rectangle in `DISABLE` tag: `x,y,w,h|` (pipe-delimited for multiple boxes)
- Content stored as base64-encoded strings within the page data block
- Font reference also base64-encoded (e.g., `DroidSansFallbackFull.ttf`)

### Headings (NOT in DB)

Stored using `TITLE*` tags. Do NOT sync to the database.

| Tag | Purpose | Example |
|---|---|---|
| `TITLESEQNO` | Sequence number | `0` |
| `TITLELEVEL` | Heading level (always 1) | `1` |
| `TITLESTYLE` | Visual style code — **encodes the heading tier** | See below |
| `TITLERECT` | Bounding box (x, y, w, h) | `144,111,316,134` |
| `TITLERECTORI` | Original bounding box | Same as TITLERECT |
| `TITLEBITMAP` | Offset to RLE-compressed bitmap | `5037` |
| `TITLEPROTOCOL` | Compression protocol | `RATTA_RLE` |

**Heading styles (visual hierarchy):**

| Style Code | Visual Appearance |
|---|---|
| `1000254` | Dark black (top-level heading) |
| `1157254` | Dark gray (sub-heading) |
| `1201000` | Light gray (sub-sub-heading) |
| `1000000` | Diagonal hatch pattern (sub-sub-sub-heading) |

Heading content is stored as a bitmap — no text representation. To extract heading text, use OCR/vision on the bitmap, or RECOGNTEXT if the note uses real-time recognition.

### Keywords (NOT in DB)

Stored inline using `KEYWORD*` tags:

```
KEYWORDPAGE: 0              ← page index (0-based)
KEYWORDSEQNO: 0             ← sequence number on that page
KEYWORDRECT: x,y,w,h        ← bounding box of the lassoed area
KEYWORDRECTORI: x,y,w,h     ← original bounding rect
KEYWORDSITE: <offset>        ← offset to keyword string data
```

At the `KEYWORDSITE` offset:
```
[4-byte little-endian length] [keyword text as UTF-8]
```

Keywords have no visual indication in the note — they are invisible metadata attached to a page region.

### Stars (NOT in DB)

Stored as `FIVESTAR` tags containing 5 vertex coordinates:

```
FIVESTAR: x1,y1,x2,y2,x3,y3,x4,y4,x5,y5,flag
```

10 coordinates (5 x,y pairs defining star vertices) plus a trailing flag (always `1` observed).

### Links (NOT in DB)

| Tag | Purpose | Example |
|---|---|---|
| `LINKTYPE` | Target type: `1` = note, `2` = document | `1` |
| `LINKINOUT` | Direction (always 0 observed) | `0` |
| `LINKFILE` | Base64-encoded target file path | |
| `LINKFILEID` | Target file's FILE_ID (or `none` for documents) | `F2026031122291380958659R7GmUC9vfx` |
| `OBJPAGE` | Target page number (0-based) | `9` |
| `PAGEID` | Target page ID (or `none`) | |
| `LINKSTYLE` | Visual style code | `25000000` |
| `LINKTIMESTAMP` | Creation timestamp | `20260312001138313344` |
| `LINKRECT` | Bounding box on page | `114,1091,886,64` |
| `LINKBITMAP` | Offset to RLE rendering | |
| `LINKPROTOCAL` | Compression (note: typo is in the original format) | `RATTA_RLE` |

**Quirk**: Creating a task from a lassoed link uses the link target's filename as the task title, not the visible handwritten text. The task `links` field points to the note *containing* the link, not the link's target.

---

## 11. Deployment & Operations

### Reverse Proxy Requirements (X-Forwarded-Port)

The supernote-service container's internal nginx listens on port **8080**. When a reverse proxy forwards requests without setting `X-Forwarded-Port`, the internal nginx defaults to `$server_port` = 8080. The Java backend uses this to construct download/upload URLs, resulting in broken URLs like `https://cloud.example.com:8080/api/oss/download?...`.

**Root cause** — the internal nginx config:
```nginx
set $real_port $server_port;                    # 8080
if ($http_x_forwarded_port) {
    set $real_port $http_x_forwarded_port;      # override if set
}
proxy_set_header X-Forwarded-Port $real_port;   # passed to Java backend
```

**Fix**: The external reverse proxy MUST send `X-Forwarded-Port` with the frontend port (e.g., 443). Ratta's setup instructions include `proxy_set_header X-Forwarded-Port $server_port;` in the recommended nginx config.

**Affected operations**: All server-generated URLs — file downloads, file uploads, note-to-PNG preview, PDF export. Anything that returns a URL from the Java backend.

### Nginx Proxy Manager Specific Fix

NPM's default `proxy.conf` include does NOT set `X-Forwarded-Port`. Add it manually:
- In NPM's advanced config for the proxy host, OR
- By editing `/etc/nginx/conf.d/include/proxy.conf` inside the NPM container

Note: changes to `proxy.conf` inside the NPM container are lost on container restart.

### Log Rotation

The nginx access log (`sndata/logs/web/access.log`) has **no log rotation configured**. It grows unbounded — ~9.6MB over 21 days of light use. The JSON format is verbose (~500-800 bytes per request). No logrotate config exists inside the container.

Recommendation: Set up host-side logrotate with `copytruncate` on `sndata/logs/web/access.log`.

### Install Script Issues

1. **CRITICAL**: `probe_service` not found — `init_database()` never runs on fresh install. Workaround: manually import `supernotedb.sql`.
2. **MINOR**: Root ownership — run `chown -R $USER /mnt/supernote` after install.
3. **MINOR**: db_data uid mismatch — run `chown -R 999:999 sndata/db_data`.

### Search Architecture (or Lack Thereof)

There is **no server-side search index**. Handwriting search happens entirely on-device by scanning `RECOGNTEXT` blobs embedded in individual `.note` files. Consequences:

- Every search requires opening and parsing multiple `.note` files
- Search cannot work across devices without syncing all files first
- No server-side API for searching note content
- The web UI cannot search handwritten content

---

## 12. API Endpoint Reference

### Authentication & Identity

| Endpoint | Method | Auth? | Purpose |
|---|---|---|---|
| `/api/official/user/check/exists/server` | POST | No | Server existence check (returns userId, uniqueMachineId) |
| `/api/official/user/query/random/code` | POST | No | Get randomCode + timestamp for password hashing |
| `/api/official/user/account/login/equipment` | POST | No | Device login (hashed password + equipmentNo/version) |
| `/api/official/user/account/login/new` | POST | No | Web UI login (hashed password + browser/language) |
| `/api/terminal/equipment/unlink` | POST | No | Unlink device from account |
| `/api/terminal/user/bindEquipment` | POST | Yes | Bind device (name, capacity, directory labels) |
| `/api/equipment/bind/status` | POST | Yes | Device binding heartbeat/presence check |
| `/api/user/query` | POST | Yes | Get user info |
| `/api/user/query/token` | POST | Yes | Get auth token |
| `/api/query/email/publickey` | GET | No | RSA public key (vestigial) |
| `/api/query/email/config` | GET | Yes | Email SMTP configuration |

### System

| Endpoint | Method | Auth? | Purpose |
|---|---|---|---|
| `/api/official/system/base/param` | POST | No | System configuration (limits, allowed file types) |
| `/api/file/query/server` | GET | Yes | Server discovery |
| `/api/file/capacity/query` | POST | Yes | Check storage quota |

### File Sync — Device (v3)

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/file/3/files/query/by/path_v3` | POST | Check if file exists by path |
| `/api/file/3/files/query_v3` | POST | Query file metadata by ID + equipmentNo |
| `/api/file/3/files/upload/apply` | POST | Get signed upload URL |
| `/api/file/3/files/upload/confirm` | POST | Confirm upload, get file ID |
| `/api/file/3/files/download_v3` | POST | Get signed download URL |
| `/api/file/3/files/list` | POST | Recursive file listing (sync diff) |
| `/api/file/3/files/move_v3` | POST | Move or rename file |
| `/api/file/3/files/delete_folder_v3` | POST | Hard delete file or folder |

### File Sync — Partner App (v2)

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/file/2/files/list_folder` | POST | Recursive file listing |
| `/api/file/2/files/synchronous/start` | POST | Lock sync session |
| `/api/file/2/files/synchronous/end` | POST | Release sync lock |
| `/api/file/2/files/upload/finish` | POST | Confirm upload (v2 variant) |

### File Sync — Web UI

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/file/list/query` | POST | File listing by folder |
| `/api/file/path/query` | POST | Resolve folder ID to path breadcrumb |
| `/api/file/folder/list/query` | POST | List folders (for move dialog) |
| `/api/file/folder/add` | POST | Create new folder |
| `/api/file/upload/apply` | POST | Request signed upload URL |
| `/api/file/upload/finish` | POST | Confirm upload |
| `/api/file/download/url` | POST | Get signed download URL |
| `/api/file/move` | POST | Move file(s) between folders |
| `/api/file/rename` | POST | Rename file |
| `/api/file/copy` | POST | Copy file(s) |
| `/api/file/delete` | POST | Delete file(s) (bulk) |
| `/api/file/note/to/png` | POST | Convert .note page to PNG via notelib |
| `/api/file/pdfwithmark/to/pdf` | POST | Get download URL for annotated PDF |

### File Transfer (Shared)

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/oss/upload` | POST | Upload file data (single-shot, <8MB) |
| `/api/oss/upload/part` | POST | Upload file chunk (>=8MB) |
| `/api/oss/download` | GET | Download file data |

### Tasks

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/file/schedule/group/all` | POST | Fetch all task groups |
| `/api/file/schedule/task/all` | POST | Fetch all tasks (with nextSyncToken) |
| `/api/file/schedule/task` | POST | Create a new task |
| `/api/file/schedule/task/list` | PUT | Bulk update tasks |
| `/api/file/schedule/task/{id}` | DELETE | Delete a task (hard delete) |

### Digests (Summaries)

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/file/query/summary/group` | POST | Fetch all digest category groups |
| `/api/file/query/summary/id` | POST | Fetch digest items by ID list |
| `/api/file/query/summary/hash` | POST | Fetch digest items by hash (change detection) |
| `/api/file/add/summary` | POST | Create a digest entry |
| `/api/file/update/summary` | PUT | Update a digest entry |
| `/api/file/upload/apply/summary` | POST | Get signed upload URL for digest handwriting |
