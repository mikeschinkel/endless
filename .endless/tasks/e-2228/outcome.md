## Measured: one idle `session-status --monitor` pane

Rig: the worktree build of `endless-go`, run against a `sqlite3 .backup` copy of the main database (`--db-dir`), focal E-2228 viewed as session ES-1330. It ran in a pane on a private tmux server, on the machine's real tmux socket directory (153 sockets, 2 live), with `ENDLESS_MONITOR_PROFILE`. Figures are averages per 2s tick.

| | before | after |
|---|---|---|
| tmux execs | 157 | 7 |
| git execs | 7 | 6 |
| job-scheduling writes (nothing due) | ~20 (counted in unit test) | 0 |
| tick wall time | 3.3s (longer than the 2s interval) | 0.8s |
| in-process CPU | 256ms | 73ms |
| child CPU | 995ms | 256ms |
| `top` %CPU (process only) | 7.1% | 3.6% (3.9% with the job trigger on, nothing due) |
| threads | 12 | 12 |
| idle wake-ups (`top` IDLEW) | 0 | 0 |

## What the profile found

- **Dead tmux sockets drove most of the cost.** Liveness probed each socket in the tmux socket directory with two tmux execs, and 151 of 153 were dead. This was not on the plan; it was folded in on Mike's call, and cleaning up the dead files is E-2310.
- **fork itself is expensive.** `sample` on a live monitor attributed about half its in-process CPU to `fork` and the atfork handlers. Every exec from a large Go process costs this, separately from the child's own CPU.
- **The 100–250 idle wake-ups per second did not reproduce on an idle tick.** The rig showed IDLEW 0 and 12 threads at baseline and after, with the job trigger on or off. The live installed monitors showed 9–600 IDLEW and 24 threads. The likely difference is jobs actually executing inside those processes (each monitor runs whatever due job it wins), not the idle loop. This is not proven.

## Remaining in-process cost (not fixed here)

About 52% of the remaining in-process CPU is the `SessionStatusRows` query (~38ms per tick), over `task_tree` and the session-lineage CTEs. Making it cheaper is query or view work, not a small fix, so per step 7 it is reported rather than folded in.

## Left for E-2229

The idle pane still runs about 6 git and 7 tmux execs per tick, including the per-row `git status` and the liveness refresh. That is E-2229's visibility and cadence scope, as is the under-0.1% CPU target.
