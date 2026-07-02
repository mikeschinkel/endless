# E-1714 — Verify the E-1586 cwd-outside-worktree gate still blocks every tool

## Context

E-1586 shipped a PreToolUse gate (`enforceClaimedCwd`, `internal/hookcmd/claude.go:1395`)
that refuses **every** tool for a claimed session whose working directory has drifted
outside its live worktree, directing the agent to `/cd` back in. Its status is `assumed`
(never verified end-to-end). The E-1712 incident — a claimed session's DB-routing edits
landing in the **main checkout** despite the gates — casts doubt on whether the gate
actually fires.

E-1714 resolves that doubt: reproduce the exact preconditions and confirm the gate fires,
then produce a committed verify script. The **outcome decides a fork**:

- **Gate fires** → E-1586 is sound; the incident was a claimed session whose cwd was
  *inside* its worktree writing to main via an **absolute path** (neither the cwd-gate nor
  the E-1346 unclaimed-block catches that). → E-1703 is the real gap to build.
- **Gate does NOT fire** → E-1586 has regressed; that is the bug.

This session delivers the reproduction + the answer + the script. It does **not** build
E-1703 (separate task, blocked-by E-1714) and does **not** touch E-1586 production code.

## How the gate works (verified by reading)

`handlePreToolUse` (`claude.go:445`) calls `enforceClaimedCwd` at `:470`, **before** the
write-tools filter — so it covers Bash/Read/etc., not just Write/Edit. It fires when ALL hold:

1. Project is **registered** (`claude.go:454` early-returns for anonymous projects).
2. `GetActiveSession(session_id)` returns a row with a non-NULL `active_task_id`.
3. `GetTaskStatus` is **non-terminal** (terminal set: confirmed/assumed/declined/obsolete/completed).
4. `WorktreePathForTask` finds an existing **directory** at
   `<project_root>/.endless/worktrees/e-<taskID>` (`worktree.go:14` — plain `os.Stat`, no git
   worktree, no DB `worktrees` table, no companion file needed).
5. No *other* live session owns the worktree lock (absent lock file → this branch is skipped).
6. `pathWithin(worktree, cwd)` is **false** → cwd is outside the worktree.

On fire → `blockToolUse` (`claude.go:546`) prints the `cdRedirect` message to **stderr** and
`os.Exit(2)`. The message contains the literal `/cd <absolute-worktree-path>` and `E-<taskID>`.

