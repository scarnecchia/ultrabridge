---
name: Vendor task sync landscape
description: Current state of task sync capabilities across e-ink vendors — Supernote only one with real task sync, Boox CalDAV broken/VEVENT-only
type: project
---

Supernote is the only e-ink vendor with meaningful built-in task sync capability.

Onyx/Boox: recently added CalDAV to their Calendar app but it only supports VEVENT (not VTODO), and it's still broken as of 2026-04-04. May never intend to support VTODO. A future Boox adapter may need to "munge VEVENT into VTODO" — intentionally creative/cursed bridging.

Viwoods, reMarkable, iFlytek: no known task sync capabilities investigated yet.

**Why:** UltraBridge will be pioneering task sync support for most devices, hacking it in via creative adapters. This is the origin of the "UltraBridge" name — bridging different software in creative ways.

**How to apply:** Don't assume other vendors have clean APIs. Adapter designs should expect messy, creative protocol translations. The Boox VEVENT→VTODO case is the template for how weird these adapters might get.
