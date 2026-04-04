---
name: NoteBridge SPC replacement on hold
description: NoteBridge project paused indefinitely due to unresolved sync semantics preventing Standard note text injection
type: project
---

NoteBridge (SPC replacement, design at docs/design-plans/2026-03-22-notebridge-spc-replacement.md) is on hold indefinitely.

**Why:** Something about the Supernote sync semantics prevents RECOGNTEXT injection into Standard notes (FILE_RECOGN_TYPE=0) from working correctly. Root cause not yet identified.

**How to apply:** Don't suggest NoteBridge work or treat it as active. If the user asks about it, note it's blocked on sync semantics investigation.

Related: the CONFLICT files that motivated NoteBridge's "no CONFLICT files" goal were caused by broken sync behavior during earlier attempts to replace text recognition data in RTR notes (FILE_RECOGN_TYPE=1). That specific issue has been resolved by no longer modifying RTR note files.
