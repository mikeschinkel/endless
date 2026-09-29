Mirror the BackfillProcess pattern.

Builds on the per-session, content-hash idempotency in existingSnapshot — re-running snapshotPlanFile for already-snapshotted content is already a no-op.