**DB-routing gotcha (decisive for isolation):** `endless-go hook ...` pins to the real
`~/.config/endless/endless.db` via `PinMainDB` (`cmd/endless-go/main.go:112`) **unless**
`--config-dir <dir>` is passed (`ConsumeDBContextFlag`). Firing the hook without it would
write to the real ledger and collide with the live session (per the "isolate hook
verification" lesson). So the script MUST invoke
`bin/endless-go --config-dir "$CFG" hook claude`. The Go binary auto-applies `schema.SQL`
(all `CREATE ... IF NOT EXISTS`) on first connect, so the temp DB self-materializes.

## What a "pre-E-1596" verify script is (from investigating E-1596 + children)

E-1596 is actively building the formal system: `endless verify <id>` runner (E-1603, underway)
reading `[[check]]` `verify.toml` manifests (E-1618) with temp-HOME/temp-XDG isolation,
normalizing to CTRF (E-1604), wrapped by `just verify` (E-1625). It isn't usable yet.

Until it is, the convention (stated verbatim in E-1603/E-1604/E-1618 plans) is a committed
**ad-hoc `tests/tasks/e-<id>-verify.sh`** modeled on `tests/tasks/e-1577-verify.sh` /
`e-1662-verify.sh`: `bash`, `set -u`, color-aware `section`/`report_pass`/`report_fail`/
`summary` helpers, `assert_*` assertions, an **ALL PASSED** summary, and **exit 0 all-pass /
1 any-fail / 2 env-setup problem**. It self-isolates (the framework's generic isolation
isn't available yet). E-1623 later retrofits these onto the harness; this one maps to a raw
`command` `[[check]]` (exit-code format).

**False-green guards (E-1612 completed, E-1692/E-1614 open) — mandatory:**

- **Specific-substring match on the block case.** A "refused" assertion that checks only
  `exit != 0` false-greens when the binary merely failed to launch. Assert exit **== 2**
  AND stderr contains the *specific* `/cd <worktree-abs-path>` line — so a "binary not found"
  can't masquerade as a gate-fire.
- **Positive controls / tripwire.** Pair the fire case with cases that must **pass through**
  (exit 0, no `/cd`), proving the gate discriminates rather than erroring on everything.
- **No stale binary.** Build and invoke **this worktree's** `bin/endless-go` by explicit path;
  assert it exists and is executable before any check.

## Deliverable

`tests/tasks/e-1714-verify.sh` — self-contained, following the interim convention above.

### Structure

1. **Preflight.** `git rev-parse --show-toplevel` → repo root; require `sqlite3`. Build the
   candidate binary (`go build -o bin/endless-go ./cmd/endless-go` from the worktree, or
   `just build`); assert `bin/endless-go` is executable (stale-binary tripwire). Create a temp
   root `T=$(mktemp -d)`, `CFG=$T/config/endless`, project dir `PROJ=$T/proj`.

2. **Seed the fixture (deterministic, direct SQL — mirrors the Go tests' INSERTs).**
   - Prime schema: fire one throwaway hook with `cwd=/tmp` (neutral, avoids auto-registering
     `$PROJ` as anonymous) so `$CFG/endless.db` is created + migrated.
   - `sqlite3 "$CFG/endless.db"` INSERTs:
     - `projects (name,path,status,...) VALUES ('demo','$PROJ','active',...)` → note id.
     - `tasks (project_id,title,status,type_id) VALUES (<pid>,'demo','underway',1)` → note id.
     - `sessions (session_id,project_id,active_task_id,state,kind_id) VALUES ('<UUID>',<pid>,<tid>,'working',1)`.
   - `mkdir -p "$PROJ/.endless/worktrees/e-<tid>"` (satisfies `WorktreePathForTask`).
   - Helper `fire_hook <cwd> <tool>` builds the payload JSON and runs
     `bin/endless-go --config-dir "$CFG" hook claude`, capturing stderr + exit code.

3. **Primary — gate FIRES (cwd outside worktree).** `fire_hook "$PROJ" Write` and
   `fire_hook "$PROJ" Bash` (proves ALL tools, not just writes): assert exit **== 2** AND
   stderr contains `/cd $PROJ/.endless/worktrees/e-<tid>` AND `E-<tid>`.

4. **Positive controls — gate PASSES (tripwire, E-1612).** Each asserts exit 0 (or ≠2) AND
   no `/cd` in stderr:
   - cwd == the worktree dir (invariant holds).
   - task status set to terminal `confirmed` (status keying).
   - worktree dir removed / task id with no dir (worktree-existence keying).
   - session `active_task_id` set NULL (no active task).

5. **Summary + exit codes** per convention (0/1/2).

### Notes on faithfulness

Direct-SQL seeding exercises the **real shipped gate code path in the real binary** end to end
(runClaude → handlePreToolUse → enforceClaimedCwd → blockToolUse/os.Exit). It skips the
`endless task claim` event pipeline only for the *fixture construction*, which is orthogonal to
what's under test (the gate reads DB rows + a directory; how they got there doesn't matter).
This matches how the Go tests seed (`claude_revisit_test.go` INSERTs projects/tasks/sessions).

## Files

- **New:** `tests/tasks/e-1714-verify.sh` (only new/changed file; executable `chmod +x`).
- **Read-only reference (no edits):** `internal/hookcmd/claude.go`, `internal/monitor/{session,worktree,files,db}.go`,
  `tests/tasks/e-1577-verify.sh` / `e-1662-verify.sh` (helper shape).

## Verification (of this task)

From the worktree:

1. `./tests/tasks/e-1714-verify.sh` → prints ALL PASSED, exit 0. The temp env is fully
   isolated (`--config-dir` temp DB); the real `~/.config/endless/endless.db` and the live
   session are untouched.
2. Confirm no real-DB / real-session side effects (the whole point of `--config-dir`).
3. **Record the fork outcome** in E-1714's handoff/outcome:
   - If ALL PASSED (gate fires — expected from the code read) → E-1586 **confirmed**; the
     E-1712 incident was the absolute-path-to-main case → E-1703 is the real gap. Report so
     Mike can unblock/build E-1703 (separate task; not built here).
   - If the fire assertions FAIL (gate does not fire) → E-1586 **regressed**; that is the bug.
     STOP and report; do not paper over it.
4. `endless task update E-1714 --status unverified --db main`; hand back how-to-test + the
   return line. Do not land/drop.
