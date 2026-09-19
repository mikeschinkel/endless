# E-1646 — `task respawn` alias + `session respawn <id> | --task E-NNN` entry points

**Epic:** E-1644 · **Blocked by:** E-1645 · **Layer:** Python CLI

Pure surface work on top of the E-1645 reopen core. The whole reason this exists is
**discoverability** — `respawn` was reached for and `--help` showed nothing.

## Files
- `src/endless/cli.py`: add `endless task respawn` near the spawn def (1559-1598); add
  `endless session respawn` to the session group (after ~line 926, before the `session status`
  subgroup at 928).
- `src/endless/task_cmd.py`: thin `task_respawn()` wrapper → `spawn_plan(..., reopen=True)`.
- `src/endless/session_cmd.py`: `session_respawn()` resolver + dispatch.

## Commands
### `endless task respawn <id>`
Exact alias for `endless task spawn <id> --reopen`. Pass through `--bg`, `--worktree`,
`--new-session`, `--force`. No new logic.

### `endless session respawn <session-id>`
Resolve the session row → its `active_task_id` + its **worktree path** (recorded cwd /
`worktree.json` / convention; may be an alt `e-<id>-<slug>`). Call the E-1645 reopen core
anchored via `--worktree <that path>`. Same liveness guard (navigate if a session is live).

### `endless session respawn --task E-NNN`
Resolve the **most-applicable** session via the E-1645 resolver (live-first; else newest ended
with `dur>=10s` OR populated `process`/`transcript_path`; **skip sub-10s ghosts**), then proceed
as `session respawn <id>`. `<session-id>` and `--task` are mutually exclusive; exactly one required.

## Notes
- Keep these wrappers dumb: all real behavior (guard, ensure-worktree, resolver, handoff) lives
  in E-1645's core so the two surfaces can't drift.

## Verification — create a per-task verify script
Create `tests/tasks/e-1646-verify.sh` in the `e-1624-verify.sh` shape (bash, `set -u`, the
`section`/`report_*`/`summary` helpers, `cd` repo root, ensure `go.work`, exit 0/1/2). Drive the
real CLI + the E-1645 dry-run seam against sandbox fixtures.

The script asserts (named checks):
- `endless task respawn --help` and `endless session respawn --help` exist (subcommands registered).
- `endless task --help` and `endless session --help` list `respawn`.
- `endless task respawn E-N` produces the **same** reopen decision as `endless task spawn E-N
  --reopen` (assert via the shared dry-run/print seam — identical resolved decision + worktree).
- `endless session respawn <id>` resolves to **that** session's worktree: seed an alt
  `e-<id>-<slug>` worktree for the session and assert the dry-run anchors there.
- `endless session respawn --task E-N` on a task with a seeded sub-10s ghost + a real session
  selects the **real** session.
- `--session-id` and `--task` together is rejected.

Re-runnability (sandbox NOT wiped between runs):
- Derive all seeded ids from freshly-allocated ids each run — never a fixed UNIQUE value.
- Capture seeding output; `exit 2` loudly on any setup/seed error (never /dev/null).
- Run the script at least TWICE before declaring it done.

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1646-verify.sh
```
