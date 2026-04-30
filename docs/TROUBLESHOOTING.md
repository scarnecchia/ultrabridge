# UltraBridge Troubleshooting

Common issues and how to resolve them. If your problem isn't covered
here, check `docker logs ultrabridge` — most failure modes log a
specific reason, and **Verbose API Logging** (Settings > General)
turns on per-request auth-failure detail.

## Auth and access

### Can't log in to the web UI

1. Try `curl http://localhost:8443/health` — should return
   `{"status":"ok"}`. If that fails, the container isn't running or
   the port isn't exposed.
2. If you previously ran the installer, check that the username /
   password you entered match. Reset with the `seed-user`
   subcommand (see README > Development > Admin subcommands).
3. On a fresh install, `/setup` should be reachable without auth —
   if it isn't, check that the settings DB was created under the
   bind-mounted `/data` path.

### Claude.ai "Authorization Failed"

1. Ensure your UltraBridge instance is reachable via a public URL
   (e.g. a tunnel or reverse proxy) — Claude.ai can't reach
   `localhost`.
2. Turn on **Verbose API Logging** in **Settings > General** so the
   OAuth handshake's failure reason is surfaced.
3. Check `docker logs ultrabridge` for `auth failure` lines.
4. If you recently changed your password, disconnect the server in
   Claude.ai and reconnect to trigger a fresh OAuth flow.

### MCP tools not appearing in Claude

1. Confirm the MCP server is enabled in **Settings > General**.
2. Verify the SSE endpoint is the one you registered (typically
   `https://your-host/mcp`).
3. Watch the **Logs** tab for `MCP tool call` entries while Claude
   is connecting — if there's nothing, Claude isn't actually
   reaching the server.

## CalDAV

### Client shows empty collection

1. `curl -u admin:password http://localhost:8443/caldav/tasks/` —
   should return an XML `multistatus` response. If it returns
   `401`, credentials are wrong. If it returns `404`, no tasks
   exist yet.
2. Verify your CalDAV client is pointing at the
   `/.well-known/caldav` endpoint or directly at
   `/caldav/tasks/` (trailing slash required by some clients).
3. Check `docker logs ultrabridge` for PROPFIND errors.

### Task created via CalDAV but not visible in UB web UI

Soft-delete filtering excludes `is_deleted='Y'` rows from both the
API and the web UI. A task marked completed on the device rather
than deleted should still be visible with a "completed" badge; if
it's not, check `docker logs` for sync errors.

## Files

### Supernote Files tab shows "No Supernote source configured"

Add a Supernote source in **Settings > Sources** with the path to
your `.note` files. The equivalent placeholder on the Boox Files
tab points at the same Settings screen.

### OCR jobs stuck in "in_progress"

The watchdog reclaims stuck jobs after 10 minutes. If jobs
consistently get stuck, check that your OCR API URL and API key
are correct in **Settings > OCR** and that the API is reachable
from inside the container:

```bash
docker exec -it ultrabridge wget -O- http://your-ocr-host:8000/v1/models
```

### Boox WebDAV sync fails

1. Verify a Boox source exists and is enabled in **Settings > Sources**.
2. Check the WebDAV URL on the device is
   `http://<host>:<port>/webdav/` (trailing slash required for
   some Boox firmware).
3. Confirm credentials match your UltraBridge username/password.
4. `docker logs ultrabridge | grep boox` to see what the server saw.

### Boox notes not appearing in Files tab

1. Verify a Boox source exists in **Settings > Sources** with a valid
   notes path.
2. Check the Docker volume mount includes the Boox notes path — the
   container has to be able to see the files on disk.
3. Uploaded files should appear at `{notes_path}/onyx/{model}/...`.
   If they don't, the WebDAV server isn't receiving them.

## Supernote Private Cloud integration

### "database connection failed" at startup

Expected in standalone mode (no SPC). The warning is non-fatal —
UltraBridge continues with SQLite-only storage, and only SPC
catalog sync is disabled. Supernote device-sync via SPC REST and
Boox WebDAV both continue to work.

If you do have SPC installed, check that `.dbenv` is readable and
MariaDB is running:

```bash
cat /mnt/supernote/.dbenv
docker ps | grep mariadb
```

### "user resolution failed"

- **"no users found"** — No users in the SPC database yet. Sync
  your Supernote device against SPC at least once.
- **"multiple users found"** — Set `UB_USER_ID` in
  `.ultrabridge.env` to disambiguate.

### Supernote device shows stale / wrong file sizes after OCR

Post-OCR catalog sync updates MariaDB's `f_user_file` and
`f_capacity` tables so the device's listing matches the modified
file. If those rows drift, trigger a re-scan from Settings or
re-process a single file from the Files tab.

## RAG search / chat

### "No Ollama connection" or RAG search falls back to FTS-only

1. Confirm Ollama is running: `curl http://your-ollama-host:11434/api/tags`.
2. The embedding model name in **Settings > Embedding** must match
   exactly what Ollama has pulled (`nomic-embed-text:v1.5`, not
   `nomic-embed-text`).
3. Embeddings are best-effort — if Ollama is down, OCR indexing
   continues; vector search just degrades to FTS. Re-run the
   backfill from Settings once Ollama is back.

### Chat streams forever / vLLM unreachable

The Chat tab surfaces a red error banner if the OpenAI-compatible
endpoint can't be reached. Verify:

```bash
curl http://your-vllm-host:8000/v1/models
```

Match the model ID returned there with **Settings > Chat > Chat
Model**.

### vLLM dies with CUDA OOM and doesn't auto-restart

Symptom: vLLM serves fine for hours or days, then OCR jobs and chat
both start failing with `connect: connection refused` to the vLLM
host. `journalctl -u vllm.service` shows
`torch.OutOfMemoryError: CUDA out of memory` deep inside the model
forward pass (often `qwen3_vl.visual`), preceded by a sustained
stream of normal `POST /v1/chat/completions` lines.

This is usually allocator fragmentation under steady multimodal
traffic, not a single oversized request. The OOM message will note
hundreds of MiB "reserved but unallocated" alongside ~95% of the
card allocated — the GPU has free memory but no contiguous slab
large enough for the next image tensor.

Two mitigations on the vLLM host's systemd unit:

```ini
[Service]
Environment="PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True"
Restart=always
RestartSec=10
```

- `expandable_segments=True` lets the CUDA allocator grow existing
  segments instead of refusing fragmented free space.
- `Restart=always` (rather than `on-failure`) catches the case
  where vLLM's API server gracefully shuts down after the engine
  process dies. systemd sees `exit 0` and `on-failure` will not
  restart the service.

After editing: `sudo systemctl daemon-reload && sudo systemctl restart vllm.service`.
