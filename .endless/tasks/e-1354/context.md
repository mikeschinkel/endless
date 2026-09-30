snapshotPlanFile currently resolves projectRoot via monitor.ProjectPath(projectID), which returns the sandbox-registered worktree path when E-1281 is active; CommitSnapshotPair then runs git -C <worktree> and ensureMainCheckout refuses on the linked-worktree gitdir.

Hit on every PostToolUse:Write that produces a plan snapshot in a sandboxed self-dev worktree (first surfaced from E-1327's session, 2026-05-15 20:27 EDT).
