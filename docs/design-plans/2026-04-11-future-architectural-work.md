# Future Architectural Work

Based on a comprehensive review of the UltraBridge codebase, the following architectural opportunities have been identified to improve efficiency, maintainability, and performance. This document serves as a roadmap for future refactoring efforts.

## 1. Pipeline Unification (High Priority)

**Current State:**
The project maintains two nearly identical implementations of an SQLite-backed job queue and worker pool: `internal/processor` (for Supernote) and `internal/booxpipeline` (for Boox). Both handle state transitions, OCR execution, and embedding extraction independently.

**Opportunity:**
Create a unified `internal/jobqueue` or generic pipeline engine.
*   **Design:** Implement a single, robust worker pool that accepts heterogeneous `Task` interfaces (e.g., `SupernoteOCRTask`, `BooxRenderTask`).
*   **Benefits:** Reduces maintenance overhead, eliminates code duplication, and makes adding support for a third device (like reMarkable or Kindle Scribe) trivial.

## 2. Dependency Injection & The `main.go` Monolith

**Current State:**
`cmd/ultrabridge/main.go` is over 700 lines long and handles all component wiring manually. It currently breaks the `Source` abstraction by having to type-cast initialized sources back to their concrete types (`*supernote.Source`, `*boox.Source`) to extract the specific stores and processors required by the Web Handler.

**Opportunity:**
Enhance the `Source` interface and implement an App Context or Registry pattern.
*   **Design:** Instead of `main.go` reaching into sources to pull out dependencies, sources should register their capabilities (e.g., `RegisterWebHandlers(mux)`, `ProvidesFileScanner()`) with a central Registry.
*   **Benefits:** Flips the dependency graph, resulting in a clean, minimal bootstrap file and a truly modular architecture.

## 3. Frontend Componentization

**Current State:**
`internal/web/templates/index.html` is a massive 1,700+ line monolith containing all HTML, CSS, and vanilla JavaScript for every tab (Tasks, Files, Search, Chat, Settings).

**Opportunity:**
Split the frontend into modular components and modernize the interaction model.
*   **Design:** Break `index.html` into Go template partials (e.g., `_tasks.html`, `_settings.html`). Introduce a lightweight, dependency-free library like **HTMX** or **Alpine.js** to manage state and asynchronous interactions.
*   **Benefits:** Drastically reduces the JavaScript footprint, improves UI responsiveness (e.g., updating settings without full page reloads), and makes the frontend significantly easier to maintain.

## 4. Auth Performance & Database Consolidation

**Current State:**
The `auth.Dynamic` middleware currently executes a database query on *every single HTTP request* to validate Basic Auth credentials or bearer tokens. Additionally, the project manages two separate SQLite databases (`ultrabridge.db` and `ultrabridge-tasks.db`).

**Opportunity:**
Optimize authentication and simplify data persistence.
*   **Design (Auth):** Implement an in-memory credential/token cache with a TTL or write-through invalidation to eliminate the per-request database hit.
*   **Design (DB):** Consolidate the two SQLite databases into one `ultrabridge.db`.
*   **Benefits:** Reduces database contention, speeds up API responses, and simplifies backups, schema migrations, and connection pooling.
