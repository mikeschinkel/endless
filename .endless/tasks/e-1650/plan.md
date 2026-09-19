# Plan: `esp` returns to project root without an active session

## Context

After exiting a completed Claude session, the shell is left sitting inside the
worktree (e.g. `~/Projects/endless/.endless/worktrees/e-1542`). A very common
next move is to return to the project root. `esp` is the natural helper for
that, but it fails:

```
$ esp
esp: no active session, run 'esu <id>' first
```

The guard in the `esp()` shell function (cli.py:578) bails out whenever there's
no `ENDLESS_SESSION_ID` and no argument — but resolving the **project root**
doesn't actually need a session. It's a property of the current working
directory. The fix makes `esp` (with no session and no arg) fall back to
resolving the project root from cwd, so leaving Claude and typing `esp` lands
you at the project root.

We reuse the existing, robust `_project_root_for_cwd()` (session_cmd.py:1103),
which walks up from cwd looking for a registered project — from a worktree it
finds the parent `endless` checkout. This is strictly better than a naive
`cd ../../..` and adds no extra complexity, since `session_cd_resolve` already
calls that function at its top.

## Changes

### 1. `src/endless/session_cmd.py` — `session_cd_resolve` (line ~1319)

`project_root = _project_root_for_cwd()` is already computed at the top of the
function (line 1336). Add an early branch: when `target == "project"` **and**
`session_ref is None`, short-circuit and print that project root instead of
requiring a session/companion to resolve.

Rationale: for `--target project` with no explicit session-ref, the answer is
identical regardless of which sibling session would auto-resolve (siblings live
in the same project). So this is equivalent for the tmux-sibling case and
additionally handles the no-session, post-exit case. Explicit `esp <id>` still
flows through `_resolve_companion` so a specific session in a *different*
project resolves correctly.

Sketch (insert before the `_resolve_companion` call at line 1348):

```python
# --target project with no explicit session-ref: the project root is a
# property of cwd, not of any session, so resolve it directly. This lets
# `esp` return to the project root even with no active session (e.g. after
# the Claude session has exited). Equivalent to sibling auto-resolution for
# the in-tmux case since siblings share the project. (E-NNNN)
if target == "project" and session_ref is None:
    click.echo(str(project_root))
    click.echo(
        f"• cwd → {_short_path(str(project_root))} (project root)",
        err=True,
    )
    return
```

(`_short_path` is the existing home-`~` shortener at session_cmd.py:1168;
status goes to stderr so stdout stays a clean `cd` target.)

### 2. `src/endless/cli.py` — `esp()` shell helper (lines 578-586)

Drop the no-session precondition guard so `esp` proceeds to call
`_endless_run session cd --target project`. With no `ENDLESS_SESSION_ID`,
`_endless_run` calls the bare `endless --db main session cd --target project`,
which now returns the cwd's project root.

New body:

```sh
# esp — cd into the project root.
#   esp          → project root of the current session, or (no session)
#                  the project root resolved from cwd
#   esp <id>     → project root of an explicit endless id / UUID prefix
esp() {
    local target
    target="$(_endless_run session cd --target project "$@")" || return $?
    cd "$target"
}
```

(`esf` keeps its own guard — it genuinely needs a session to forget.)

## Automated tests (pytest)

### `tests/test_session_cd.py`
Add a test: with cwd inside a registered project (use the existing
`registered_with_sessions` fixture, which `monkeypatch.chdir`s into the
project) and **no** live sessions, `session_cd_resolve(None, target="project")`
prints the project root rather than erroring. Mirror the assertion style of
`test_target_project_returns_project_root` (line 259). Add a second case from a
nested subdir to confirm the `_project_root_for_cwd()` walk-up.

### `tests/test_shell_init.py`
Update `test_shell_init_precondition_checks` (line 97): `esp` no longer emits
`"esp: no active session"`, so assert `"esp: no active session"` is **absent**
and keep `"esf: no active session" in out` (esf retains its guard). The other
shell-init tests (`esp()` present, routes via `_endless_run`, passes
`--db main`, nounset-safe) remain valid.

## Deliverable: per-task verify script

Write `tests/tasks/e-<id>-verify.sh`, the single entry point for verifying this
task (per E-1596). **Model it directly on `tests/tasks/e-1542-verify.sh`** —
same harness: `set -u`, colored `section`/`report_pass`/`report_fail`/`summary`
counters, the `endless()` wrapper that runs `uv run endless "$@" --db sandbox`,
`assert_succeeds`/`assert_contains`/`assert_eq`, and a `main()` that `cd`s to
`git rev-parse --show-toplevel`, checks `uv`, runs the sections, and exits
0 on all-passed / 1 on any failure.

Sections:

1. **Build & automated suites** — `just build` if `bin/*` is stale (reuse
   e-1542's staleness check), then
   `uv run pytest tests/test_session_cd.py tests/test_shell_init.py -q`.

2. **shell-init content** — capture `uv run endless shell-init`. Assert:
   - `esp()` is present,
   - `"esp: no active session"` is **absent** (the guard is gone),
   - `"esf: no active session"` is **present** (esf guard kept).
   These assert against the worktree source string directly, sidestepping the
   stale global `endless` on PATH (the global lacks the fix until landed).

3. **Behavior E2E (worktree source, sandbox DB)** — the regression itself.
   Ensure schema exists (a throwaway `task add` applies `schema.sql` to the
   sandbox, as e-1542 relies on), then seed a project row pointing at the
   worktree's parent `endless` checkout
   (`real_root=$(cd ../../.. && pwd)`; `INSERT OR IGNORE INTO projects ...` via
   the script's `sql_write`). In a subshell with `ENDLESS_SESSION_ID` unset,
   run `endless session cd --target project` (no session-ref) and assert:
   - exit 0 (previously errored — this is the core fix), and
   - stdout equals `real_root` (the walk-up from the worktree cwd resolves the
     project root).
   Confirm the projects-table column names (`name`/`path`) against
   `schema.sql` before writing the seed.

There is **no irreducible manual step** for this change (unlike e-1542's
live-Claude render), so the script fully covers verification.

## Verification handoff

After implementation, the entire verify step the user runs is:

```sh
esu && ./tests/tasks/e-<id>-verify.sh
```

(`esu` cd's into the worktree and exports `ENDLESS_SESSION_ID`; the script
`cd`s to the worktree root itself, rebuilds if stale, and reports ALL PASSED or
a list of failures.) After landing, `just install` refreshes the global
install and a new shell (or re-`eval "$(endless shell-init)"`) picks up the
updated `esp`.

## Endless workflow

- File the task (`--type task`), claim it (creates the worktree), attach this
  plan via `task update <id> --text-file <this file>`.
- On completion set status `unverified` and hand back with the single
  `esu && ./tests/tasks/e-<id>-verify.sh` line; do not self-confirm.
