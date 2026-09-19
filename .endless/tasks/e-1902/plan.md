# Plan — make worktrees disposable by capturing WIP as durable git refs

## Decisions locked (Mike, 2026-08-16)

| # | Decision | Choice |
|---|---|---|
| 1 | Trigger and cadence | **Both** — event-driven at drop/reap, plus a periodic sweep |
| 2 | Ref granularity | **Chained** — one ref per worktree, each snapshot parented on the last |
| 3 | Retention | **Lifecycle-based** — prune when the task is terminal AND its branch is merged to main. Explicitly NOT age-based. |
| 4 | Drop's unlanded refusal | **Warn + snapshot + proceed**, with one carve-out: refuse when non-derived gitignored files are present |
| 5 | macOS TM/Spotlight exclusions | **Out of scope** — deferred to E-1988 (phase `later`) |

The verified mechanism, the destructive-test transcript, and the measurements
behind all of this are in this task's `analysis`. Read it first; do not re-derive.

## Architecture

Three packages, arranged so nothing needs a seam (contrast E-1904, where
`sandboxcmd` already imported `monitor` and the reaper had to call out through a
function variable):

```
internal/wip      pure git plumbing. NO database, NO monitor import.
                  -> monitor may import it directly.
internal/wipjob   the periodic sweep + lifecycle pruning. Imports wip AND
                  monitor. Jobs sit at the top of the graph (see triagejob),
                  so this cannot cycle.
endless-go wip    thin subcommand over internal/wip, so the Python CLI can
                  shell to it the way it already shells to `endless-go event`.
```

Keeping `internal/wip` DB-free is the load-bearing constraint: the reaper lives
in `monitor`, and retention needs task status, so putting both in one package
would force exactly the cycle the split avoids.

## Phase 1 — the snapshot mechanism (`internal/wip`)

`Snapshot(worktreeDir string) (Result, error)` — the core, per the verified
recipe in `analysis`:

- Build the tree with a throwaway index so the working tree and real index are
  never touched: `GIT_INDEX_FILE=$(mktemp -u)`, `git add -A`, `git write-tree`.
- **Dedup before committing.** If the new tree hash equals the tree of the
  current `refs/wip/<name>`, return "unchanged" and create nothing. Without
  this the timer grows the chain forever with identical snapshots.
- Parent selection (this is what makes the chain): the current
  `refs/wip/<name>` when it exists, otherwise the worktree's `HEAD`. The chain
  therefore reads snapshot → snapshot → … → branch tip, and
  `git log refs/wip/<name>` just works.
- Commit message carries the provenance trailers, so restore is self-describing
  even if the ledger is gone:

  ```
  wip: e-1904

  Branch: task/1904-clean-up-worktree-s-dependents-when
  Worktree: .endless/worktrees/e-1904
  ```

- `git update-ref refs/wip/<name> <commit>`.

Also in this package: `List()`, `Show(name, path)`, `Diff(name)`,
`Restore(name, opts)`, `Drop(name)`.

`Restore` must (a) recreate the worktree at the recorded branch, (b) lay the
snapshot tree over it, (c) re-run `post-worktree-create` so `go.work`, `bin/`,
the sandbox, and `.claude/settings.json` come back. `--into DIR` skips (a) and
(c) and just extracts.

### Known, accepted gaps — document them, do not silently ship them

- **Staged-vs-unstaged is collapsed** into one tree. `git stash` preserves the
  split via a second parent; we deliberately do not, because the distinction is
  rarely load-bearing at drop time and it doubles the snapshot logic.
- **Gitignored files are not captured** — `git add -A` honors `.gitignore`,
  which is exactly what keeps snapshots at kilobytes. This gap is the entire
  reason the drop carve-out in Phase 3 exists.
- **Empty directories** cannot be represented by git.

## Phase 2 — event-driven capture

Two call sites, both snapshotting immediately before the directory goes:

- **Reaper** — `internal/monitor/reap_worktrees.go`, in `maybeReapWorktree`
  before `git worktree remove`. `monitor` imports `internal/wip` directly.
  Failure to snapshot must ABORT that worktree's reap (skip it, log it) rather
  than proceed: the whole point is that removal is safe only once capture
  succeeded. This is the opposite of `reapBoundSandbox`'s swallow-and-continue,
  which is correct there because the worktree is already gone by then.
- **Drop** — `src/endless/worktree_cmd.py:drop_worktree`, shelling
  `endless-go wip snapshot`.

## Phase 3 — drop UX (warn + snapshot + proceed)

Replace the refusal with:

