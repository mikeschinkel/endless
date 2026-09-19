# Plan: fix the two main-guard block messages (E-2015)

E-1012 shipped `blockCommitOnMainIfApplicable` (internal/hookcmd/claude.go) and
is `confirmed`. A session still committed on main to clear a `worktree land`
refusal. The gate is not missing — its message is the hole.

Three defects, all in message text, all in `internal/hookcmd/claude.go`:

1. **The commit-on-main block publishes its own bypass.** It ends with
   "Bypass (NOT recommended): git commit --no-verify". PreToolUse fires for
   exactly one audience — an agent — and an agent reading a block is already
   looking for a way through. `--no-verify` still works; endless should simply
   stop handing it over.
2. **Both guards call a BLOCKED action "highly discouraged".** Softened language
   plus a published escape hatch reads as permission with a disclaimer.
3. **Both guards teach `git worktree add` by hand.** Agents must not create
   worktrees on an Endless project: `endless task claim` creates AND wires one,
   and a hand-made worktree skips the post-worktree-create hook (go.work, the
   worktree's own bin/endless-go, the per-worktree Claude hook override), leaving
   the session silently on the global binary. Long-standing text, not a recent
   regression.

## The change

Written and verified on E-1817's branch (`go build/vet ./...` clean,
`go test ./internal/hookcmd/` ok, `just test` 1499 passed), then reverted from
there: E-1817 is a research task and had no business carrying behavior code.
Apply this patch verbatim.

```diff
diff --git a/internal/hookcmd/claude.go b/internal/hookcmd/claude.go
index dfe07ecd..18ffd7b7 100644
--- a/internal/hookcmd/claude.go
+++ b/internal/hookcmd/claude.go
@@ -1248,7 +1248,7 @@ func blockCommitOnMainIfApplicable(payload claudePayload) {
 		return
 	}
 
-	blockToolUse(`Direct commits to main are highly discouraged when using endless.
+	blockToolUse(`Direct commits to main are blocked when using endless.
 
 main is the integration target. Make changes in a worktree on a per-task
 branch, then merge via ` + "`endless worktree land <task-id>`" + `.
@@ -1256,14 +1256,9 @@ branch, then merge via ` + "`endless worktree land <task-id>`" + `.
 If you have an Endless task for this work:
   endless task claim E-NNN          # creates worktree at .endless/worktrees/e-NNN
 
-Or by hand:
-  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main
-  cd .endless/worktrees/e-NNN
-  # ... do work, commit ...
-  endless worktree land E-NNN
-
-Bypass (NOT recommended):
-  git commit --no-verify`)
+If you do not:
+  endless task add "<title>"
+  endless task claim E-NNN`)
 }
 
 // blockPlanFileWriteIfApplicable refuses any Write/Edit/NotebookEdit whose
@@ -1827,16 +1822,14 @@ func enforceWorktreeGate(projectID int64, payload claudePayload) {
 					*session.ActiveTaskID, wp, wp)
 			}
 		}
-		blockToolUse("Edits in main are highly discouraged when using endless.\n\n" +
+		blockToolUse("Edits in main are blocked when using endless.\n\n" +
 			"main is the integration target — every edit ideally should go through\n" +
 			"a worktree.\n\n" +
 			"If you do not yet have an active task, create one and start it:\n" +
 			"  endless task add \"<title>\"\n" +
 			"  endless task claim E-NNN          # auto-creates the worktree\n\n" +
 			"If you already have an active task without a worktree:\n" +
-			"  endless task claim E-NNN          # idempotent; creates if missing\n\n" +
-			"Or create the worktree by hand or via `endless pivot` (when available):\n" +
-			"  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main" +
+			"  endless task claim E-NNN          # idempotent; creates if missing" +
 			redirectHint)
 		return
 	}
```

## Verification

`tests/tasks/e-2015-verify.sh` must assert, against the built binary's emitted
block payloads:

- neither guard's message contains `--no-verify`
- neither contains `git worktree add`
- neither contains "highly discouraged"; both say "blocked"
- the commit-on-main guard still fires from main's working tree and still allows
  a commit from inside a worktree and during an active merge (unchanged
  behavior — only the text moved)

Plus `go build/vet ./...`, `go test ./internal/hookcmd/`, `just test`.

## Out of scope, still open

`endless worktree land`'s refusal ("main has uncommitted user changes") does not
say a refusal is terminal, so being asked to fix the blocker still reads as
authorization to commit. Separate message, separate call — decide whether it
belongs here or in its own task.
