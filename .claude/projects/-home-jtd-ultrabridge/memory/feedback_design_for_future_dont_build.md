---
name: Design for future state, don't build it yet
description: User validates future architectural direction during brainstorming to avoid painting into corners, not to scope-creep current work
type: feedback
---

When the user explores future scenarios during design (e.g., inbound WebDAV adapters, document-store backends, multi-vendor sync), they're verifying the architecture won't preclude future work — not requesting it be built now.

**Why:** Avoid building things that make it harder to step into future designs later. The goal is to document desired future state and ensure current interfaces/schemas are compatible with it.

**How to apply:** During design, validate that interfaces and data models accommodate future patterns. Document the future direction in the design plan's "Additional Considerations" section. But scope the actual implementation to what's needed now. Don't add abstractions, config flags, or extension points that aren't used by current work — just don't close doors.
