# E-1519 — Smooth endless CLI's first-invocation friction in a fresh worktree sandbox

## Problem

Invoking `endless` commands in a fresh worktree sandbox hits a chain of errors where each one fails in isolation without pointing at the next step. The result: any agent (or human) trying to verify a Python-side CLI change ends up debugging the verification harness instead of the change. A related cwd-resolution gotcha bites even when explicitly targeting `--db main` from inside a worktree.

## The chain (observed 2026-05-30 verifying E-1517)

1. **No project row in sandbox DB.** `endless task add ... --db sandbox` → `Error: No project found with name 'endless'`. Required manual `endless register "$(pwd)" --db sandbox`. (Related slice already filed: **E-1508** — clone the project row from main.)
2. **No session row bound to the invocation.** `endless task add ... --db sandbox` → `Error: Cannot determine the Endless session for this pane`. Worked around with `ENDLESS_SESSION_ID=0`, which is exactly the bypass the E-1401 gate is supposed to prevent — so the workaround is itself a smell.
3. **cwd overrides `--db main` project resolution.** Running `endless task add ... --db main` from inside `.endless/worktrees/e-1517/` resolves the project from cwd (→ `e-1517`) instead of from the `--db main` context, and fails with `No project found with name 'e-1517'`. Only fix today: pass `--project endless` explicitly. Observed *while filing E-1519 itself*.

(A fourth instance — "worktree's endless-go binary missing" via the `bin-sandbox/` wrapper — is excluded from this ticket: it's exactly E-1368's scope.)

## Fix

### (1) and (2) — pre-seed at sandbox init, not bandaid at runtime

The product entrypoint is `endless-go sandbox init` (`internal/sandboxcmd/init.go`), which already declares modes `empty | seed | clone` — `seed` and `clone` are placeholder stubs today. The pre-seed work lands inside the `seed` mode's implementation (or a successor mode if `seed` is reserved for a different shape; this plan doesn't fix the mode-naming question, just the behavior). The Justfile recipe `just dev-sandbox-init` continues to be a thin wrapper around the binary, and `endless task claim` / `endless task spawn` already auto-invoke the same product code path — so the logic ships once and every entry point benefits.

What the init code writes into the new sandbox DB before the call returns:

- **Project row.** Copy from the main DB's project row corresponding to the main checkout this worktree was cut from. The name and metadata are already canonical there; no inference, no naming heuristic, no asking the user.
- **Session row.** Insert one row into the existing `sessions` table — no schema change, all existing columns only. The `session_id` value is:
  - `CLAUDE_CODE_SESSION_ID` from the environment, when it is set (the common case — init is invoked under a Claude session via `endless task claim` or `endless task spawn`).
  - The null UUID `00000000-0000-0000-0000-000000000000` otherwise (manual `just dev-sandbox-init` from a non-Claude terminal, direct `endless-go sandbox init` invocation, etc.). This is the established Endless convention for any non-Claude use-case — distinct from "any live id" bypass (see feedback memory `feedback_session_id_not_interchangeable`): the null UUID is the *designated* non-Claude session_id, not "any other live id."
  
  The `bin-sandbox/<name>` wrapper exports this same `session_id` as `ENDLESS_SESSION_ID` so that terminal invocations inside the worktree (i.e. running `endless task add --db sandbox` to verify a change, outside any Claude session) attach to the seeded row. Claude sessions inside the worktree continue to mint per-pane rows through the normal hook flow keyed off their own `CLAUDE_CODE_SESSION_ID`; the seeded row only services the terminal path.

Both errors stop firing because the precondition they guard is already true by construction. No "first-invocation" lazy bootstrap, no error-message-with-suggested-command bandaid.

### (3) — main-checkout-aware project resolution for `--db main` from a worktree

When `--db main` is in effect and the current cwd is inside a git worktree (not the main checkout), resolve the project's directory by walking to the main checkout:

```
main_checkout_dir = dirname(git rev-parse --git-common-dir)
```

`--git-common-dir` returns the path to the shared `.git/` directory, which in a worktree points at `<main_checkout>/.git`. `dirname` of that is the main checkout. Use that path as the project-resolution root instead of cwd. The lookup then finds `endless` (or whatever the project is named) in the main DB and the call succeeds without `--project`.

The project's own `CLAUDE.md` already documents this exact discriminator (`git-dir` vs `git-common-dir` equality) for the `.claude/settings.json` recipe's main-vs-worktree check, so the pattern is an established norm in the codebase.

## Verification

- From a fresh worktree (no manual `register`, no `ENDLESS_SESSION_ID` export), `endless task add "Add x" --db sandbox` succeeds and writes to the sandbox DB.
- From the same worktree, `endless task add "Add y" --db main` succeeds against the main DB without `--project endless`.
- The seeded `sessions` row's `session_id` equals `CLAUDE_CODE_SESSION_ID` when init was run under Claude, and the null UUID when run from a non-Claude shell.
- Existing tests for `register`, sandbox routing, and `--db` resolution continue to pass.

## Out of scope

- Cleaning up the `bin-sandbox/` wrapper layer / binary self-detect — **E-1368** owns that.
- Seeding the sandbox DB with *full* main-DB content (tasks, decisions, relations, etc., not just one project + one session row) — **E-1508** owns that broader scope.
- The naming of the init mode that carries this behavior (`seed` vs a new mode); the existing `--mode seed` stub may have a different intent already.
- A formal `endless decision add` record for the null-UUID convention. (Was attempted as E-1523 but the legacy CLI path orphans rows pre-E-1507; the convention is captured in this plan's prose instead, to be re-recorded as a formal decision after E-1507 lands.)
