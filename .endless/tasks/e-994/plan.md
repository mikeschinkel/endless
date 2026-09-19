You are a spawned Claude session working on **E-994** in the `endless` project.

Your goal: audit `snapshotPlanFile` in `cmd/endless-hook/claude.go` against the fatal-vs-non-fatal error-handling criteria we established, and either (a) document why non-fatal is justified or (b) convert it to fatal-on-error. One commit, small surface, well-scoped.

## Context

`snapshotPlanFile` currently swallows errors with `log.Printf` and returns `nil`. It's called from the `PostToolUse / Write` branch of the Claude hook to snapshot plan-file content into `.endless/plans/snapshots/`.

The fatal-vs-non-fatal criteria, established during E-989/E-1011 work:

**Non-fatal is justified ONLY when:**
1. The operation is opportunistic / best-effort by design (e.g., diagnostics, telemetry, snapshots that aren't required for correctness).
2. Failure is self-healing on a later event (e.g., `BackfillProcess` retries on the next `UserPromptSubmit`).
3. The error rate is high under normal operation and would be noisy if surfaced.

For comparison: `InitSession`, `SetProcess`, `WriteCompanion` are all fatal — silent failure means downstream features mysteriously break.

## What to do

1. `endless task start E-994` to register the session.
2. Read `cmd/endless-hook/claude.go` — find `snapshotPlanFile` (around line 538) and study its callers, what it produces, and what depends on the snapshots.
3. Apply the criteria. Decide: keep non-fatal (document why) or convert to fatal.
4. Implement the decision. If keeping non-fatal: add a `// Why non-fatal: ...` comment citing the criterion. If converting: change `log.Printf` to a returned wrapped error and propagate it up through the hook.
5. If you discover other `log.Printf("...: %v", err)` sites in the hook that fail the criteria, do NOT sweep them — file follow-up tasks. The audit for this task is scoped to `snapshotPlanFile` only.

## Constraints / conventions

- Read the project's `CLAUDE.md` first (it has worktree, install, and worktree-build setup notes — including E-996's `just go-work-init` for Go builds inside a worktree).
- Work in a git worktree under `.endless/worktrees/e-994/` on branch `e-994-snapshot-error-policy`. Set up with `git worktree add -b e-994-snapshot-error-policy .endless/worktrees/e-994 main` and `cd` there. Run `just go-work-init` if Go builds error on `replace` paths.
- Tests: `go test ./internal/monitor/... && go test ./cmd/endless-hook/...` (whichever apply).
- Commit message ends with the standard `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` line.
- When done: set status to `verify` (NOT completed). Mike marks `assumed` after he's seen it.
- Plan file: this task is small enough that a plan file is not required. If your audit produces design content beyond ~3 paragraphs, write `.endless/plans/E-994.md` and attach via `endless task update E-994 --text <path>` AND commit the plan file in the same commit (per project rule: the session that creates a plan file owns committing it).

## Status flow when finished

1. Commit on the worktree branch with a message like `"Audit snapshotPlanFile error policy (E-994)"`.
2. Land via `git -C <main> merge --ff-only e-994-snapshot-error-policy && git -C <main> worktree remove .endless/worktrees/e-994 && git -C <main> branch -d e-994-snapshot-error-policy` (rebase first if main has diverged).
3. From main, run `just install` to refresh the live binary.
4. `endless task update E-994 --status verify`.
5. Report back what you decided and why.

## Out of scope

- Sweeping other `log.Printf("...: %v", err)` sites — file follow-ups, don't fix them here.
- Refactoring `snapshotPlanFile`'s functionality. This is an error-policy audit, not a rewrite.
- Removing the function or changing what it produces.
