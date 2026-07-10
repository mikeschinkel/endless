Once E-969 ships eager plan snapshots to .endless/plans/snapshots/, this task adds the prune mechanism.

Policy:
- Snapshots whose content was attached to a task end up copied to .endless/plans/<task-id>.md, so the snapshot itself is redundant after attach (provenance lives at the task's file).
- Keep for N days (default 30, configurable): unattached snapshots.
- Hard cap (default 100MB, configurable): oldest-first eviction.

CLI: 'endless plan snapshots prune [--days N] [--max-size MB] [--dry-run]'. Idempotent. Surface count and bytes freed. Triggered opportunistically (on 'endless plan snapshots' invocations) or via explicit prune call. No background daemon.