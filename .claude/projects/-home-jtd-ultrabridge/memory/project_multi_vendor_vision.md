---
name: Multi-vendor device hub vision
description: UltraBridge's long-term direction is device-agnostic personal knowledge hub with vendor sync adapters, not Supernote-specific sidecar
type: project
---

UltraBridge's goal is to support multiple e-ink device vendors (Supernote, Boox, Viwoods, possibly reMarkable, iFlytek) as seamlessly as possible — not just be a Supernote sidecar.

**Why:** User wants to be able to stand up UltraBridge independently and connect any combination of devices. Example: run UltraBridge alone consuming only Boox content, no SPC needed.

**How to apply:**
- UltraBridge should be the authority for tasks and content, with vendor-specific sync as optional downstream adapters
- CalDAV task storage should move to UltraBridge's own schema (strategy #4 from Tier 3 discussion) — this decouples from SPC entirely for tasks
- Notes pipeline should generalize beyond .note format over time
- Architecture direction: `CalDAV clients ←→ UltraBridge (own DB) ←→ vendor adapters`
- Don't assume Supernote/SPC is always present when designing new features
