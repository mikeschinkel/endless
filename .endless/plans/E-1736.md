# E-1736 — Refuse worktree land when the branch's commits touch `.endless/db-ledger/`

## Context

`.endless/db-ledger/` is the write-ahead log for Endless's SQLite DB — its JSONL
segments ARE the database (corrupting them corrupts the DB). Ledger auto-commits
are recorded on the **main checkout only** (the ED-1525 routing policy, enforced
in Go at `internal/events/commit.go`'s `ensureMainCheckout`). A ledger commit on
a *task branch* is therefore always wrong: it rides the land rebase straight into
`main`, injecting a branch-authored ledger segment into shared history.

Today, `endless worktree land` only catches this *indirectly* — if the branch-side
ledger commit happens to conflict during the Step 4 rebase, the user sees a generic
rebase-failure message. A ledger commit that rebases cleanly slips into `main`
silently. This task adds a **deterministic gate**: before the rebase, refuse the
land outright when any surviving branch commit touches the ledger dir, naming the
offending commits. It's a backstop to the ED-1525 routing policy — the Go guard
prevents these commits from being *created*; this gate stops any that slip through
from *landing*.

**Key subtlety (from E-1733's planning input):** a naive "does `base..HEAD` touch
`.endless/db-ledger/`" check FALSE-POSITIVES on normal lands. Branches routinely
fork from `main` while its tip is the rolling *"Endless: record ledger entry"*
commit; when `main` later amends that tip, the branch's fork-point copy becomes an
orphan reachable from the branch but not `main` — it appears in `base..HEAD` and
touches the ledger by definition. Land's **Step 3.7** (`_drop_orphan_amendable_commits`,
E-1342) already silently drops exactly that class. The gate therefore runs **after
Step 3.7** and refuses only what survives — Step 3.7 remains the sole authority on
legitimately-discardable ledger commits; the gate does not re-implement its logic.

## Files

- `src/endless/worktree_cmd.py` — add the constant, the helper, and the gate.
- `tests/test_worktree_land_dbledger_gate.py` — new pytest for the helper (mirrors
  `tests/test_worktree_land_orphan_drop.py`).
- `tests/tasks/e-1736-verify.sh` — new isolated verify script (mandated).

## Implementation

### 1. Ledger-path constant

Near `AUTO_COMMIT_GLOBS` (`worktree_cmd.py:54`), add a single source of truth for
the ledger dir pathspec (Python has none today — only the `*.jsonl` glob):

```python
# E-1736: the DB ledger directory, as a git pathspec. A commit under here
# on a task branch violates the ledger routing policy (ledger entries are
# recorded on the main checkout only) and must never ride a land into main.
DB_LEDGER_DIR = ".endless/db-ledger"
```

### 2. Helper — enumerate surviving ledger-touching commits

Add alongside `_drop_orphan_amendable_commits` (same file, same `_git_run` idiom).
Restrict `git log` by pathspec so it returns *only* commits that touched the ledger
dir — no per-commit `show` walk needed:

```python
def _ledger_touching_commits(
    worktree_path: Path, base_branch: str
) -> list[tuple[str, str]]:
    """Return (sha, subject) for every commit in base..HEAD that modifies a
    file under the DB ledger dir. Empty list when none.

    Backstop to the ledger routing policy: ledger entries are auto-committed
    on the main checkout only, so a branch-side commit under DB_LEDGER_DIR is
    always wrong and would be rebased into main by land. Intended to run AFTER
    Step 3.7's orphan-drop, so legitimately-orphaned base ledger commits are
    already gone and only genuine offenders remain.
    """
    out = _git_run(
        ["log", "--reverse", "--format=%H %s",
         f"{base_branch}..HEAD", "--", DB_LEDGER_DIR],
        cwd=worktree_path,
    )
    commits: list[tuple[str, str]] = []
    for ln in out.stdout.splitlines():
        if not ln.strip():
            continue
        sha, _, subject = ln.partition(" ")
        commits.append((sha, subject))
    return commits
```

### 3. The gate — new Step 3.75, after 3.7, before 3.8

Insert between the Step 3.7 advisory echo (`worktree_cmd.py:1614`) and Step 3.8's
`_guard_dirty_worktree` (`:1616`). Refusal message names each offending commit, is
plain-language, and cites **no internal task IDs** (per house rule on user-facing
strings):

```python
        # Step 3.75 (backstop to the ledger routing policy): refuse if any
        # commit surviving Step 3.7 still touches the DB ledger. Step 3.7
        # already dropped the legitimately-orphaned base ledger commits;
        # anything still under DB_LEDGER_DIR is a genuine branch-side ledger
        # commit that would be rebased into main and corrupt shared history.
        offenders = _ledger_touching_commits(worktree_path, base_branch)
        if offenders:
            noun = "commit" if len(offenders) == 1 else "commits"
            listing = "\n".join(
                f"  {sha[:12]}  {subject}" for sha, subject in offenders
            )
            raise click.ClickException(
                f"cannot land {canonical}: the branch has {len(offenders)} "
                f"{noun} modifying the database ledger ({DB_LEDGER_DIR}/):\n\n"
                f"{listing}\n\n"
                f"Ledger entries are recorded on the main checkout, never on a "
                f"task branch — landing these would rebase a branch-authored "
                f"ledger segment into main and corrupt the shared database "
                f"history. Remove these commits from the branch before retrying "
                f"(inspect each with `git show <sha>`)."
            )
```

Step 4's existing rebase-failure message (which still mentions "db-ledger entry
or config.json") stays as a fallback for the config.json / genuine-conflict cases
— no change needed there.

## Why this shape

- **After Step 3.7, restricted by pathspec** — avoids the orphan false-positive
  without duplicating Step 3.7's exclusion logic; Step 3.7 stays the single
  authority on discardable ledger commits.
- **`git log … -- <pathspec>`** — one call returns exactly the offenders; no
  per-commit `show --name-only` walk.
- **Deterministic, pre-rebase** — closes the silent-clean-rebase hole that Step 4's
  conflict-only message misses.

## Testing

### pytest — `tests/test_worktree_land_dbledger_gate.py`

Mirror `test_worktree_land_orphan_drop.py`'s real-git-repo fixtures
(`repo_with_worktree`, `_create_task_branch`, `_commit`, `_amend_with_extra_line`).
Cover the helper directly:

1. **Clean branch → no offenders.** User commit only; `_ledger_touching_commits`
   returns `[]`.
2. **Standalone branch-side ledger commit → 1 offender.** Commit touching
   `.endless/db-ledger/x.jsonl` on the branch; returns 1 (sha, subject).
3. **Orphan false-positive → no offenders after Step 3.7.** Branch forks at an old
   ledger tip, `main` amends it (the E-1342 scenario). Run
   `_drop_orphan_amendable_commits` first (as land does), THEN
   `_ledger_touching_commits` — must return `[]`. This is the critical regression.
4. **Mid-branch ledger commit Step 3.7 won't drop → caught by gate.** User commit,
   then a ledger commit on top. Step 3.7 breaks at the user commit (drops nothing);
   the gate returns 1 offender.

### Verify script — `tests/tasks/e-1736-verify.sh`

Fully isolated throwaway repos (the E-1729 harness shape: `mktemp -d`, real
`git init` main + `git worktree add` branch, cleanup on exit; colored pass/fail
+ summary; exit 0 all-passed / 1 any-fail / 2 setup error). Full end-to-end
`endless worktree land` can't be exercised in isolation — `land_worktree` opens
with `config.default_db_to_main()` and resolves the *real* project root — so the
script drives the gate at the code-path level via `uv run python -c` importing
`_ledger_touching_commits` (and `_drop_orphan_amendable_commits` for the orphan
case), against the four scenarios above. It also runs the new pytest and the
existing `test_worktree_land_orphan_drop.py` as a no-regression check.

Handoff to Mike is exactly:

```
esu && ./tests/tasks/e-1736-verify.sh
```

## Out of scope / not doing

- No change to the Go event layer (E-1309 already guards *creation*; this guards
  *landing*).
- No rewrite of Step 4's fallback message.
- No auto-remediation of offending commits — the gate refuses and instructs; it
  does not rewrite the user's branch.

## Completion

- `endless task update E-1736 --status unverified --db main`.
- Report git/worktree side-effects + the return line; do not self-confirm.
