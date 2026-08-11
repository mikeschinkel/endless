# E-1941 — Rework `just land`'s ordering so no failure can leave the DB ahead of the code

## Context

Landing E-1898 on 2026-08-10 migrated the real database to a schema no installed
binary understood, then failed to merge. Session tracking froze machine-wide and
recovery needed a hand-rolled `cp` restore.

The cause is ordering in the dev-only `just land` recipe. It runs, in order:
rebuild the worktree binary → **`endless db apply-change` against the real DB** →
`endless worktree land` (rebase + ff-merge). The recipe's own comment reasons
only about apply *failing* ("the land aborts before main advances"). Apply
*succeeding* and the merge then failing is the unhandled case, and it is the
irreversible one.

Reproduced by extracting the real recipe body with `just --show land` and running
it against a throwaway repo with stubbed `endless`/`just`; task branch 3 commits
behind main, the land stubbed to fail as it did on 2026-08-10 (this harness
becomes check 1 of the verify script below):

```
just go (cwd=e-9999) → endless db backup → endless db apply-change → endless worktree land ✗
DB state: MIGRATED      main advanced: NO
```

Two defects, both confirmed: there is no check that the branch is current with
main before the binary is rebuilt, and the DB is migrated before anything
irreversible is known to succeed.

**Outcome:** invert the asymmetry. Apply schema changes only after main has
actually advanced, so a failed apply is a retry rather than a restore; and
refuse the land outright when the branch is behind main, so the binary that runs
against the real DB is built from source that matches main.

## Approach

### 1. Refuse a behind-main land

The rule lives in `land_worktree`, **gated on `config.project_is_self_dev`**, so
a direct `endless worktree land` gets it too — not only `just land`. It must stay
self_dev-gated: a downstream user's branch being behind main is the normal case
the land's rebase exists to handle, and refusing there would break ordinary
product usage.

The justfile keeps a cheap `git -C "$wt" rev-list --count HEAD..main` pre-check
before `( cd "$wt" && just go )` purely to avoid a pointless rebuild before the
refusal fires. Deliberate duplication of one git command; the rule itself has a
single home.

Refuse, do not auto-rebase — E-1898's own rebase stopped on a conflict, and a
rebase that stalls with main partly advanced is worse than a clean refusal.

**The message must not just say "rebase."** Under a rewritten main the rebase is
itself the thing that conflicts, so a bare instruction sends the user into the
failure. It should state the drift, say why the land is refused (the binary about
to touch the real DB must be built from source matching main), and then name both
cases: an ordinary behind-main branch rebases cleanly; a branch whose base was
rewritten (`git reflog show main` showing a `rebase (finish)` entry, or
`git cherry main HEAD` reporting patch-equivalent commits) may conflict on
`.endless/db-ledger/`, which is E-1943 and not yet solved.

### 2. Move the schema apply into `land_worktree`, after the merge

`src/endless/worktree_cmd.py`, `land_worktree()`. The apply cannot simply move
later in the justfile: Step 6 (`_record_landing`) runs the *worktree* binary
against the *real* DB, and for an enum-adding branch that binary carries a
constant the DB lacks until the change is applied — E-1664's failure inverted.
So it belongs between Step 5 and Step 6.

- **Compute the change list before Step 5**, into a local. This is load-bearing:
  after the ff-merge `main` and the branch are the same commit, so
  `git diff main...HEAD` returns empty and the changes would be silently skipped.
  Reuse the justfile's existing pathspec verbatim:
  `--diff-filter=A --name-only -- internal/schema/changes/
  ':(exclude)internal/schema/changes/runner/'`, filtered to `*.sql`/`*.go`.
- **After the ff-merge succeeds**, and only when
  `config.project_is_self_dev(main_root)`: back up, then apply each change file
  in order, pinning `endless_go_bin` (already resolved at line 2085 by
  `_resolve_land_endless_go`).