```
◆ e-1904 has uncommitted work — captured before dropping.

    3 modified, 1 untracked  →  refs/wip/e-1904  (a3f9c21)

  Recover with:  endless wip restore E-1904

  Removed .endless/worktrees/e-1904
```

**The carve-out.** Enumerate ignored files with
`git ls-files --others --ignored --exclude-standard`, subtract the known-derived
set, and refuse if anything remains:

```
✗ Refusing to drop e-1904 — 2 gitignored files cannot be captured:

      .env
      scratch/local.db

  WIP capture honors .gitignore, so these would be lost with no way back.
  Move them out, git-add them, or pass --force to drop anyway.
```

**The derived set MUST be configurable**, not hardcoded. Endless's own derived
paths (`bin/`, `.venv/`, `__pycache__/`, `.pytest_cache/`, `.ruff_cache/`,
`.mypy_cache/`, `node_modules/`) are the default; a downstream project has
different ones, and a hardcoded list would make this refuse on every drop for
them. Read `wip_derived_paths` from `.endless/config.json`, defaulting to the
list above.

Also coordinate with **E-1947**, which adds drop's live-cwd guard and explicitly
defers the unlanded-refusal question here. That guard stays; it is about a
process standing in the directory, not about losing work.

## Phase 4 — the periodic sweep and lifecycle retention (`internal/wipjob`)

One `jobs.Job` doing both halves — they share the worktree enumeration and want
the same cadence. Follow `internal/triagejob` exactly: `Name()`, `Schedule()`,
`Run(ctx)`, `jobs.Register` in `init()`, registered by side-effect import in
`cmd/endless-go/main.go` next to `triagejob`. `Run` must not write to stdout or
stderr (the trigger may be a live TUI) and must be idempotent under the lease.

**Sweep half.** For each worktree: cheap `git status --porcelain`; skip if
clean; otherwise `Snapshot`. The tree-hash dedup from Phase 1 means an idle-but-
dirty worktree costs one `write-tree` and no new objects.

Cost matters here and should be measured, not assumed: this machine has 113
worktrees, and the problem that motivated the whole epic was filesystem churn.
Pick the interval from the measured sweep cost, and record the measurement in
the task outcome.

**Retention half.** Delete `refs/wip/<name>` only when BOTH hold:

1. the task is at a terminal status (`confirmed` / `assumed` / `completed`), and
2. its branch is merged to main.

Condition 2 reuses the predicate shape from E-1904 —
`internal/sandboxcmd/reapguard.go:unmergedTaskBranches` already computes exactly
this from `git branch --no-merged main`. Extract it somewhere shared rather than
copying it; one concept, now three callers.

The deletion condition is "this content is provably already on main," so
retention can never destroy the only copy of anything. Never prune on age.

## Phase 5 — `endless wip` command family

Python CLI group (user-facing, consistent with `endless task` / `endless
worktree`), shelling to `endless-go wip`:

```
endless wip list                      # recoverable snapshots: name, age, task, files
endless wip show <id> [path]          # stats, or one file's contents
endless wip diff <id>                 # vs main
endless wip restore <id> [--into DIR] # recreate the worktree, or extract
endless wip drop <id>                 # discard
```

Accept both `E-1904` and `e-1904` forms. Users must never need the raw plumbing
— that is the whole justification for this phase.

## Verification

`tests/tasks/e-1902-verify.sh`, fail-fast layered like `e-1904-verify.sh`.

**Layer A — the destructive E2E. This is the crown jewel; write it first.**
Reproduce by script the exact sequence proved by hand in `analysis`: build a
scratch repo, add a worktree, make a tracked modification AND an untracked file,
snapshot, then

```
git worktree remove --force ; git branch -D
git reflog expire --expire=now --all ; git gc --prune=now --aggressive
```

then `wip restore` and assert both files come back **byte-identical**. If this
does not hold, nothing else matters.

**Layer B — unit tests.** Chain parenting (second snapshot parents on the
first); tree-hash dedup creates no commit; provenance trailers round-trip;
`.gitignore` is honored; `--into` extracts without a worktree.

**Layer C — retention.** A ref survives while the branch is unmerged; survives
while the task is non-terminal; is pruned only when both conditions hold.

**Layer D — drop UX.** Warns and proceeds on ordinary uncommitted work; refuses
on a non-derived gitignored file; does NOT refuse on `bin/` or `.venv/`; honors
a project-configured `wip_derived_paths`.

**Layer E — project-wide regression.** `go build ./...`, `go test ./...`,
`just test`.

## Sequencing

Phases 1 → 2 → 3 deliver the whole safety guarantee and are independently
landable. 4 and 5 are the ergonomics on top. If the task needs splitting, split
after 3 — but prefer landing it whole over filing more tasks.
