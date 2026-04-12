# UltraBridge API Specification (v1)

This document defines the formal API contract for the UltraBridge headless platform.

## Core Entities

### Task
Represents a synchronization task, typically mirrored from a device (Supernote/Boox) or a CalDAV server.

```json
{
  "id": "string",
  "title": "string",
  "status": "needsAction | completed",
  "created_at": "ISO8601 string",
  "due_at": "ISO8601 string | null",
  "completed_at": "ISO8601 string | null",
  "detail": "string | null",
  "links": {
    "app_name": "string",
    "file_path": "string",
    "page": 1
  }
}
```

### NoteFile
Represents a digital notebook file on the system.

```json
{
  "name": "string",
  "path": "string",
  "rel_path": "string",
  "is_dir": false,
  "file_type": "note | pdf | epub | other",
  "size_bytes": 1024,
  "created_at": "ISO8601 string",
  "modified_at": "ISO8601 string",
  "source": "supernote | boox",
  "device_info": "string | null",
  "job_status": "unprocessed | pending | in_progress | done | failed | skipped | unsupported",
  "last_error": "string | null"
}
```

### SyncStatus
Represents the state of the CalDAV synchronization engine.

```json
{
  "adapter_id": "string",
  "adapter_active": true,
  "in_progress": false,
  "last_sync_at": "ISO8601 string | null",
  "next_sync_at": "ISO8601 string | null",
  "last_error": "string | null"
}
```

### EmbeddingJob
Represents the status of background processing (OCR and Vector Embeddings).

```json
{
  "running": true,
  "pending_count": 5,
  "in_flight_count": 1,
  "processed_count": 120,
  "failed_count": 2,
  "active_task": {
    "path": "string",
    "title": "string",
    "started_at": "ISO8601 string"
  }
}
```

### Configuration
System-wide configuration settings.

```json
{
  "auth": {
    "username": "string"
  },
  "ocr": {
    "provider": "anthropic | openai",
    "api_url": "string",
    "model": "string",
    "concurrency": 1,
    "max_file_mb": 50
  },
  "rag": {
    "enabled": true,
    "ollama_url": "string",
    "embed_model": "string",
    "chat_enabled": true,
    "chat_api_url": "string",
    "chat_model": "string"
  },
  "sources": [
    {
      "id": 1,
      "type": "supernote | boox",
      "name": "string",
      "enabled": true,
      "config": {
        "notes_path": "string",
        "backup_path": "string"
      }
    }
  ]
}
```

## Endpoints (Draft)

### Tasks
- `GET /api/v1/tasks` - List all tasks
- `POST /api/v1/tasks` - Create a new task
- `POST /api/v1/tasks/{id}/complete` - Mark task as completed
- `DELETE /api/v1/tasks/{id}` - Delete a task
- `POST /api/v1/tasks/bulk` - Bulk operations (complete, delete)

### Files
- `GET /api/v1/files` - List files (with filtering, sorting, pagination)
- `POST /api/v1/files/scan` - Trigger filesystem scan
- `POST /api/v1/files/queue` - Enqueue file for processing
- `GET /api/v1/files/content?path={path}` - Get OCR text and page metadata
- `GET /api/v1/files/render?path={path}&page={n}` - Get page image

### Search & Chat
- `GET /api/v1/search?q={query}` - Unified keyword and RAG search
- `POST /api/v1/chat/ask` - Conversational RAG interface (SSE stream)

### System
- `GET /api/v1/status` - Global system status (Sync + Jobs)
- `GET /api/v1/config` - Get current configuration
- `PUT /api/v1/config` - Update configuration
- `GET /api/v1/logs` - Stream live logs (WebSocket)
