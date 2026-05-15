# Implement E-1342 — auto-detect and drop orphan auto-amend commits during worktree land

## Context

`canAmend` (`internal/events/commit.go:185`) lets ledger and snapshot auto-commits *amend in place* when the previous commit on HEAD has the same subject and hasn't been pushed. The result: main carries one rolling ledger commit and one rolling snapshot commit per "run", their SHAs rewritten each time a new event/snapshot is appended.

When a task branch forks off main at moment T, its base picks up whatever SHA the ledger/snapshot commits had at T. If a later event amends those commits on main, the branch's base SHA becomes orphaned — unreachable from main, but still anchored on the branch. `git log main..task-branch` then shows the orphan as a commit ahead of main, and `endless worktree land`'s `git rebase main` (Step 4 at line 855 of `worktree_cmd.py`) tries to replay it, hitting a content conflict on the same `.endless/db-ledger/*.jsonl` or snapshot file whose superset already exists in main's amended commit.

Observed manually three times so far:
- 2026-05-14: E-1333's land (orphan `0514e4a`) — recovered via `git rebase --onto main 0514e4a HEAD`.
- 2026-05-15: E-1219's land (orphan `6ed7aca`) — same manual `rebase --onto`.
- 2026-05-15: E-1238's land (orphan `a6d2491`) — same manual `rebase --onto`.

**E-1309 is complementary.** E-1309 (`internal/events/commit.go` env sanitation + worktree-root refusal) prevents auto-commits from being *misrouted* to a worktree branch's gitdir. That stops the *creation* of rogue commits ON the branch. It does NOT stop main's amend-in-place flow from *orphaning* a base SHA that the branch happens to point at. E-1342 addresses the residual class.

## Critical files

- `src/endless/worktree_cmd.py:775-934` — `land_worktree`. Insert the orphan-drop pre-step between Step 3.5 (dedup, line 846) and Step 4 (rebase, line 855).
- `internal/events/commit.go:23-29` — source of truth for the two amendable subjects: `LedgerCommitSubject` ("Endless: record ledger entry") and `SnapshotCommitSubject` ("Endless: snapshot plan"). Python mirrors these as a module-level tuple; the values are tiny strings unlikely to drift, but mirroring is documented as the contract.
- `tests/test_worktree_land_orphan_drop.py` — new Python test file under the existing `tests/` directory; covers the detection + drop end-to-end against a synthetic git repo fixture.

## Decisions (locked)

### D1 — Cover both amendable subjects, not just ledger

Drop orphans whose subject is *either* `LedgerCommitSubject` or `SnapshotCommitSubject`. The task title and description name ledger only, but the same `canAmend` mechanism applies to snapshots (`commit.go:29`), so the same orphan class exists for them. Treating them symmetrically with one code path is simpler than handling ledger now and snapshots later.

### D2 — Contiguous-at-base detection only

Detect orphans by walking commits in `main..task-branch` in topological order, oldest-first. Drop the longest *contiguous-from-the-base* run whose subjects are all in the amendable-subject set. Stop at the first non-amendable commit; everything from there to HEAD is preserved.

**Why contiguous-at-base only:** in any post-E-1309 worktree, auto-commits can no longer fire ON the worktree's branch — they route to main. So an orphan can only enter a branch's history at fork-time (when the branch was created) or at the moment of a previous manual `rebase --onto`. In both cases the orphan sits at the BASE of the branch. Mid-branch orphans would require a pre-E-1309 contamination AND user commits on top of it; treating those as orphans risks dropping commits the user actually wanted. If interleaved orphans ever surface, that's a separate bug needing separate triage.

### D3 — Single `git rebase --onto` for the drop, not interactive rebase

If N contiguous orphans are detected (N ≥ 1), one `git rebase --onto <base> <Nth-orphan-sha> HEAD` drops all of them in a single rebase. Cheaper, simpler, and atomic (one operation either succeeds or fails) compared to `git rebase -i` with N drop entries.

### D4 — Mirror the amendable-subject list in Python, do not shell out

Add a module-level tuple in `worktree_cmd.py`:
```python
# Mirrors internal/events/commit.go:23,29 (E-1342). Subjects whose
# auto-commits can amend in place via canAmend, producing orphans
# when main amends past a branch's fork-point SHA.
AMENDABLE_COMMIT_SUBJECTS = (
    "Endless: record ledger entry",   # LedgerCommitSubject
    "Endless: snapshot plan",         # SnapshotCommitSubject
)
```

**Why mirror rather than shell out to Go:** the values are 2 stable strings already inlined in commit.go; introducing an `endless-sandbox subjects` JSON RPC for two constants would be ceremony. The comment cites the Go source so future drift is obvious during code review.

### D5 — Silent on no-op, visible on action

If the detection finds zero orphans, the pre-step is silent (no output). If it finds one or more, print a single yellow line summarizing the drop:
```
• Dropped 1 orphan auto-amend commit (Endless: record ledger entry, was 6ed7aca)
```
Per the existing land output style (yellow bullet for advisory notes, green for success). Helps confirm the new step did its job without flooding stdout in the happy path.

## Implementation

### Step 1 — Add the amendable-subjects tuple

In `src/endless/worktree_cmd.py`, near the existing constants (`AUTO_COMMIT_GLOBS` around line 51), add:
```python
AMENDABLE_COMMIT_SUBJECTS = (
    "Endless: record ledger entry",
    "Endless: snapshot plan",
)
```
With the comment from D4.

### Step 2 — Add `_drop_orphan_amendable_commits` helper

A small private helper near the other underscore-prefixed helpers (`_check_worktree_lock_liveness` was added in E-1209; new one goes nearby):

