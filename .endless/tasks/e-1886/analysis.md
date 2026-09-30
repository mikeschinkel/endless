Resolution cannot be split from task reads (queryActiveTaskForPanes is a single-DB JOIN sessions->tasks, and sandbox task ids are a separate universe since seedFromWorktree copies no tasks), so the only coherent fix is to read main again.

Restore the pin as 'if !HasExplicitDBContext()' matching main.go's hook/channel/tmux pattern, which also keeps the --config-dir seam E-1685's harness needs. Then move the candidate-code-writes-real-ledger guard to the job trigger: suppress RunDue when InSelfDevWorktree() && pinnedToForeignRealDB().
