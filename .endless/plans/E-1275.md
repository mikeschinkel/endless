# E-1275 — Record each snapshot write in git immediately

## Context

E-1275 is the third in the write-time auto-commit sequence (after E-1208 for verbs.jsonl and E-1206 for db-ledger). It applies the same pattern to plan-file snapshots: the snapshot writer at `cmd/endless-hook/claude.go:888` (`snapshotPlanFile`) currently writes `<project>/.endless/plans/snapshots/<ts>-<sha8>.{md,json}` and returns; it does not commit. The files accumulate as dirt on main until `worktree land` bundles them.

Per E-987's resolution, write-time commit is the correct pattern. After E-1275 lands, the only remaining piece is E-1272 (drop `land`'s auto-commit step entirely + replace the destructive recovery hint) — all three auto-managed file types will commit at their write point.

## What's actually being changed

After `snapshotPlanFile` writes both files (the .md and the .json), commit them together on main as one commit with subject `Endless: snapshot plan`. Same amend-or-new-commit rolling pattern as E-1206 so successive snapshots between non-snapshot commits coalesce into one commit.

## Code organization

E-1206's `internal/events/commit.go` already implements the amend-or-new-commit decision for ledger segments. E-1275 needs nearly identical logic with three differences: two paths instead of one, a different commit subject, and a different "index-clean modulo X" exclude path. Refactor approach:

1. **Extract the shared logic** into a private helper `commitPaths(projectRoot string, paths []string, subject string) error` in `internal/events/commit.go`.
2. **Keep `CommitLedgerSegment`** as a thin wrapper: `return commitPaths(projectRoot, []string{segmentRel}, LedgerCommitSubject)`.
3. **Add `CommitSnapshotPair(projectRoot, mdRel, jsonRel string) error`** as a thin wrapper: `return commitPaths(projectRoot, []string{mdRel, jsonRel}, SnapshotCommitSubject)`.
4. Add constant `SnapshotCommitSubject = "Endless: snapshot plan"`.

The package name `internal/events` is slightly misleading for snapshot logic but the refactor keeps the helper colocated with `CommitLedgerSegment` and avoids a new package for two small wrappers. The right time to extract `internal/git/commit.go` is when there's a third unrelated caller; defer that.

### Amend semantics

Same as E-1206. The shared helper compares HEAD's subject to the subject being committed (not a literal string), so:
- HEAD subject `Endless: record ledger entry` + new snapshot commit → subjects differ → new commit on top.
- HEAD subject `Endless: snapshot plan` + new snapshot commit → subjects match → amend (rolling).
- HEAD subject anything else + new snapshot commit → new commit.

The `git commit -o <paths>...` works for multiple paths. When amending, `-o` stages just the named paths from the working tree; HEAD's existing tree for other paths is preserved, so prior snapshots already in HEAD remain in the amended commit while the new snapshot is added.

### Index-clean check

The existing "index clean modulo ledger dir" check needs generalizing to "index clean modulo a path glob." Pass the exclude glob as a parameter:

- `commitPaths(..., excludeGlob ".endless/db-ledger/*.jsonl")` for ledger
- `commitPaths(..., excludeGlob ".endless/plans/snapshots/*")` for snapshots

Or derive the exclude glob from the paths themselves — e.g., the directory of the first path. The latter is more concise; both work.

## Code surface

**Files to modify:**
- `internal/events/commit.go` — extract `commitPaths` private helper; add `CommitSnapshotPair` and `SnapshotCommitSubject`; rewrite `CommitLedgerSegment` as a wrapper. Same package, same file.
- `cmd/endless-hook/claude.go:933` (end of `snapshotPlanFile`) — after the json write succeeds, call `events.CommitSnapshotPair(projectRoot, mdRel, jsonRel)`. Compute the relative paths from snapsDir + stem.
- `internal/events/commit_test.go` — add tests mirroring E-1206's tests but for the snapshot subject and multi-path commit. Specifically: first snapshot creates commit, second amends (rolling), ledger-commit-then-snapshot is a new commit (subjects differ), unrelated staged path prevents amend, pushed HEAD starts new commit, non-git fails loudly.

**Existing utilities to reuse:**
- `internal/events/commit.go` `runGit` / `runGitOutput` — already there.
- `internal/events/commit.go` `ensureGitRepo` — gates non-git projects (fails loudly).

## Failure handling — fail loudly (matches E-1206/E-1208)

Per Mike's stance ("fail loudly until we know what failure modes look like"), align with E-1206/E-1208. This **changes** the existing non-fatal contract for `snapshotPlanFile`:

- **Before**: `snapshotPlanFile` returned errors; the caller at `cmd/endless-hook/claude.go:386–397` logged `"plan snapshot: %v"` and continued.
- **After**: `snapshotPlanFile` returns errors (file-write OR commit failures); the caller propagates the error and the hook exits non-zero.

Update the justifying comment block at `claude.go:387–396` to reflect the new policy. The "best-effort, self-healing" rationale was reasonable when snapshots were write-only; once we're also committing to git, a silent failure means the snapshot misses git history without surfacing the problem.

Scope clarification: this broadens the change beyond just the commit step (existing file-write failures will also become loud). That's a deliberate consequence of the "fail loudly" choice — gain visibility into all snapshot failure modes, soften specific paths later if telemetry shows benign noise.

## Verification

1. **First snapshot commits**: edit a plan file via Claude Write hook. Expect: `git log -1 --format=%s` shows `Endless: snapshot plan`; the two snapshot files appear in the commit's diff; `git status` clean for the snapshots dir.
2. **Second snapshot amends**: edit the plan again (different content). Expect: HEAD's commit subject is still `Endless: snapshot plan`; HEAD's parent unchanged; HEAD's diff now contains both pairs of snapshot files (rolling commit).
3. **Ledger commit between snapshots starts new commit**: run an endless mutation (triggers a ledger commit) between two plan-file writes. Expect: ledger commit on top of first snapshot; second snapshot is a new commit on top of the ledger commit (different subjects break the amend chain).
4. **Stale staged user-work prevents amend**: stage an unrelated file change before triggering a second snapshot. Expect: new commit (not amend); staged file remains staged.
5. **Pushed snapshot HEAD starts new commit**: simulate push (fake origin); subsequent snapshot starts a new commit.
6. **Non-git project fails loudly via `ensureGitRepo`**: but the caller (`snapshotPlanFile`) logs and continues per the non-fatal contract. The hook overall keeps working.
7. **Idempotent snapshots don't commit**: trigger two snapshots of the SAME content+session. The second is skipped by `existingSnapshot` early-return; no second commit.
8. **Tests**: `go test ./internal/events/...` for the commit decision matrix.

## Out of scope

- Moving `internal/events/commit.go` to `internal/git/commit.go` (or similar package). Refactor only when a third unrelated caller appears.
- Changing the snapshot failure mode from non-fatal to fail-loud. If we want fail-loud later, that's a one-line change in `snapshotPlanFile`'s caller.
- Anything in `worktree_cmd.AUTO_COMMIT_GLOBS` — E-1272 owns that.

## Resolved during planning

- **Refactor scope**: extract `commitPaths` private helper now (two callers justify the abstraction).
- **Commit subject**: `Endless: snapshot plan` (shortest of the proposed forms).
- **Failure mode**: fail loudly. Change `snapshotPlanFile`'s caller to propagate the error instead of swallowing with `log.Printf`. Broadens the existing "non-fatal snapshot" contract — that's the intended outcome per Mike's "fail loudly until we know what fails" stance.
- **Exclude-glob parameter**: pass explicitly to `commitPaths`. Clear at call sites and avoids deriving from path internals.