```python
def _drop_orphan_amendable_commits(
    worktree_path: Path, base_branch: str
) -> tuple[int, str | None]:
    """Drop contiguous orphan auto-amend commits at branch base (E-1342).

    Returns (count_dropped, first_subject) — count is 0 when no orphans
    found and the helper is a no-op. first_subject is the subject of
    the oldest dropped commit, for the advisory log.
    """
    # Enumerate main..HEAD oldest-first.
    out = _git_run(
        ["log", "--reverse", "--format=%H %s", f"{base_branch}..HEAD"],
        cwd=worktree_path,
    )
    lines = [ln for ln in out.stdout.splitlines() if ln.strip()]
    if not lines:
        return (0, None)

    last_orphan_sha = None
    first_subject = None
    for line in lines:
        sha, _, subject = line.partition(" ")
        if subject in AMENDABLE_COMMIT_SUBJECTS:
            last_orphan_sha = sha
            if first_subject is None:
                first_subject = subject
        else:
            break  # contiguous-at-base only (D2)

    if last_orphan_sha is None:
        return (0, None)

    # One rebase --onto drops the entire contiguous run.
    _git_run(
        ["rebase", "--onto", base_branch, last_orphan_sha, "HEAD"],
        cwd=worktree_path,
    )
    # Count = number of orphan commits processed.
    n = 0
    for line in lines:
        if line.split(" ", 1)[1] in AMENDABLE_COMMIT_SUBJECTS:
            n += 1
        else:
            break
    return (n, first_subject)
```

### Step 3 — Insert pre-step in `land_worktree`

Insert between Step 3.5 (line 854, end of verbs dedup) and Step 4 (line 855, rebase). New "Step 3.7":

```python
# Step 3.7: drop orphan auto-amend commits at branch base (E-1342).
# canAmend in commit.go rewrites the ledger/snapshot commit SHAs on
# main as new events arrive; a branch forked off the old SHA carries
# an orphan that conflicts on rebase. Detect and strip before Step 4
# so the rebase sees only the user's real commits.
try:
    n_orphans, first_subj = _drop_orphan_amendable_commits(
        worktree_path, base_branch
    )
except subprocess.CalledProcessError as e:
    # The pre-step's own rebase --onto failed (rare — implies a real
    # conflict between an orphan and the branch's user commits).
    # Surface the failure rather than masking it.
    _git_run(["rebase", "--abort"], cwd=worktree_path, check=False)
    raise click.ClickException(
        f"orphan auto-amend cleanup failed: {(e.stderr or '') + (e.stdout or '')}"
    )

if n_orphans:
    last_sha = "(none)"  # filled in by helper-returned context below if needed
    click.echo(
        click.style("•", fg="yellow")
        + f" Dropped {n_orphans} orphan auto-amend "
        + ("commit" if n_orphans == 1 else "commits")
        + f" ({first_subj})"
    )
```

*(Drop the trailing-SHA line if it adds noise; the count + subject is enough for the user to understand what happened. Final wording can be tightened during implementation.)*

### Step 4 — Test

`tests/test_worktree_land_orphan_drop.py`:

1. **Single-orphan happy path:** synthesize a git repo with a `main` branch carrying a `Endless: record ledger entry` commit at HEAD; create a task branch off that ledger commit; add one user commit on the task branch; on main, amend the ledger commit so its SHA changes (simulate `canAmend` flow); call the helper; assert (a) returned count == 1, (b) `main..task-branch` now contains only the user commit, (c) rebase on top of new main is a no-op (no conflict).

2. **Two contiguous orphans:** plant both a ledger orphan AND a snapshot orphan at the base of the task branch; assert both get dropped, only user commit survives.

3. **Mixed (orphan then user then potential-orphan-by-subject):** ledger orphan at base, user commit, then a commit whose subject happens to be `Endless: snapshot plan` (this is unlikely in practice but is the "non-contiguous" guard rail); assert ONLY the base orphan is dropped, the mid-branch snapshot-subject commit is preserved.

4. **No orphans:** task branch with only user commits; helper returns (0, None), no rebase happens, no error.

5. **Branch at main (no commits):** task-branch == main; helper returns (0, None) and is a no-op.

## Verification

- **Unit:** `just test` (or `pytest tests/test_worktree_land_orphan_drop.py -v`) — all 5 cases pass.
- **Integration:** create a scratch task, claim its worktree, simulate the orphan by `git reset --hard <prev-ledger-sha>` followed by re-adding a commit, then run `endless worktree land <task>`. Confirm:
  - The yellow `Dropped N orphan auto-amend commits ...` line prints once.
  - The normal Step 4 rebase succeeds without conflict.
  - The task lands cleanly with no further intervention.
- **Regression smoke:** land any task without orphans (e.g., a freshly-claimed task with a trivial commit). The pre-step prints nothing, land behaves identically to current main.

## Out of scope

- **Mid-branch orphans** — non-contiguous-at-base orphans (a user commit sandwiched between two amendable subjects). Per D2, these are not treated as orphans here. If they ever surface in practice, file a follow-up.
- **Verb-registration commits** ("Endless: register verb 'X'") — these do NOT amend (each registration is its own commit); not in the amendable-subjects set; not orphan-prone.
- **`Endless: auto-record session activity` commits** — produced by land's own Step 3, not by `canAmend`. They land on main as the user's auto-commit and do not amend.
- **Removing the amend-in-place pattern entirely** — that'd be E-1342's "alternative B" (reconsider amend-in-place), much larger scope, not chosen here. Amend-in-place is good for keeping main's log readable; the orphan drop just patches its one downside.
- **Migrating land to Go** — separate ongoing effort; this fix lives in Python because land does today.
- **Auto-detecting + auto-fixing during a FAILED rebase**, instead of pre-empting it — would mean handling the rebase abort + diagnosis + retry in the error path. Pre-stepping is cleaner.
