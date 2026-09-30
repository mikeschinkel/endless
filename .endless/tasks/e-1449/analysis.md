Deletes snapshotPlanFile + PostToolUse trigger, events.CommitSnapshotPair, monitor.IsSandboxActive, the endless snapshots CLI surface, the .endless/plans/snapshots/* entry in AUTO_COMMIT_GLOBS, and all 60+ committed snapshot files.

Preserves isPlanFile and SetPlanFilePath (used by ExitPlanMode plan-file tracking, unrelated to snapshots).
