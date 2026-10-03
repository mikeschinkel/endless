On 2026-10-03, with 30+ tmux windows open and each running a `session monitor` pane, the machine's CPU sat at 100% (72% system, 0% idle; load average ~325 on 10 cores). The user found the ceiling is roughly 17–18 monitor panes; past that, the machine saturates.

Evidence from Activity Monitor and `ps` at the time:
- ~30 `endless-go` processes (one per monitor pane), each using 1.6–4.8% CPU, 16–23 threads and 100–250 idle wake-ups per second, with 22 minutes to 3+ hours of accumulated CPU time each.
- `fseventsd` at ~103% CPU, with 133+ hours of accumulated CPU time.
- A steady stream of short-lived `git -C <worktree> status --porcelain` processes across many task worktrees.

How it works today: each pane runs its own `endless-go session-status --monitor`, which redraws on a 2-second ticker (`liveview.Interval`). Each process independently reads the database and git state, so cost grows linearly with the number of panes. None of that work is shared between panes.

The goal is that an idle monitor costs ~0% CPU, so that 30+ windows (and more) are sustainable. The user wants this treated as a rearchitecture discussion, not a tuning pass.
