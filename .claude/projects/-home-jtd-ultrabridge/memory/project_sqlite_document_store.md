---
name: SQLite as universal storage backend
description: Exploring SQLite's document-store capabilities inspired by Boox's Couchbase Lite usage — flexible metadata + blob storage for multi-device note data
type: project
---

Boox devices use Couchbase Lite as a do-all document store: files, metadata, point/stroke data all go in, cloud service sorts it out. This flexibility is appealing.

User is exploring whether SQLite can serve a similar role — flexible document/metadata store for notes from multiple device vendors. Performance likely not a concern for single-user system with at most two devices syncing simultaneously.

**Why:** Multi-vendor vision needs a storage backend that can handle heterogeneous data from different device formats without rigid schema for each vendor.

**How to apply:** When designing storage schemas, consider SQLite's JSON support, blob storage, and FTS capabilities as potential document-store patterns. Don't over-commit to rigid relational schemas for data that varies by vendor. This is exploratory thinking, not a decision yet.
