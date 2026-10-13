# Plan: make an idle session monitor actually idle

## What the code actually does (corrects the brainstorm's premise)

The monitor is not Bubble Tea and nothing in it spins. `liveview.Loop` in `internal/liveview/liveview.go` runs one `time.NewTicker(Interval)` (2s), selects only on that ticker and OS signals, reads no stdin, and has no spinner or animation. The 100–250 wake-ups per second come from what each tick does:

- **git per row:** `monitor.AnnotateSessionStatusUnsettled` → `TaskWorktreeUnsettledDetail` → `worktreeUnsettledAt` runs `git status --porcelain`, and `cachedUnlanded` runs `git rev-parse HEAD^{commit}`, for every row with a worktree.
- **git for the focal row:** `monitor.WorktreeAnomalies` → `worktreeAnomaliesAt` runs a second `git status --porcelain` on the same worktree, plus `git symbolic-ref` and `git worktree list`.
- **Job trigger:** `jobs.RunDue` runs `ensureRow` (an INSERT) and `claim` (an UPDATE) for each of the ten registered jobs, so about 20 SQLite write statements per tick per monitor, even when no job is due.
- **Liveness:** `RefreshLiveness` runs tmux execs per server socket and rewrites the TEMP observation tables.
- **Fault row:** `faults.LogOnlyCount` re-reads `errors.jsonl` from its watermark every tick.
- **Pane fit:** `liveview.FitPaneToFrame` already resizes only when the height changes. It still runs `monitor.PaneWindowHeight`, a `tmux display-message` exec, on every changed frame. It also retries a failed resize on every repaint.

The cost of git is the work, not the exec. Measured on this machine, `git --version` costs about 0 ms of CPU, while `git --no-optional-locks status --porcelain` in an Endless worktree costs 40–80 ms, almost all of it system time spent `lstat`-ing every tracked file. Batching git calls into one exec would save nothing.

## Scope

This task cuts per-tick work that is wasted whether or not the pane is visible. It does not touch:

- per-row `git status` or `rev-parse` cadence;
- the liveness refresh;
- visibility.

All three belong to E-2229. Mike's answer settled that a visible pane keeps running git status every tick, and a hidden pane does no work at all.

## Steps

### 1. Measure first

Add an opt-in diagnostic, an env var such as `ENDLESS_MONITOR_PROFILE=<dir>`, read in `liveview.Loop`:
- Write a `runtime/pprof` CPU profile.
- Log one line per tick: tick wall time, plus the child-process CPU delta from `syscall.Getrusage(RUSAGE_CHILDREN)`.

Count child execs per tick with a counter on the monitor package's `runGit` seam (`internal/monitor/reap_worktrees.go`) and on the tmux exec helpers the monitor uses. Expose it through the same diagnostic.

Baseline one idle `session-status --monitor` pane: CPU %, idle wake-ups (`top -l 10 -stats pid,cpu,idlew,th -pid <pid>`), threads, and execs and SQLite writes per tick. Ablate by switching off jobs (`ENDLESS_NO_JOBS`), git and liveness in turn. If any ablation switch beyond `ENDLESS_NO_JOBS` is needed, keep it as a documented debug env var rather than a temporary hack. Record the baseline and after numbers in the task outcome, not in the plan.

### 2. Stop git's index rewrites

In the monitor package's `runGit`, pass the global `--no-optional-locks` before the subcommand on every call, so a read-only status never rewrites `.git/index`. That rewrite is what `fseventsd` reports. Apply the same flag in any other read-only `git status` call path reached from a monitor tick. Check with a repo-wide search for `"status", "--porcelain"`.

### 3. Do not run the focal worktree's git status twice

`worktreeAnomaliesAt` and `worktreeUnsettledAt` both run `git status --porcelain` on the focal worktree in the same frame. Compute it once per frame, with a per-frame memo keyed by worktree path that is passed in or held for the frame's lifetime, and give both consumers the result.

### 4. Make the job trigger free when nothing is due

In `internal/jobs/run.go`:
- Run `ensureRow` for each registered job once per process (the first `RunDue`), not every tick.
- Before any claim, run one read-only query for whether any registered job is due and unleased (or lease-expired), and return without writing when none is. Leave lease semantics, the compare-and-set `claim` and `complete` unchanged.

Target: zero SQLite writes from the job trigger on a tick where nothing is due.

### 5. Pane fit only asks tmux when it has to

In `liveview.FitPaneToFrame` / `liveview.Loop`:
- Remember the frame's needed height. Call `monitor.PaneWindowHeight` only when the needed height changed or the terminal size changed (SIGWINCH, which `Loop` can add to its signal set), not on every changed frame.
- After a failed resize, do not retry the same target height on every repaint.

### 6. Fault row reads the log only when it grew

`faults.LogOnlyCount` should `stat` `errors.jsonl` and skip the read when size and mtime are unchanged since its watermark.

### 7. Whatever else the profile shows

Fix in place if it is small. If the profile shows a large cost not listed here, stop and report it rather than growing scope.

## Acceptance

Measured before and after on one idle `session-status --monitor` pane, using the step 1 diagnostic:
- The job trigger issues zero SQLite writes on a tick where no job is due.
- The focal worktree gets one `git status` per frame, not two.
- `tmux display-message` for pane fit runs only when the needed height or terminal size changed.
- No `git status` from the monitor rewrites `.git/index`: its mtime is unchanged across ticks on a clean worktree.
- CPU and idle wake-ups per pane are lower, with numbers in the outcome.

"Zero child processes on an idle tick" and Mike's 30-window check are E-2229's acceptance, not this task's.

## Tests

- `runGit` argv includes `--no-optional-locks`, via the existing seam.
- `RunDue` with nothing due performs no writes. Assert with SQLite `total_changes()` on the test DB before and after.
- The focal-frame memo: a fake `runGit` counts one `status` call for the focal worktree per frame.
- Pane fit does not query tmux when the needed height is unchanged, via the `PaneWindowHeight` seam, extending `internal/sessionstatuscmd/pane_fit_test.go`.
- `LogOnlyCount` skips the read when the log is unchanged.
- `just test-go` and `just test` green.
