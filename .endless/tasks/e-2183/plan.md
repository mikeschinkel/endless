# E-2183 — plan

## First: a correction to this task's own analysis

The analysis says fixing `install` means "running `just install` in any of those
~99 pre-land branches" stops re-pinning. **That is wrong**, and the plan depends
on it being wrong.

An unsynced worktree runs its OWN `install` recipe too. Fixing `install` on main
does nothing for a branch that predates the fix — that branch's `install` still
has the bare `just` calls AND its `claude-settings-init` still pins. Conversely,
a worktree that HAS synced already has the fixed `claude-settings-init`, so its
bare `just` call is harmless.

So piece (1) fixes nobody today. What it does is stop recurrence: a worktree
synced now, against a `claude-settings-init` that changes again later, would
otherwise re-apply the older config recipe. That is worth doing — it is the
durable half — but it is prophylaxis, not remediation. Remediation is sync.

## Measured on main, 2026-10-11

    worktrees (registered == directories)                190
      justfile still carrying the OLD pinning recipe      88
      justfile carrying the fixed recipe                 102
    worktrees currently pinned                             1   (e-2157)

**e-2157 is the bug in the wild.** The sweep reported 122 visited / 99 unpinned
/ 0 failures, so it was unpinned; its `settings.local.json` is dated Sep 27
10:42 and names its own `bin/endless-go` again, and its justfile still has the
old recipe. Which of the three routes re-pinned it (a worktree-scoped `just
install`, a direct `just claude-settings-init`, or a re-run of
`post-worktree-create.sh`) is not recoverable from the filesystem, and the plan
does not need it: all three are closed by the same two moves.

## The ordering this forces

Deleting the sweep while 88 old-recipe worktrees exist removes the only tool
that can clean up the re-pin that just happened. So:

1. Re-sweep (closes e-2157).
2. Fix `install` (piece 1).
3. `endless worktree sync --apply` until the old-recipe count is 0, or the
   residue is named and understood.
4. THEN delete the sweep (piece 2).

Steps 2 and 4 are this task's code. Steps 1 and 3 are operations Mike runs; the
task is not done until 3 reports zero, because 4 is unsafe before then.

## Step 2 — `install` resolves config recipes from main

In the worktree-scoped branch of `install`, mirror what
`.endless/hooks/post-worktree-create.sh` already does and for the reason its
header already gives:

    main_checkout="$(dirname "$(cd "$(git rev-parse --git-common-dir)" && pwd)")"
    recipe() { just --justfile "${main_checkout}/justfile" \
                    --working-directory "$(pwd)" "$@"; }

    recipe go-work-init
    just build            # the BRANCH's, deliberately
    recipe dev-sandbox-init
    recipe claude-settings-init

`build` stays bare. A worktree exists to build candidate code; a branch that
changed how it builds must build its own way. That split is the statement worth
landing: **machine-level config comes from main, candidate behaviour comes from
the branch.** Put it in the recipe's comment, next to the existing paragraph
claiming the scoped install "NEVER touches the global symlink" — which E-2166
already had to amend once.

Check while there whether `go-work-init` and `dev-sandbox-init` genuinely belong
on the config side. `go-work-init` writes absolute paths to local go-pkgs
modules and `dev-sandbox-init` seeds a sandbox DB; both look like machine wiring
rather than candidate behaviour, which is why they are grouped here — but if
either is being deliberately exercised as candidate code, it moves to the
`build` side and the comment says so.

## Step 4 — delete `claude-settings-sweep`

Gate: step 3 reports 0 old-recipe worktrees.

- Remove the recipe from `justfile`.
- Remove `tests/test_claude_settings_sweep.py` with it. It exists only to cover
  the sweep; keeping a test for a deleted recipe is the same debt in another
  file.
- Leave `tests/test_claude_settings_init.py` alone. Its pin-removal cases cover
  `claude-settings-init`, which survives, and a worktree recreated at a path
  that once had a pin still needs the strip.

**This breaks E-2166's landed verify suite, and that is correct.**
`.endless/tasks/e-2166/verify.sh` asserts the sweep exists in five places
(lines 159, 161, 174, 180, and the skip message at 222). Per
`.endless/tasks/CLAUDE.md` — "Do not edit a landed task's suite… If your change
alters a string or a behaviour a landed suite asserted, leave the suite alone"
— do NOT retrofit it. Bit rot there is expected and accepted. Say so in the
commit message so the next reader of that suite is not surprised.

## Durable coverage

The behaviour must outlive this task's own gate, per `.endless/tasks/CLAUDE.md`:

- A test that `install`'s worktree-scoped path resolves `claude-settings-init`,
  `dev-sandbox-init` and `go-work-init` through `--justfile <main>/justfile`,
  and `build` without it. The cheap honest form reads the recipe body, as
  E-2166's suite did for the sweep; the strong form is the end-to-end shape
  `tests/test_claude_settings_sweep.py` uses — a temp repo whose main justfile
  differs from the worktree's, asserting which one ran. Prefer the end-to-end
  one: a source-reading test passes on a recipe that names the flag and still
  calls the wrong thing.

## Open question for Mike

Step 4's gate may never reach zero: `endless worktree sync` skips worktrees with
uncommitted changes, which is most live sessions. If the count stalls at a
handful of long-lived dirty worktrees, the choice is to (a) hold the sweep until
they land, or (b) delete it anyway and accept that those few can re-pin and
would need a hand-run `claude-settings-init` from main. Worth answering before
step 4, not during it.

## Not in scope, stated so it is not re-derived

`just claude-settings-init` run DIRECTLY in an unsynced worktree still gets that
branch's recipe. Nothing in the justfile closes that — only rebasing the branch
does. That is what makes "keep task worktrees synced" the answer rather than a
recipe that re-fixes the symptom, and it is why step 3 is part of this task
rather than a follow-up.

Self_dev only (ED-1571): no other project has a per-worktree install or a land.
