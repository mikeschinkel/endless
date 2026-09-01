## Evidence (measured 2026-08-30)

`endless task unsettled --all` reported 86 unsettled worktrees. Re-running the
same population through `git cherry main <branch>` — which matches by patch-id
rather than SHA — gives 14 branches holding any commit not already on main.

Worst offenders, reported vs. actual:

| Branch | `unsettled` says | `git cherry` says |
|---|---|---|
| E-1934 | 314 | 2 |
| E-1844 | 223 | 4 |
| E-1785 | 198 | 2 |
| E-1853 | 182 | 6 |
| E-1537 | 122 | 2 |

## Cause

`worktree land` rebases the branch before fast-forwarding main. The rebase
rewrites SHAs, so main receives a copy of each commit under a new hash while
the branch retains the original. Any check based on "is this SHA reachable from
main" reports the branch's copies as unlanded permanently, and the number grows
with every subsequent land on the branch.

## Why it matters

The count is the input to every "what is outstanding?" judgement. At ~6× it
makes a 14-branch backlog look like an 86-branch one, and it buries the handful
of branches that genuinely hold work under a pile that does not. It also makes
the number grow over time for branches that are *more* settled, not less.

## Scope

Replace the SHA-reachability check with a patch-id comparison (`git cherry`, or
`git log --cherry-mark --left-right`) in whatever `unsettled` and the `session
status` ◆ probe share — the two read the same probe by design and must keep
doing so.

Note the separate case the fix should keep visible: a branch can carry patches
that are genuinely absent from main and still be undesirable to land, because
main has moved past them by another route. Four branches here (E-1537, E-1785,
E-1844, E-1934) hold only a README rewrite that E-1832 superseded. Patch-id
correctly calls those unlanded; it is the operator who has to decide they are
not worth landing. That is out of scope here — just do not paper over it.



## Correction from E-2089: patch-id is not sufficient — use `git range-diff`

E-2089 re-measured this population and found the prescribed fix falls short.
Patch-id survives a plain rebase, but `worktree land` rebases onto a moving main
and conflict resolution CHANGES the diff — after which the branch copy and the
landed copy have different patch-ids and read as unlanded forever. The four
README branches named above are exactly that case, and the earlier reading of
them as "genuinely absent from main" was wrong:

- `98bb7b24` landed as `785fe07b`; `4e611073` landed as `a8bfccff`. Same
  subjects, different SHAs AND different patch-ids. E-1832 is `confirmed` and
  its README rewrite is on main.
- `git cherry` still reports both as `+` on E-1803, E-1864, E-1865 and E-1929,
  which is the false positive this task exists to remove.

`git range-diff` pairs two commit series by content SIMILARITY rather than exact
patch-id, so a conflict-resolved commit is still matched to its landed
counterpart. Measured over every branch in this repo:

```
B=$(git merge-base main "$BR")
git range-diff "$B..$BR" "$B..main" | grep -E '^ *[0-9]+: +[0-9a-f]+ +< +-:'
```

Each matching row is a branch commit with NO counterpart on main. It clears
E-1803/1864/1865/1929 to zero unmatched, and still flags E-1115, E-1523 and
E-2010 — the branches that really did hold work main lacked. Drop rows whose
commit touches only `.endless/` and what remains is real unlanded work. Cost is
~1.5s on the worst branch here (320 commits against main's 1238), so it is fine
per-task and acceptable for an `--all` sweep.

## Two other readers want this same probe

Keep it one implementation, not three:

- **E-2095 part 2**, the not-on-main report, needs exactly "does this branch hold
  any commit main lacks?".
- **The reaper.** It is gated on `task_landings` — its comment says it "only
  touches dirs whose task has landed at least once" — so a worktree whose branch
  sits at main with nothing to land is settled and disposable but has no landing
  row and is never reclaimed. E-1360 and E-1697 are live examples: E-2084 reset
  both branches to main and both are still on disk holding nothing. The tempting
  shortcut is to record a landing so they reap; that writes a falsehood into the
  exact table the not-on-main report reads. The fix is to let the reaper accept
  "settled with nothing to land" alongside "landed", same TTL, same live-session
  refusal.
