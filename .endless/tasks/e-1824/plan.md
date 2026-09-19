# Plan — `endless worktree sync [<id>]`: bring a worktree current with main

## Goal
One idempotent command that updates a live worktree to current `main` and
rebuilds it, replacing the error-prone manual dance (commit → merge/rebase →
recreate companion → go-work-init → rebuild binaries). No-arg form resolves the
current session's bound worktree; explicit `<id>` targets
`.endless/worktrees/e-<id>`.

## Behavior (in order; stop loudly on any failure, never auto-resolve)
1. Resolve the target worktree, its branch, and base `main`. Clean no-op with a
   clear message when already at `main`.
2. Refuse if the worktree tree is dirty (uncommitted changes) — a sync must
   never stash or discard work; the user commits first.
3. Update the branch to include current `main`. DEFAULT: **merge** `main` into
   the branch. Rationale: `.endless/*.jsonl` is the DB's append-only WAL; a
   rebase replays the branch's ledger-appending commits and risks
   reordering/duplicating WAL lines against main's appends. Merge preserves the
   appends. (E-1109 owns making merge-vs-rebase configurable; until it lands,
   sync merges.)
4. On merge conflict: stop, leave the worktree mid-merge, print the conflicted
   paths and how to finish or abort. Never auto-resolve.
5. Recreate the companion `.endless/worktree.json` (a rebase/merge can drop or
   stale it) from the task id + branch + base.
6. Regenerate `go.work` when the project uses one (endless does).
7. Rebuild the worktree's binaries so its hooks/CLI stop running stale code —
   the whole point of the command. This step is PROJECT-SPECIFIC, so delegate it
   to a pluggable hook (`.endless/hooks/post-worktree-sync.sh`, or a build-mode
   of the existing `post-worktree-create.sh`), NOT a hardcoded `just build` —
   keeping the shipped verb project-agnostic (user-facing verbs must not shell
   out to `just`). Endless's own hook runs `just build`.
8. Reconcile the sandbox: a non-pinned open with the rebuilt binary self-heals
   the enum mirror via the E-1659 upsert seed, so one post-rebuild non-pinned
   invocation (or the next use) reconciles it — no manual DB edits.
9. Print one summary line: commits pulled + binary rebuilt.

## Touch-points
- `src/endless/worktree_cmd.py` — new `sync` command in the `endless worktree`
  group; reuse existing worktree-resolution + companion-writer helpers.
- `src/endless/cli.py` — wire the subcommand (no-arg + optional `<id>`).
- `.endless/hooks/post-worktree-sync.sh` (new) OR a build-mode of
  `post-worktree-create.sh` — the project's rebuild step.
- Existing go-work-init + companion helpers (reuse, do not duplicate).

## Scope boundaries
- Merge-vs-rebase configurability = E-1109 (this ships only the safe merge
  default).
- Auto-refreshing ALL live worktrees on land (the push side) is E-1364-adjacent,
  not here — sync is the explicit, per-worktree pull.
- No stash, no auto-resolve of conflicts or dirty trees — refuse and defer to
  the user.

## Verify
Fold into `tests/tasks/e-<id>-verify.sh` (model `tests/tasks/e-1659-verify.sh`),
single hand-off command `esu && ./tests/tasks/e-<id>-verify.sh`:
- a worktree behind main, synced, ends containing main's HEAD with its own
  commits intact; companion + `go.work` regenerated; binary rebuilt (newer).
- an already-current worktree → clean no-op, exit 0.
- a dirty worktree → refused, tree unchanged.
- (integration) after sync, the worktree binary's hook opens the real DB with no
  fail-close — the e-1755 stale-binary symptom is gone.
- full Go + Python suites as the final regression gate.
