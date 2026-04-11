# Roadmap: Decoupled Client-Server Architecture

This document outlines the strategic evolution of UltraBridge from a monolithic web application into a headless platform with specialized user interfaces.

## Phase 1: The API Audit & Contract (The Blueprint)

**Goal:** Establish a formal API specification that completely describes the system's capabilities independent of the UI.

*   **Action:** Audit `internal/web/handler.go` and `index.html` to identify every data point and user action (form POSTs, template variables, action triggers).
*   **Outcome:** Define standard JSON schemas for core entities: `Task`, `NoteFile`, `SyncStatus`, `EmbeddingJob`, and `Configuration`.
*   **Benefit:** Exposes exactly where backend logic is currently coupled with frontend templates and provides a checklist for all subsequent phases.

## Phase 2: The Logic/Render Split (Headless-Ready)

**Goal:** Extract business logic from the HTTP layer into a dedicated service layer.

*   **Action:** Refactor the Go backend to use a "Service Layer" pattern. Handlers should only handle request parsing and response formatting, delegating all logic (DB queries, job creation, state changes) to specialized services.
*   **Outcome:** A robust `/api/v1/...` endpoint for every system capability.
*   **Benefit:** The backend becomes fully "headless." The core system can be managed entirely via CLI or API without the web UI.

## Phase 3: Modular UI via Fragments (HTMX/Componentization)

**Goal:** Break the `index.html` monolith and implement a dynamic, reactive frontend.

*   **Action:** Decompose `index.html` into modular Go template fragments (e.g., `_tasks.html`, `_files.html`, `_chat.html`). Use **HTMX** to load and swap these fragments dynamically.
*   **Outcome:** A faster, more responsive UI that updates parts of the page without full reloads.
*   **Benefit:** These fragments become reusable "building blocks" for different UI variants (desktop, mobile, E-Ink).

## Phase 4: The Unified Engine (Deep Efficiency)

**Goal:** Consolidate note processing pipelines into a single, high-efficiency job engine.

*   **Action:** Merge `internal/processor` (Supernote) and `internal/booxpipeline` (Boox) into a single, generic Job Engine that handles state, OCR, and embeddings for all device types.
*   **Outcome:** A unified WebSocket/SSE stream for tracking all background activity.
*   **Benefit:** Maximum code reuse and a simplified architecture for adding support for new devices or document formats.

## Phase 5: Multi-Client Deployment (Vision Realized)

**Goal:** Deploy specialized interfaces tailored for specific devices and use cases.

*   **The Desktop Dashboard:** The current full-featured management UI.
*   **The E-Ink Remote:** A specialized UI (e.g., at `/tablet`) designed for low-refresh E-Ink browsers with high-contrast CSS and large touch targets.
*   **The Mobile Sync:** A lean, PWA-ready UI focused on task management and search for use on phones.
*   **The Power-User CLI:** A specialized CLI tool for bulk operations, leveraging the core API.
