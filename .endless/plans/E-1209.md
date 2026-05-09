# Implement E-1209 — fix `endless worktree land` cleanup of untracked `.endless/{worktree.json,worktree.lock}`

## Context

`endless worktree land <task-id>` succeeds at the merge step but its final `git worktree remove` consistently fails with `fatal: '<path>' contains modified or untracked files`, because two intentionally-untracked per-worktree state files survive the land:

- `.endless/worktree.json` — companion file written by `create_task_worktree()`. Should never be committed (committing it rotates content every time another worktree is created/destroyed).
- `.endless/worktree.lock` — ownership marker per E-971. SessionEnd hook (`cmd/endless-hook/claude.go:181-202`) is supposed to delete it via `monitor.ReleaseWorktreeLock`, but in practice the lock file survived on the E-1186 land (2026-05-09).

Land currently surfaces this as a yellow non-fatal warning at `src/endless/worktree_cmd.py:709-713` and prints "Manual cleanup: `git worktree remove ...`". The user sees a spurious error every land.

**E-1218** (sibling session, in flight at plan-write time) untracks both files from main and adds them to `.gitignore`. Empirical (verified in `/tmp` outside this repo): `git worktree remove` does NOT complain about gitignored-untracked files; only non-gitignored untracked files trigger the failure. So **E-1218 alone resolves the user-visible symptom** of E-1209.

E-1209 still ships because the underlying bugs are not symptom-cosmetic:

- **Layer A** (Python — `src/endless/worktree_cmd.py`): land owns its cleanup contract for known per-worktree state. With E-1218 in place this is defense-in-depth; without it (or in any state where another untracked file slips in) the contract still belongs to land. Layer A also adds the missing live-session refusal: today, running `endless worktree land E-NNN` while a Claude session is still actively working in the e-NNN worktree silently succeeds and strands that session.
- **Layer B** (Go — `cmd/endless-hook/claude.go`): the global hook's `shouldSkipForWorktree` early return at line 86-88 currently bypasses the entire SessionEnd handler when a session yields to a worktree-local binary. If the worktree-local binary fails to fire SessionEnd for any reason (missing `bin/endless-hook`, crash mid-execution, hook misconfiguration, kill -9 of the Claude process), the lock file leaks. The fix carves out lock-release as the one idempotent SessionEnd op the global *also* runs.

Layer B is the lock-leak root cause; Layer A is defense-in-depth and a separate live-session bug fix. They ship in one branch (different files, clean review).

Related: also see **E-1219** (audit `FindWorktreeRoot` walk-up termination at projectRoot) — separate fix site, surfaced during this work, not bundled.

## Critical files

- `src/endless/worktree_cmd.py` — Layer A. Land function at `land_worktree()` line 590-728. Cleanup change goes between Step 5 (ff-merge, line 692) and Step 6 (`git worktree remove`, line 706). New helper for liveness check.
- `cmd/endless-hook/claude.go` — Layer B. Add an "always-run SessionEnd lock-release" block before the `shouldSkipForWorktree` check at line 86. The existing SessionEnd lock-release at lines 193-198 stays (worktree-local binary continues to handle it; second release is a no-op via `os.Remove`'s ErrNotExist swallow).
- `internal/monitor/worktree_lock.go` — read-only here. Layer A's Python liveness check mirrors the `kill -0 <pid>` semantics of `IsWorktreeLockStale` at line 145-159. Layer B reuses `FindLockBySessionID` (line 179) and `ReleaseWorktreeLock` (line 163) unchanged.
- `.endless/plans/E-1209.md` — this file. Implementation must NOT edit it (per E-1199/E-1200).

## Decisions (locked)

### A1 — Cleanup: explicit delete, no `--force`

Land explicitly removes `.endless/worktree.json` and `.endless/worktree.lock` before `git worktree remove`. Never pass `--force`.

**Why:** Mike rejected `--force` because it can mask other untracked files that legitimately deserve user attention (loose edits, leftover scratch files). After explicit cleanup of the two known state files, any remaining `git worktree remove` failure now reflects a real problem the user should see, not noise from Endless's own state.

**How to apply:** Delete the lock file (after the liveness check below) and the companion file unconditionally. Then run `git worktree remove` (no `--force`). If it still fails, surface the existing yellow non-fatal warning — it now means an *unexpected* file is in the way.

### A2 — Stale-lock policy: refuse if alive, delete if stale

Before deleting `.endless/worktree.lock`:

1. If lock file is absent → skip lock deletion, proceed to companion deletion.
2. If lock file is present, parse JSON, check PID liveness via `os.kill(pid, 0)`:
   - **Process alive** (`os.kill` returns without error, or raises `PermissionError` — different uid is treated as alive, never reclaim) → **refuse land** with actionable message:
     > Worktree for <task-id> is owned by a live session (PID <pid>, session <session_id>, claimed at <claimed_at>). End that session before landing this task.
   - **Process gone** (`os.kill` raises `ProcessLookupError` / ESRCH) → delete lock + proceed.
   - **Invalid PID** (`pid <= 0` or unparseable lock JSON) → delete lock + proceed (treat malformed as stale).

**Why:** Matches E-971's existing `IsWorktreeLockStale` model in Go (`internal/monitor/worktree_lock.go:145-159`). A live session in the worktree is doing real work; clobbering its lock would strand it on next FS write. A stale lock is one of the symptoms we're fixing — reap and continue.

**How to apply:** Implement liveness check inline in `worktree_cmd.py` (5 lines of `os.kill`). Don't shell out to `endless` or import Go internals. Fail-closed on any liveness ambiguity (treat unknown errors as alive — never reclaim a lock we can't conclusively prove dead).

