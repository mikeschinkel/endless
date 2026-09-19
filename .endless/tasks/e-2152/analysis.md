## The problem in general

Endless runs code from at least FOUR independent binary identities, and nothing
detects when any of them diverges from the source it claims to be:

1. **A long-running process's image.** `endless-go session-status --monitor` is
   exec'd once and lives for days. The job runner fires IN-PROCESS inside it
   (`liveview.Loop` → `jobs.RunDue`), so the jobs run whatever build that process
   started with.
2. **The main checkout's build**, the project's `bin/endless-go`, which every
   caller reaches through the the global `endless-go` symlink symlink. `just land`
   rebuilds it; anything else that puts code on main does not.
3. **Each worktree's own build.** A worktree-scoped install points at that
   worktree's `bin/endless-go`, built from its branch. A rebase onto main changes
   the source underneath it and does not rebuild.
4. **The editable Python install.** `uv tool install -e .` means Python changes go
   live the instant they are written, while Go changes need a build. A land that
   touches both leaves the two halves out of step until somebody rebuilds — and
   the Python half will happily call a Go binary that predates it.

## Two observed instances, both on 2026-09-15, both silent

**A stale process (identity 1).** E-2128 landed a fix to the `worktree-unlanded`
job and the incidents it fixed kept arriving. The code on main was correct and
running the current binary by hand produced no error, but `ps` showed NINETEEN
`endless-go session-status --monitor` processes started across ten days (Sep 5, 6,
7, 11, 14), each still executing its original image. The oldest was ten days out
of date and still writing to shared state. Restarting tmux fixed it; nothing had
said anything was wrong.

**A stale worktree build (identity 3).** In the same session, 89 Python tests
failed in a worktree immediately after a rebase onto main. The cause was that
worktree's `bin/endless-go`, built before the rebase, against a Go/Python contract
main had since changed. `just build` fixed all 89. The first instinct was to look
for a regression in the change under review — which is the same wrong turn the
stale process caused, from a different identity.

Both failures invert trust: correct code appears broken, so the search goes to the
code rather than to what is executing it.

## Why this is its own problem, and not E-1848's

E-1848 builds the long-running daemon so jobs run without a monitor being live.
Adjacent, but different and NOT a fix: that task is about jobs running when NO
monitor exists; this is about code running WRONGLY when something does. A
long-lived daemon is MORE exposed to identity 1, not less.

Decide this first and let E-1848 inherit the answer.

## Approaches, which differ in kind

Not variations on one fix — different answers to "whose job is it to notice":

1. **Detect and warn.** Compare the running identity against the source it came
   from and mark the surface. Cheapest; leaves the human to act, and adds one more
   thing to notice.
2. **Self-restart.** Re-exec on change. Removes the human, but re-exec under tmux
   with a live pane painting into it is its own hazard.
3. **Refuse while stale.** Keep painting, stop firing. Fails safe without fixing
   anything — and silently stops work the operator believes is happening, which is
   arguably the same class of defect.
4. **Eliminate the identity.** For 1 that is E-1848's daemon; for 3 it is
   rebuilding on rebase, or not having per-worktree binaries at all.
5. **Version-stamp.** Record which build ran a job, served a command, or produced
   a result, so `jobs list` can show a job last run by a build three days old.
   Diagnostic only, but it puts the condition where the operator already looks,
   and it COMPOSES with every other option.

## What the outcome should say

Which identities Endless will guard and how — noting that the right answer may
differ per identity, and that 5 composes with all of them. Specifically whether
the approach is prevention (1, 2), containment (3), elimination (4), or
visibility (5).

A cheap early win worth costing separately: a single `endless doctor`-style check
that reports all four identities and whether each matches its source. That does
not decide anything, but it makes the condition visible today.