- **On apply failure**, raise a ClickException in the shape `_record_landing`
  already uses for post-merge failures (worktree_cmd.py:2001-2008): say main was
  advanced, name the cause, and state that `endless db apply-change` is
  idempotent and gated by `_schema_version` so re-running `just land` recovers.

Two new module-level helpers alongside the existing ones:
`_branch_schema_changes(worktree_path, base_branch)` and
`_apply_branch_schema_changes(paths, main_root, endless_go_bin)`.

### 3. Thread the pinned binary through `apply_change` / `backup_db`

`src/endless/event_bridge.py`. Both currently call `_resolve_endless_go()` with
no override, which is why the justfile needs its `PATH="$wt/bin:$PATH"` hack.
Add `endless_go_bin: str | None = None` to each and pass it to
`_resolve_endless_go(override=...)` — exactly the parameter `emit_event` already
threads (line 203). This finishes the job E-1664 started: it explicitly
superseded the E-1660 PATH hack for the record step, and this removes the last
one.

`cli.py`'s `db apply-change` / `db backup` commands keep their current
no-override behavior; the new parameter defaults to `None`.

### 4. Justfile: relocate, don't drop; rebuild whenever main advanced

**The backup is not removed — it moves.** The `endless db backup` call and the
`for f in $changes` apply loop (justfile:219-237) both relocate into
`land_worktree` per section 2. The backup stays warranted: a change set can be
several files, so file 1 can apply and file 2 fail, leaving a partially migrated
DB no re-run heals, and E-1942 means there is still no `endless db restore`.

**Close the remaining window.** If the apply succeeds and `_record_landing` then
fails, the recipe aborts before `just build` — leaving old global binaries against
a *new* DB, which is the incident's shape. That window exists today (wider: it
spans the merge too). Main advancing is the fact that obliges a rebuild; the
land's exit code is not. So capture main's SHA before and after the land and
rebuild whenever it moved, propagating the land's exit code:

```sh
before=$(git -C "$main_root" rev-parse main)
endless worktree land "$tid"
land_rc=$?
after=$(git -C "$main_root" rev-parse main)
if [ "$before" != "$after" ]; then ( cd "$main_root" && just build ); fi
exit "$land_rc"
```

**Drop `set -euo pipefail` from this recipe** (justfile:169) and handle errors
explicitly. It is banned in this project — `pipefail` turns SIGPIPE from an
early-exiting `head`/`grep -q` into a crash, and `set -e` has inconsistent
semantics in subshells, loops, and conditionals. The logic above depends on
observing a non-zero exit rather than dying at it, so the conversion is part of
this change, not incidental cleanup. Every command whose failure matters gets an
explicit check; `${PIPESTATUS[@]}` where a pipeline's stage matters. Load the
`shell-script-author` skill before writing it, and ShellCheck the result.

The other 5 recipes using strict mode are left alone — out of scope here.

Also update the stale comment block at justfile:193-198, whose claim "If any
apply fails, the land aborts before main advances" is the reasoning error behind
the incident. Keep the up-front `just go` (E-1709).

## Explicitly out of scope

**The throwaway rehearsal branch is dropped.** The analysis prescribes
rehearsing the merge on `endless-land-<tid>-<rand>`, but this needs your
sign-off to omit, because it is a deliberate departure from the written plan.
Tested across four scenarios (amend alone; `pull --rebase` alone; both; both with
a union driver) against two strategies — rebase the task branch onto main, versus
branch off main and merge the task branch in. The two gave an identical verdict
in every scenario: a temp branch with the task branch *merged* into it conflicts
on the same file, in the same cases, as the current rebase does.
Step 4 already rebases in the worktree (main untouched, clean abort) and Step 5
is already `git merge --ff-only` (a pure pointer move), so the rehearse-then-
pointer-move shape exists. It would cost ~5 test files plus a temp-branch /
task-branch reconciliation question, and change no tested outcome.

**The `canAmend` guard and `pull.rebase` support are not fixed here.**
`git pull --rebase` blinds canAmend's shared-tip guard (it is SHA-reachability
based), which is the actual origin of the orphaned ledger bases. That evidence —
the guard experiment, the struck union-merge lever, and the arguments already
tried and discarded — is folded into E-1943's analysis, which owns the decision.
Per ED-1550 no new task was filed.

