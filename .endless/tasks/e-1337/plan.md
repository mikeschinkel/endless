# E-1337: Stop deleting worktrees at land; record landings in DB; reap by sweep

## Problem

`endless worktree land` calls `git worktree remove` (see the land flow in `src/endless/worktree_cmd.py`), which deletes the worktree directory. This breaks the **currently-running Claude session** whose cwd is the worktree (the dir disappears underneath it; the active session, not just future `claude --resume` attempts, is what breaks). Hit during E-1281's land — the dir had to be recovered from IntelliJ local history. Suspected related to E-1372's "SessionStart hook did not re-register after worktree-delete-on-land" symptom.

## Approach

**Don't delete anything at land time.** The cleanup at the end of land was incidental and is what introduced the bug.

Land flow becomes:
1. Rebase the branch onto main.
2. ff-merge into main.
3. `INSERT INTO task_landings(...)` recording the event.
4. (Branch stays. Worktree dir stays. Nothing is removed.)

Re-landing (when a bug is found post-land in the same task) is the same flow: new commit on the branch, rebase, ff-merge, INSERT another row. `task_landings` is append-only history.

## Schema addition

```sql
CREATE TABLE task_landings (
  id INTEGER PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  session_id INTEGER NOT NULL REFERENCES sessions(id),
  branch TEXT NOT NULL,
  merge_commit_sha TEXT NOT NULL,
  landed_at TIMESTAMP NOT NULL
);
```

Migration follows existing endless DB migration patterns.

## Reaper sweep

A sweep that periodically removes stale worktrees that no live process is using.

**Detection logic** (per dir in `.endless/worktrees/*`):
1. Get `task_id` from the dir name (`e-NNNN` → `E-NNNN`).
2. `SELECT MAX(landed_at) FROM task_landings WHERE task_id = ?`
   - No rows → in-progress task, not landed yet → skip.
3. If `MAX(landed_at) > config.worktree_ttl ago` AND `lsof -d cwd +D <dir>` shows no live process with cwd in the dir → eligible to reap.

**Reap action:**
- `git worktree remove --force <dir>` (the supported git command — handles `.git/worktrees/<id>/` cleanup)
- `git branch -D <branch>` (branch name from the latest `task_landings` row)
- Nothing else (no marker file to clean up; no archive to remove)

**Pre-existing orphan dirs** (the ~10 currently in `.endless/worktrees/` from before this work, with no entries in `task_landings`) are skipped — the reaper only touches dirs whose task has at least one landing record.

## Configuration

Add `worktree_ttl` to `.endless/config.json` project config. Default: 14 days. Format: duration string (`"14d"`, `"24h"`, etc.) or integer seconds — implementer's call, document the choice.

## Sweep trigger points

Sweep runs (cheaply) at these invocations rather than as a background daemon:
- `endless-hook` firings (PostToolUse, PreToolUse, etc.) — picks up the routine cases
- `endless task claim`
- `endless task spawn`
- `endless worktree land`

Daemon is a future concern, not in scope here.

## lsof caveat

`lsof -d cwd +D <dir>` is fast enough for the current worktree count (~25 dirs). It can emit warnings on stat() failures for unrelated filesystems, e.g.:

```
lsof: WARNING: can't stat() smbfs file system /Volumes/.timemachine/...
    Output information may be incomplete.
    assuming "dev=36001a9b" from mount table
```

These warnings are noise for our use case. Filter known warning patterns from stderr (or `2>/dev/null`); rely on the exit code and stdout to determine whether the dir has live processes. Do not let warnings about unrelated paths affect the reap decision.

## E-1426 has landed

E-1426 (companion-file elimination, sessions.process UPSERTs on every hook) is in main. The reaper's process-detection can rely on `lsof` directly without needing to inspect any companion files. No interaction with companion-file plumbing needed.

## Related and downstream

- **E-1372** (`SessionStart hook did not re-register Claude session after worktree-delete-on-land`): downstream of this root cause. Once this lands, run E-1372's repro to confirm whether the SessionStart-on-reincarnate gap resolves structurally or whether a separate fix remains.
- **E-1281**: the task whose land first surfaced the bug.
- **E-1421** (curated 'next' list): blocked on this task; unblocks once this lands.
- **Post-MVP — undo**: a future feature that reverses a land. The "don't delete anything" approach in this task makes undo feasible (the branch, worktree, and tip refs are all still there). Undo does NOT delete `task_landings` rows — that's append-only history; undo would add a separate record (design TBD).

## Verification

1. From a worktree with an active Claude session (cwd = worktree path):
   - Run `endless worktree land <id>`.
   - Land succeeds; the dir still exists; the branch still exists; `git worktree list` still shows it; a new row appears in `task_landings`.
   - The active Claude session continues without disruption — `pwd` still resolves; subsequent commands work; no SessionEnd fires.

2. Re-land scenario:
   - In the same worktree, make a new commit (a bug fix).
   - `endless worktree land <id>` again.
   - Second row appears in `task_landings`; first row preserved.

3. Reaper sweep:
   - Worktree with `MAX(landed_at)` younger than `worktree_ttl` → does NOT reap.
   - Advance time (or lower `worktree_ttl` for the test) → reaper runs at next trigger, reaps successfully (worktree removed, branch deleted).
   - Worktree with `MAX(landed_at)` older than `worktree_ttl` but a process has cwd inside → does NOT reap.
   - Pre-existing orphan dir (no rows in `task_landings`) → does NOT reap; logged for awareness only.

4. lsof stderr filter:
   - Run the reaper on a machine with dead mount-table entries; warnings appear in lsof stderr but do not affect the reap decision.

## Instructions for the implementing session

1. Read this prompt + `endless guide` + relevant code (`src/endless/worktree_cmd.py`'s `land` flow).
2. Write the plan file at `.endless/plans/E-1337.md` covering: schema migration, code touchpoints in worktree_cmd.py, reaper module location, sweep trigger wiring, config schema addition, test strategy.
3. Attach via `endless task update E-1337 --text .endless/plans/E-1337.md` (auto-promotes to `ready`).
4. Implement. Tests required for: land happy path, re-land, reaper grace-period boundary, reaper live-process protection, lsof warning tolerance.
5. Flip to `verify` when done, including the verification block above in the handoff message.
