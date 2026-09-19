## The problem

"Does this branch hold work the base branch lacks?" has an exact answer and a
cheap answer, and they are not the same answer.

- **Exact** (E-2087, `unlandedCommits` in internal/monitor/worktree_unlanded.go):
  `git range-diff` between the branch and the base since their merge base.
  Recognises a commit a rebasing land re-hashed, and one whose diff changed when
  a conflict was resolved. Measured cost: ~1-2s per worktree, scaling with how
  far the branch has drifted from the base, NOT with what the branch holds. A
  worktree 2251 commits behind main costs 2.1s on its own.
- **Cheap** (`reapNothingToLand` in internal/monitor/reap_worktrees.go):
  `git rev-list --count HEAD ^base ^<recorded landing>...`. One call, ~5ms.
  Cannot see a landing that was not recorded, so it over-reports outstanding
  work — safe for a delete decision, wrong for a display.

## What happened, and why this is filed

E-2087 replaced the SHA check with the exact one everywhere the two shared a
probe — including the worktree reaper. `monitor.ReapWorktreesForProject` is
called from FIVE branches of internal/hookcmd/claude.go, among them PreToolUse
and PostToolUse, so the reaper sweeps every worktree before and after every tool
call in every session. At ~90s per sweep that stopped the product, not just
`task spawn`.

The hotfix reverted the reaper to the cheap check and left the exact one on the
display surfaces (◆ in `session status`, `task unsettled`), which probe one row
or a handful rather than 139. The debt it accepted: a worktree whose work landed
under rewritten SHAs with no `task_landings` row reads as holding work forever,
so its directory is never reclaimed. That is the leak E-1940 and E-2087 each
tried to close, reopened deliberately because a slow leak is survivable and a
90-second tool call is not.

## What to decide

How to have the exact answer without paying for it per tool call. Directions
worth weighing, not a menu to pick from:

1. **Cache the verdict, keyed on (branch tip OID, base tip OID).** Both are
   ~1ms to read, and the key self-invalidates when either ref moves. The
   question is where it lives (a table? a file under the worktree's .git?) and
   what happens after a land, which moves the base and invalidates every entry
   at once — the cold sweep is back to ~90s unless something warms it.
2. **Take the reaper off the hook path.** A destructive background sweep
   running before and after every tool call is questionable independent of what
   it costs. Debounce it, run it on a timer, or move it to session start/end.
   This may be the real fix, with caching a nice-to-have on top.
3. **Make the exact answer cheaper.** The cost is dominated by patch-ids for
   every commit the base gained since the fork. Measured alternatives:
   `git cherry` (patch-id only) is ~3x faster but misses conflict-resolved
   landings; pairing by subject + author date is ~35x faster but disagrees with
   range-diff in BOTH directions, so it is unsafe alone. A union or a two-stage
   filter may be exact enough and much cheaper.
4. **Stop needing it.** ED-1587 makes branch names derivable and retires
   `task_landings.branch`. If landings were also recorded reliably, the cheap
   check would rarely be wrong — E-2087 measured worktrees whose work is on main
   with no landing row at all, and that recording gap is arguably the real
   defect underneath both leaks.

## Constraints any answer has to respect

- The reaper's mistakes are not symmetric. Refusing to reap costs disk; reaping
  a worktree that still holds work destroys it. Whatever it consults must be
  wrong only in the safe direction, or be exact.
- ◆, `task unsettled` and the reaper are supposed to agree about what "landed"
  means. The hotfix broke that on purpose and it should not stay broken.
- Whatever runs on the hook path is paid before and after every tool call.
  Budget it in milliseconds.

## Measured, after the hotfix — the sweep is still not cheap

On this project (139 worktrees, 41 of them past the 14d TTL and therefore
reaching git) the reaper's git sequence costs **2.58s per sweep**, on
PreToolUse AND PostToolUse. Down from ~90s, but not free, and it is paid twice
per tool call.

Two findings worth starting from:

- **Roughly 60% of it is re-deriving a constant.** The sequence per candidate
  is five git calls: three of them are `monitor.DefaultBranch` resolving
  `origin/HEAD` → `init.defaultBranch` → `rev-parse --verify main`. That is a
  property of the REPOSITORY, and all 139 worktrees share one. It is memoized
  per directory in a `sync.Map`, which does nothing here because every hook
  invocation is a fresh process. Caching per repo — or persisting it — cuts the
  sweep to roughly 1s without touching the accuracy question at all.
- **E-2087's eligibility change roughly doubled the candidate set.** Dropping
  the task_landings requirement admits 20 more worktrees (22 → 41) past the TTL
  gate and into git. That is the intended behaviour — those are precisely the
  never-recorded worktrees that could not be reclaimed — but it means the hot
  path got more expensive in exchange for reclaiming more, and nobody priced
  that at the time.

Both point the same way as direction 2: a sweep this shape does not belong on
PreToolUse/PostToolUse regardless of how fast each candidate is made.

## Measured 2026-09-06 — the display surfaces are not "a handful" either

The hotfix rationale above rests on a premise worth correcting: that leaving
the exact probe on the display surfaces was affordable because they "probe one
row or a handful rather than 139". On this machine they do not.

`session monitor` is a long-running view that re-probes every row it renders
every two seconds, and it is normal to have many panes open. Measured with the
reaper already off the exact probe:

- **13 concurrent `endless session monitor` processes**, uptimes 7 to 54
  minutes.
- Sampling `ps` once a second for ten seconds found **4-6 `git range-diff`
  processes in flight at every single instant** — never zero.

So the exact answer is a continuous multi-core background load, paid on the
DISPLAY path, with the reaper already excused from it. The cost model there is
(monitors x rows with worktrees x per-row range-diff), refreshed every two
seconds, with no cache — so every monitor pays again for work another monitor
just finished, and adding a pane multiplies the whole bill.

Three consequences for the directions above:

1. **Direction 1 (cache keyed on branch/base tip OIDs) is worth more than this
   analysis credits it.** It was weighed as a way to make the reaper's sweep
   affordable; it is also the only direction that fixes the display path, which
   is where the load actually is. Thirteen monitors sharing one cache pay for a
   worktree once per ref movement instead of 13 times per two seconds.

2. **The cold-sweep-after-a-land objection is worse on the display path than on
   the reaper's.** A land moves the base and invalidates every entry at once —
   and on the display path every monitor hits that cold cache simultaneously,
   within the same two-second tick.

3. **It is not only a cost problem.** With a range-diff always in flight, a
   Ctrl-C in any monitor pane kills one mid-run. The probe fails closed and
   records ERR-0010 against a task that is fine — observed for E-1972 at
   2026-09-06 00:20:03 EDT (`git range-diff: signal: interrupt`, one
   occurrence, while `task unsettled E-1972` answers cleanly on demand). The
   misclassification itself is filed as **E-2113** and is worth fixing whatever
   this task decides; it is noted here because the near-certainty of hitting it
   is a property of this cost problem, and a cheap or cached probe would shrink
   the window that makes it routine.

Per-worktree cost is confirmed in the range this analysis reports: e-1972 is
7 branch commits against the 648 main gained since its fork, and its
range-diff measures 0.9s. 128 worktrees are on disk.
