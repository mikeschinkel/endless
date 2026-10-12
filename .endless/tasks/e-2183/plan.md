# E-2183 — plan

Scope narrowed 2026-10-11, on Mike's call: this task is the `install` fix only.
Deleting `claude-settings-sweep` has come OUT and is recorded on E-2172 instead,
because the condition that makes deletion safe is E-2172's subject, not this
task's. Keeping both here would have left E-2183 open for months waiting on it.

## First: a correction to this task's own analysis

The analysis says fixing `install` stops "running `just install` in any of those
~99 pre-land branches" from re-applying the old behaviour. **That is wrong**, and
the plan depends on it being wrong.

A worktree that has not been rebased onto main runs its OWN `install` recipe
too. Fixing `install` on main does nothing for such a branch — its `install`
still has the bare `just` calls and its `claude-settings-init` still writes the
hooks block. Conversely, a worktree that HAS been rebased already has the fixed
`claude-settings-init`, so its bare `just` call is harmless.

So this change fixes nobody today. What it does is stop recurrence: a worktree
rebased now, against a `claude-settings-init` that changes again later, would
otherwise re-apply the older config recipe. That is worth doing and it is the
durable half — but it is prophylaxis, not remediation. Remediation is rebasing
the branch, which is E-1881/E-2172's territory.

## Measured on main, 2026-10-11

    worktrees (registered == directories)                191
      justfile still carrying the OLD config recipe       88
      justfile carrying the fixed recipe                 103
    worktrees whose settings.local.json has a hooks block  0

The hooks-block count is zero because Mike re-ran the sweep today. It had been
1 (`e-2157`) — unpinned by the first sweep on Sep 27, then written back the same
morning, with its justfile still carrying the old recipe. One recurrence in two
weeks across ~190 worktrees is the observed rate, and it is the evidence that
this task is about something real rather than hypothetical.

## The change

In the worktree-scoped branch of `install`, mirror what
`.endless/hooks/post-worktree-create.sh` already does, for the reason its header
already gives (a worktree on an older branch carries an older justfile):

    main_checkout="$(dirname "$(cd "$(git rev-parse --git-common-dir)" && pwd)")"
    recipe() { just --justfile "${main_checkout}/justfile" \
                    --working-directory "$(pwd)" "$@"; }

    recipe go-work-init
    just build            # the BRANCH's, deliberately
    recipe dev-sandbox-init
    recipe claude-settings-init

`build` stays bare. A worktree exists to build candidate code; a branch that
changed how it builds must build its own way. The statement to land in the
recipe's comment is: **machine-level config comes from main, candidate behaviour
comes from the branch.** Put it beside the existing paragraph claiming the
scoped install "NEVER touches the global symlink" — which E-2166 already had to
amend once, so it is the paragraph that goes stale.

Check while there whether `go-work-init` and `dev-sandbox-init` genuinely belong
on the config side. `go-work-init` writes absolute paths to local go-pkgs
modules and `dev-sandbox-init` seeds a sandbox database; both read as machine
wiring rather than candidate behaviour, which is why they are grouped there.
If either turns out to be deliberately exercised as candidate code, it moves to
the `build` side and the comment says which and why.

## Document the one-worktree recovery

While the sweep still exists this is redundant, but the sweep is going away on
E-2172 and the knowledge must not go with it. Record, in the `install` recipe's
comment or `endless guide`, the two commands that matter:

- Detect: list every worktree whose `.claude/settings.local.json` contains a
  `hooks` key.
- Repair one: `just --justfile <main>/justfile --working-directory <worktree>
  claude-settings-init`

That is exactly what the sweep does per worktree, so documenting it is what
makes the sweep's eventual removal a loss of a loop rather than a loss of a
capability.

## Durable coverage

Per `.endless/tasks/CLAUDE.md`, this must outlive the task's own gate:

A test that `install`'s worktree-scoped path resolves `claude-settings-init`,
`dev-sandbox-init` and `go-work-init` through `--justfile <main>/justfile`, and
`build` without it. Prefer the end-to-end shape
`tests/test_claude_settings_sweep.py` already uses — a temp repo whose main
justfile differs from the worktree's, asserting which one actually ran. A test
that merely greps the recipe source passes on a recipe that names the flag and
still calls the wrong thing.

## Not in scope, stated so it is not re-derived

`just claude-settings-init` run DIRECTLY in a worktree that has not been rebased
still gets that branch's recipe. Nothing in the justfile closes that; only
rebasing the branch does. That is E-2172's question, and it is why the sweep's
removal is recorded there rather than gated inside this task.

Self_dev only (ED-1571): no other project has a per-worktree install or a land,
so none of this mechanism exists there.