Honest limit: the behind-main refusal converts an irreversible DB-ahead disaster
into a clean refusal — and under a blinded guard the rebase it names is itself
the thing that conflicts, which is why the message must describe that case rather
than issue a bare instruction. This bounds the damage; E-1943 is what makes the
land succeed.

**This does nothing for product (non-self_dev) users, by construction.** Only
endless's own branches carry `internal/schema/changes/` files, so downstream
users have no apply step and no DB-ahead risk from landing; the behind-main
refusal is deliberately withheld from them because their branches being behind
main is normal. Their equivalent exposures live elsewhere and are not addressed
here: the ledger-orphan land conflict applies to them in full (**E-1943**, which
now carries "must work under either `pull.rebase` setting" as a requirement), and
how a downstream DB receives a *non-additive* migration at all has no answer
today — `db apply-change` is documented as driven by `just land`, and
apply-on-connect is additive only (**E-1944**). Noted in conversation per
ED-1550; not filed.

One relation worth stating: E-1941 is formally `blocked_by` E-1943. The ordering
fix does not depend on E-1943's outcome, so it proceeds.

## Files

| File | Change |
|---|---|
| `justfile` `land` (~167-251) | drop strict mode for explicit checks; behind-main pre-check; relocate backup/apply loop; rebuild when main advanced; fix stale comment |
| `src/endless/worktree_cmd.py` | behind-main refusal (self_dev-gated) + message; 2 helpers; pre-merge change list; apply between Steps 5 and 6 |
| `src/endless/event_bridge.py` | `endless_go_bin` param on `apply_change`, `backup_db` |
| `tests/test_worktree_land_schema_apply.py` | new |
| `tests/tasks/e-1941-verify.sh` | new |

## Tests

`tests/test_worktree_land_schema_apply.py`, following the monkeypatch style of
`tests/test_worktree_land_record_landing.py`:

- apply is invoked **after** the ff-merge and **before** `_record_landing`
  (ordering asserted via a shared call log)
- the change list is captured pre-merge and is still non-empty post-merge
- apply failure raises a re-runnable "main was advanced" error and does not
  unwind the merge
- a non-self_dev project skips apply entirely **and is never refused for being
  behind main**
- a self_dev project **is** refused when behind main, and the message names the
  rewritten-base case rather than only saying "rebase"
- the pinned `endless_go_bin` reaches `_resolve_endless_go(override=...)`

## Verification

Single command: `esu && ./tests/tasks/e-1941-verify.sh`

Modeled on `tests/tasks/e-1709-verify.sh` (same pass/fail + summary shape,
exit 0/1/2), fail-fast, folding this task's own tests in:

1. **Regression, end-to-end** — the reproduction harness from Context, promoted
   into the suite: extract the recipe body with `just --show land`, run it against
   a throwaway repo with stubbed `endless`/`just`, land stubbed to fail. Assert
   the DB was **never** migrated and main never advanced. This is the check that
   fails on today's code.
2. **Behind-main refusal** — same harness with the branch behind main: assert the
   recipe exits non-zero, never reaches `just go` or `apply-change`, and that the
   message names the rewritten-base case, not just "rebase".
3. **Rebuild follows main** — harness with the land stubbed to advance main and
   then fail: assert `just build` still ran. And with the land stubbed to fail
   without advancing main: assert it did not.
4. **Recipe hygiene** (static guards, per E-1709's check 2) — `just --show land`
   contains no `endless db apply-change` and no `set -euo pipefail`; `just go`
   still precedes `endless worktree land`; `bash -n` parses the body; ShellCheck
   is clean.
5. **Unit tests** — `pytest tests/test_worktree_land_schema_apply.py` and the
   existing `tests/test_worktree_land_*.py` suite.

Project-wide regression (`just test`, `just test-go`, `just build`,
`just guide-check`) I run myself and report as one line.
