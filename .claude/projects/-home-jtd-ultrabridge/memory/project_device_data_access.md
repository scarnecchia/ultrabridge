---
name: Device data access landscape
description: How data gets out of each vendor's devices — Supernote is the outlier with a private cloud; others require hacks like WebDAV export or syncthing /sdcard
type: project
---

Supernote (Ratta) is the ONLY vendor offering a private cloud server option. They are the "model citizen" — clean REST API, self-hosted, documented.

All other vendors use "we control the horizontal and vertical" approaches:
- **Boox (Onyx)**: Data access via "export to WebDAV on note close" feature, or syncthing /sdcard/.ksync. Cloud service not meaningfully user-accessible.
- **Viwoods**: Cloud service barely exists.
- **reMarkable, iFlytek**: TBD but expect similar walled-garden patterns.

**Why:** UltraBridge is the integration hub. Supernote is the exception with a proper API. Most adapters will consume data from filesystem-level hacks (watched directories, synced folders), not vendor APIs.

**How to apply:**
- Don't design adapters assuming clean vendor APIs — most won't have one
- The adapter interface should accommodate "watch a directory for files" as a first-class input method, not just "call a REST API"
- Supernote is the model citizen; design the adapter interface so it works naturally for Supernote, then verify it also works for "I'm watching a syncthing folder" style inputs
- Task sync specifically: for non-Supernote devices, tasks may arrive as files (exported iCal), directory watches, or creative protocol munging — not API calls
