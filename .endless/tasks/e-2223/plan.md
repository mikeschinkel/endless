Open questions for this brainstorm. Answer each in place on its **Answer:** line; the options, pros/cons and recommendations are there to answer from, not decisions already made.

## What every monitor pane does today

Every monitor pane runs this loop on its own, every 2 seconds (`liveview.Interval`):

- **Database query** for its rows.
- **`git status --porcelain`** for every row that has a worktree. The expensive `git range-diff` "unlanded" verdict is already derived state written by one background job (E-2128 / ED-1589); the dirty check is not, and stays live.
- **Extra git checks** on the focused worktree (`WorktreeAnomalies`).
- **A tmux resize** so the pane fits its own frame (E-1851), on every repaint.
- **A job-runner trigger:** every pane fires `jobs.RunDue`, and a database lease picks one winner.

With 30 panes and about 20 rows each, that's on the order of 150 `git status` runs per second. That probably explains `fseventsd`: `git status` refreshes each worktree's index, and macOS reports every write.

## Q1. Should we measure before redesigning?

| Option | Pros | Cons |
|---|---|---|
| **A. Profile first** (per-tick CPU and child processes, with git/tmux/jobs/DB each switched off in turn) | The redesign targets measured costs; settles whether `git status` drives `fseventsd` | Delays the redesign by one measurement pass |
| **B. Redesign from the code reading above** | Starts now | If the cost turns out to be the threads, the 2s wakeups or the jobs trigger rather than git, the redesign misses it |

**Recommendation: A.** One cheap profile turns the rest of these answers from guesses into facts.

**Answer:**

## Q2. Where does the shared work live?

| Option | Pros | Cons |
|---|---|---|
| **A. One hub process computes the state; panes are thin clients that subscribe to it** | Cost is flat in the number of panes; an idle pane is just a blocked read, ~0% CPU | A new long-lived process to start, find and recover when it dies |
| **B. Monitors elect a leader; the leader writes a snapshot and the others read it** | No new process type | A leader handoff when its window closes; followers still wake up to check the snapshot |
| **C. Keep panes independent but make each one cheap** (Q3–Q5 applied per pane) | Smallest change; no coordination | Cost still grows with every pane, just with a smaller slope, so the ceiling moves rather than goes away |
| **D. One process rendering into every pane's terminal** | Cheapest possible | Panes no longer own their process; a crash blanks every window at once, and tmux pane lifecycles get harder to follow |

**Recommendation: A.** It's the only option where 30, or 100, windows cost about the same as one.

**Answer:**

## Q3. What triggers a recompute?

| Option | Pros | Cons |
|---|---|---|
| **A. Events:** the event pipeline notifies on every write, and a file watcher on worktrees catches git changes | Zero work while nothing changes, which is the literal ~0% goal | A file watcher behaves differently on macOS (FSEvents) and Linux (inotify), and inotify watches are limited per user |
| **B. Polling with a cheap change check:** recompute only when the database's change counter or a worktree's index modification time moves | One code path on every OS; nothing to subscribe to | Still wakes on a timer, so not quite 0% |
| **C. Adaptive polling:** back off to around 30s when idle, speed up on activity | Simplest change to today's loop | A slow-tick monitor is stale exactly when activity resumes |

**Recommendation: A for database changes; B for git state.** The event pipeline already sees every write, and an mtime check portably replaces a file watcher.

**Answer:**

## Q4. How is "is this worktree dirty?" answered?

| Option | Pros | Cons |
|---|---|---|
| **A. Derived state written by one background job, as the unlanded verdict already is** | Reuses an existing pattern; monitors only read | Dirty status lags by the job's cadence |
| **B. Recompute only when the worktree's index or HEAD modification time changes** | Fresh and cheap; still `git status` when something did change | An edit to a tracked file that hasn't been staged doesn't touch the index, so an mtime check alone misses it |
| **C. Mark the worktree dirty from Claude's hook events** (Write/Edit/Bash) | No git calls at all for agent edits | Misses edits made by a person in an editor or shell |
| **D. Keep live `git status`, just less often** | Trivial | Leaves the cost that most likely drives `fseventsd` in place |

**Recommendation: A, fed by B's trigger.** One process runs `git status` only for worktrees whose state moved, and every pane reads the result.

**Answer:**

## Q5. Who fires the job runner?

| Option | Pros | Cons |
|---|---|---|
| **A. Keep every monitor firing it each tick** (the database lease picks one winner) | No change | N panes means N lease attempts every 2s, all for nothing |
| **B. The hub from Q2 fires it** | One trigger, wherever the hub lives | Jobs stop when no monitor is open, unless the hub outlives its clients |
| **C. A separate OS scheduler** (launchd, systemd timer, cron) | Jobs run with no monitor open at all | Needs per-OS install and uninstall from `endless setup` |

**Recommendation: B.** It's the natural home if Q2 is A. Choose C only if you want jobs to run with no monitor open.

**Answer:**

## Q6. Hub lifecycle (only if Q2 is A)

| Option | Pros | Cons |
|---|---|---|
| **A. The first monitor starts it; it exits a while after the last client disconnects** | Nothing to install; nothing runs when you're not looking | Start-up races when several windows open at once, which needs a lock |
| **B. An OS service installed by `endless setup`** | Always there; restarted by the OS | Per-OS service files; it runs even when no monitor is open |
| **C. The existing long-lived `session-status --monitor` becomes the hub** | No new binary role | Couples the hub to one view; `project monitor` would need the same treatment |

**Recommendation: A.** It works the same on any machine with no setup step.

**Answer:**

## Q7. What does "~0% at idle" mean as an acceptance test?

| Option | Pros | Cons |
|---|---|---|
| **A. Measured: under 0.1% CPU per idle pane, and no child processes spawned on an idle tick** | Testable in a verify suite; "no child processes" catches git and tmux regressions directly | CPU percentages vary by machine and need a tolerance |
| **B. Structural: an idle pane makes no system calls beyond a blocked read** | Holds on any machine | Hard to assert automatically |
| **C. The original scenario: 30+ windows with the machine above 90% idle** | Exactly the case that prompted this task | Not reproducible in CI, so a manual check only |

**Recommendation: A, with C as the confirming manual check.**

**Answer:**

## Q8. What does this brainstorm deliver?

| Option | Pros | Cons |
|---|---|---|
| **A. An accepted decision plus child tasks for each piece** (profile, hub, dirty-state job, job-trigger move, pane-fit) | Each piece lands and gets verified on its own | none |
| **B. One implementation task carrying the whole design** | One land | Couples a risky redesign of a process every window runs into a single revert |

**Recommendation: A.**

**Answer:**

## Not a question

The tmux resize runs on every repaint, even when the frame height hasn't changed. Skipping it when the height is unchanged is cheap under any design, so it folds into the first child task.