### B1 — Belt-and-suspenders: global runs SessionEnd lock-release even when yielding

In `cmd/endless-hook/claude.go`, before the `shouldSkipForWorktree` check at line 86, add a SessionEnd-only block that runs lock-release unconditionally. Worktree-local binary still runs the full SessionEnd handler (companion remove, EndSession, lock release). Both binaries calling `ReleaseWorktreeLock` is safe — `os.Remove` swallows ErrNotExist (line 165 in worktree_lock.go).

**Why:** Layer B's existing skip is correct for state-mutating handlers (RecordActivity, RemoveCompanion, EndSession). Lock release is idempotent — running it in both binaries is safe and trades nothing for substantial robustness against the worktree-local binary failing for any reason. The cost is one extra `os.ReadDir` + `os.Remove` per SessionEnd in the global, which is microseconds.

**How to apply:** Insert before line 86:
```go
// Belt-and-suspenders for E-971's worktree lock release. The
// worktree-local binary handles SessionEnd's full lifecycle; if it
// fails to fire (missing, crashed, misconfigured), the lock leaks
// and future sessions can't claim the worktree. ReleaseWorktreeLock
// is idempotent (os.Remove swallows ErrNotExist), so running it in
// both binaries is safe. Other SessionEnd ops stay single-fire in
// the worktree-local handler. (E-1209)
if payload.EventName == "SessionEnd" {
    if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
        if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
            log.Printf("global SessionEnd lock release at %s: %v", wtPath, err)
        }
    }
}
```

### B2 — Add diagnostic log line in worktree-local SessionEnd path

In the existing SessionEnd lock-release at `cmd/endless-hook/claude.go:193-198`, add an info-level log on success so future failures are diagnosable:
```go
if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
    if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
        log.Printf("releasing worktree lock at %s: %v", wtPath, err)
    } else {
        log.Printf("released worktree lock at %s for session %s", wtPath, payload.SessionID)
    }
}
```

