---
name: Prefer SPC endpoints over direct DB access
description: Direction to favor SPC REST API endpoints over direct MariaDB access for state changes, to maintain consistency between SPC and UB
type: feedback
---

Lean towards leveraging SPC REST endpoints to make changes or get information rather than directly accessing the MariaDB database, where those options are available.

**Why:** Direct DB access risks inconsistency/atomicity issues between SPC's internal state and UB's view. SPC may cache or derive state from its own writes; bypassing its API can create divergence.

**How to apply:** When designing new features or refactoring existing ones that interact with SPC data, prefer REST API calls over SQL queries/writes. This is a directional preference, not an immediate mandate — existing direct DB access doesn't need to be migrated right now, but new work should favor the API path where practical.
