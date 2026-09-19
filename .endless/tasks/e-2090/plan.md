# Plan — an un-forgeable in-file guard, and a sweep that delivers it

## What changed since this task was filed

The filed plan's premise is gone. E-2023 (d3cf2342, three hours before this task
was filed) already prepended `source ../_harness.sh` to every suite: on `main`,
203 of 203 carry it as their first executable line, so "prepend a line to ~200
files" nets zero edits.

The exposure the plan describes is real, but it is not on `main` — it is in the
worktrees. Measured: 142 worktrees exist, 138 branched before E-2023 landed, and
105 of those still carry the retired `tests/tasks/*.sh` corpus — as many as 203
old, unguarded, directly-runnable copies each (e-1643). E-2071's incident is
reproducible today in 105 checkouts.

Two consequences, both load-bearing:

1. `ENDLESS_VERIFY_RUN` is a claim, not a check. Nothing reads its value — only
   its presence, in one place. `ENDLESS_VERIFY_RUN=/tmp/anything` walks straight
   past the harness, verified. It looks like enforcement, which is why no real
   enforcement was built behind it.
2. A guard living inside the suite file cannot reach a checkout that predates
   the guard. Editing `main` does not touch those 138 branches. The guard
   arrives only when a branch rebases.

## Measured, not assumed

Rebasing e-1917's branch onto main in a scratch clone:

    untouched                 -> rc=0, 0 conflicts, all 204 guarded suites arrive
    guard committed in place  -> rc=1, 5 conflicts, 165 suites double-guarded,
                                 stray _guard.sh in .endless/tasks/e-1719/

So editing the old paths in place is strictly worse than rebasing: it converts a
currently-clean rebase into a conflicted one, 138 times, because `main` already
put a different line at the top of the same files at their new path. Rebasing is
the cheap path AND the one that delivers the guard.

## 1. `_guard.sh` — two refusals, neither forgeable

`.endless/tasks/_guard.sh`, sourced (not executed). `_harness.sh` sources it as
its first act, so all 203 suites get it transitively. No suite gets a direct
line as well: one path in, or the refusal doubles.

**A. Ownership, from the paths alone.** The suite's own task id comes from
`${BASH_SOURCE[0]}` — `.endless/tasks/e-1001/verify.sh` is E-1001's, and no
environment can say otherwise. The checkout it is sitting in names its task the
same way: `.endless/worktrees/e-2090/...` is E-2090's. Different -> refuse.
Defeating this means physically moving files, not exporting a variable.

When the suite is NOT inside a worktree the path cannot answer, and the check
abstains rather than refusing: a project that does not use worktrees at all, and
a task whose worktree was reaped, both run their suite from the checkout — the
runner already falls back to cwd for exactly those two cases.

**B. Isolation, from the environment's actual shape.** Ownership does not stop
the owner running the suite by hand with a real `$HOME`, which is how the
original incident wrote a session row into the main database. So: refuse when
the real database is reachable — `${XDG_CONFIG_HOME:-$HOME/.config}/endless/
endless.db`, and the `$HOME/.config` spelling `--db main` uses. This is a check
on the condition, not on a claim about it: the only way to satisfy it is to
actually be isolated, at which point the suite cannot do the damage.

`ENDLESS_VERIFY_RUN` is deleted — the const in `internal/verifycmd/script.go`,
its export in `suiteEnv`, the block in `_harness.sh`, its row in the rules table
in `src/endless/suite_rules.py` and `.endless/tasks/CLAUDE.md`, and its tests.
E-2023's own suite asserts the old marker behaviour; per the rules of this tree
it is a landed suite and is left alone.

## 2. The banner — the guard says who may edit it

Four uniform lines below the shebang of every suite, naming that suite's owner:

    # ── DO NOT EDIT ────────────────────────────────────────────────────────
    # This suite belongs to E-1001 and records what was true when E-1001
    # landed. Edit it only if you ARE E-1001. If your change breaks an
    # assertion here, leave it alone — see .endless/tasks/CLAUDE.md.

Prose is the layer that already failed, so this is not counted as a defence on
its own. It is placed where an agent meets it at the moment it opens the file,
and it is the interlock E-1916 Arm 1 enforces: refusing edits to a landed suite
is what stops the guard being deleted.

Mechanically uniform, so the sweep is scriptable and the result greppable, and
idempotent: re-running never adds a second copy.

## 3. `endless worktree sync` — the sweep that delivers it

The guard reaches the 138 stale worktrees only by rebase, so the rebase is part
of this task rather than a follow-up.

A product verb, not a one-off script: worktrees drifting from main is not an
Endless-specific condition, and 138 of 142 here is what that drift looks like
when nothing sweeps it. `endless worktree sync` rebases each worktree of the
current project onto the default branch.

- Dry-run by default; `--apply` acts. A sweep across every worktree of a project
  must show its work before it rewrites 90 branches.
- Skips a worktree with uncommitted changes, naming it and why — a rebase there
  would refuse anyway, and its session is mid-flight. Measured now: 90 clean,
  48 dirty.
- Aborts and restores any rebase that conflicts, reports it, and moves on. One
  worktree's conflict must not strand the sweep or the worktree.
- Never removes anything.

## Boundaries

- Does NOT convert suites to the harness's assertion helpers (ED-1534). The
  suites already source it; nothing here touches an assertion.
- Does NOT edit a landed suite's body. The banner is inserted below the shebang;
  no assertion, fixture or message changes.
- Does NOT retire E-1916's Arm 2. It is the only thing covering a checkout that
  has not yet been swept, which is the state this task starts in.
- Does NOT rebase a dirty worktree, or touch another session's uncommitted work.

## Verify

1. A suite run from a foreign task's worktree refuses, naming its own task and
   the worktree it is in — with no environment variable involved.
2. A suite run by hand from its OWN worktree, with a real `$HOME`, refuses.
3. A suite run through the runner by the session that owns it succeeds, and its
   output is byte-identical to before.
4. `ENDLESS_VERIFY_RUN` no longer appears in the runner, the harness, or the
   rules; setting it changes nothing.
5. A suite outside any worktree does not refuse on ownership — the reaped-
   worktree and no-worktrees-project cases still verify.
6. Every suite carries the banner exactly once, naming its own task; re-running
   the sweep is a no-op.
7. `endless worktree sync` dry-runs by default, skips dirty worktrees with a
   reason, restores a conflicting rebase, and leaves a swept worktree carrying
   the guarded suites.
8. The regression E-2023 guards: a landed, reopened, re-claimed task verifies
   from its own worktree.