**Why:** The E-1186 lock-leak has no smoking-gun cause in current logs. Confirming when SessionEnd ran successfully (and when it didn't fire at all) requires the success-path log. Without this, the next leak is again unattributable.

## Implementation steps

Order: B first (root cause), then A (defense-in-depth + live-session refusal). Both changes ship in branch `task/1209-fix-land-cleanup`.

### Step 1 — Layer B: belt-and-suspenders SessionEnd lock-release in global hook

1.1. In `cmd/endless-hook/claude.go`, insert the unconditional SessionEnd lock-release block (per B1 above) immediately before line 86 (the `shouldSkipForWorktree` check).

1.2. Add the success-path log line in the worktree-local SessionEnd handler (per B2 above), at line 193-198.

1.3. `just build` to rebuild `bin/endless-hook` in the worktree.

1.4. Quick sanity: `bin/endless-hook` runs without panicking on a synthetic SessionEnd payload (echo a minimal JSON; lock-release path should be a no-op when no lock matches).

### Step 2 — Layer A: pre-cleanup in `endless worktree land`

2.1. In `src/endless/worktree_cmd.py`, add a small helper near the other underscore-prefixed helpers in this module:

```python
def _check_worktree_lock_liveness(worktree_path: Path) -> tuple[str, dict | None]:
    """Inspect <worktree>/.endless/worktree.lock for liveness (E-1209).

    Returns (state, lock_data) where state is one of:
      - "absent":    no lock file
      - "alive":     lock owner's PID responds to kill(pid, 0)
      - "stale":     PID is gone (ESRCH) or invalid
      - "malformed": file present but unparseable

    Mirrors `monitor.IsWorktreeLockStale` semantics from E-971 — never
    reclaim a lock we can't conclusively prove dead.
    """
    lock_path = worktree_path / ".endless" / "worktree.lock"
    if not lock_path.exists():
        return ("absent", None)
    try:
        data = json.loads(lock_path.read_text())
    except (json.JSONDecodeError, OSError):
        return ("malformed", None)
    pid = data.get("pid")
    if not isinstance(pid, int) or pid <= 0:
        return ("malformed", data)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return ("stale", data)
    except PermissionError:
        # Different uid — treat as alive, never reclaim.
        return ("alive", data)
    except OSError:
        # Conservative: unknown error = alive.
        return ("alive", data)
    return ("alive", data)
```

(Imports: `os` and `json` are already imported at the top of the file. Verify before adding.)

2.2. In `land_worktree()` between Step 5 (ff-merge, line 702) and Step 6 (`git worktree remove`, line 706), insert the pre-cleanup block:

```python
# Step 5.5: pre-cleanup of per-worktree state files (E-1209). Without
# this, `git worktree remove` fails on .endless/worktree.{json,lock}
# whenever they aren't gitignored (E-1218 is the gitignore fix; this
# is land's own cleanup contract).
state, lock_data = _check_worktree_lock_liveness(worktree_path)
if state == "alive":
    raise click.ClickException(
        f"Worktree for {canonical} is owned by a live session "
        f"(PID {lock_data['pid']}, session {lock_data.get('session_id', '?')}, "
        f"claimed at {lock_data.get('claimed_at', '?')}). End that session "
        f"before landing this task."
    )
# Stale, absent, or malformed: safe to delete both files.
lock_path = worktree_path / ".endless" / "worktree.lock"
companion_path = worktree_path / ".endless" / "worktree.json"
for p in (lock_path, companion_path):
    try:
        p.unlink(missing_ok=True)
    except OSError as e:
        # Don't block land on a cleanup error — let `git worktree remove`
        # surface whatever's actually wrong.
        click.echo(
            click.style("•", fg="yellow")
            + f" pre-cleanup: failed to remove {p}: {e} (continuing)"
        )
```

2.3. Leave Step 6 (`git worktree remove`) as-is (no `--force`). Leave the existing yellow-warning failure path at lines 707-713 — it now fires only on truly unexpected leftover files.

### Step 3 — Build, install, smoke-test

3.1. From the worktree: `just build` (Go binaries) — refreshes `bin/endless-hook` for both layers' code.

3.2. From the **main checkout** (per "Never run just install from a worktree" memory): `just install`. This installs the Python CLI in editable mode and rebuilds Go binaries to `/usr/local/bin/`.

3.3. Smoke-test land in a scratch worktree (see Verification below).

## Verification

All three scenarios MUST be exercised by the implementer before flipping E-1209 to `verify`. The implementer should run them in a separate scratch worktree (NOT this E-1209 worktree, to keep the test isolated from the change under test).

### V1 — Happy path: clean land of a freshly-spawned task

1. Create a throwaway task: `endless task add 'E-1209 verify-1' --status ready`. Note the returned ID, e.g., E-1300.
2. Spawn a worktree: `endless task spawn E-1300` (or manually `git worktree add` + `endless task start`).
3. Inside the worktree: make a trivial commit (`echo 'verify' >> README.md && git add README.md && git commit -m verify`).
4. End the Claude session cleanly so SessionEnd fires (`/quit` or terminal close that triggers SessionEnd hook).
5. From main checkout: `endless worktree land E-1300`.
6. **Pass criteria:**
   - Land prints green checkmark, no yellow warning.
   - `git worktree list` no longer includes the e-1300 path.
   - `.endless/worktrees/e-1300/` directory does not exist.
   - Branch `task/1300-...` is deleted (`git branch | grep 1300` returns nothing).

### V2 — Stale lock survival path: SessionEnd never fired

1. Repeat V1 steps 1-3 to create another throwaway task and worktree.
2. Inside the worktree's Claude session, do NOT cleanly end. Instead simulate the bug: in a separate terminal, find the Claude PID (e.g., `pgrep -f claude-code`), then `kill -9 <pid>`. Terminal closes without SessionEnd firing.
3. Confirm `.endless/worktrees/e-NNN/.endless/worktree.lock` still exists (`ls .endless/worktrees/e-NNN/.endless/`).
4. From main checkout: `endless worktree land E-NNN`.
5. **Pass criteria:**
   - Lock's PID is dead → policy classifies as "stale" → land deletes lock + companion + proceeds.
   - `git worktree remove` succeeds without error.
   - Same FS clean-state as V1.

### V3 — Live session: refuse land with actionable error

1. Repeat V1 steps 1-3, but leave the Claude session in the worktree alive.
2. From a separate terminal in main checkout: `endless worktree land E-NNN`.
3. **Pass criteria:**
   - Land refuses with the message: "Worktree for E-NNN is owned by a live session (PID X, session Y, claimed at Z). End that session before landing this task."
   - Exit code is non-zero.
   - Worktree dir is unchanged (lock file still in place, no deletions performed).
   - The worktree's Claude session continues to function normally afterward.
4. End the Claude session cleanly. Re-run `endless worktree land E-NNN`. Should now succeed (B's belt-and-suspenders should have released the lock; A finds it absent and proceeds).

### V4 — Belt-and-suspenders observability

1. Trigger a clean SessionEnd in any test worktree.
2. Inspect the global hook's stderr/log output for the "released worktree lock at <path> for session <id>" line from B2.
3. **Pass criteria:** the success log appears, confirming SessionEnd's lock-release path executed.

### V5 — Diff `git worktree list` before and after

For V1 and V2, capture `git worktree list` output before the land and after. The landed worktree must disappear from the list with no manual `git worktree remove --force` or `git worktree prune` required.

## Out of scope

- **E-1218** (untrack + gitignore `.endless/worktree.{json,lock}`): companion fix that resolves the user-visible symptom of E-1209 by making the two files gitignored. Different fix site (`.gitignore` + `git rm --cached`), shipping in parallel via the sibling session. E-1209's Layer A is defense-in-depth atop E-1218; Layer B is independently necessary regardless.
- **E-1219** (audit `FindWorktreeRoot` walk-up termination at projectRoot): surfaced during E-1209 research when my own session at SessionStart claimed a lock on main itself due to E-1218's tracked-stale companion. The walk-up logic should be audited to ensure it never invents a worktree at projectRoot. Separate fix site (`internal/monitor/worktree_lock.go:220-245`), out of E-1209's land + SessionEnd scope.
- **E-1009** (gitignore the two files): superseded by E-1218 — same intent, E-1218 is the live ticket.
- **E-1195** (review whether the lock-file model itself stays): the architectural question of whether E-971's filesystem lock is the right primitive at all. E-1209 fixes the bugs under the *current* model.
- **E-1217** (`task update --project` flag): a separate CLI gap noticed during this work; not folded in.
- **Porting `endless worktree land` (or any `worktree_cmd.py` verb) to Go**: confirmed in chat 2026-05-09 — Python stays for now; the global Python-to-Go migration owns the larger move.
- **Restructuring the global/worktree-local hook split**: Layer B's belt-and-suspenders pattern is intentionally narrow — only SessionEnd's idempotent lock-release runs in both binaries. Other event handlers' single-fire semantics are unchanged.
- **Diagnosing the specific E-1186 lock leak retrospectively**: no smoking-gun logs survive. B2's success-path log makes the next leak attributable; we don't try to forensically reconstruct E-1186's cause here.
