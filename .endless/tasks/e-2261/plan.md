# Settle verify and land around the user's own context

Today Mike runs verify and land himself, because a verify an agent ran green
has often failed when he ran it. After a land he sets the task `assumed`
almost every time. This epic moves the verify into Mike's context on the
agent's request, makes a successful land settle the task, and later lets an
agent land tasks marked for it.

## The lifecycle this epic produces

```
underway → unverified → unlanded → (land) → assumed
             ↑______________|  a commit after the pass
```

- **`unlanded`** — new status: a verify passed in the user's context at a
  recorded commit, and the work awaits a land.
- Land is allowed only from `unlanded`, and sets `assumed`.
- A commit after the pass returns the task to `unverified`, deterministically
  (Q15, below).

## Children, in order

### E-2266 — Find why verify passes for agents but fails for the user (research)
Blocks E-2263 (Q13). Review past transcripts for "it worked for me" cases and
the explanations given; classify each cause as intended or a bug. Bugs get
fixed at the source rather than worked around.

### E-2262 — Have a successful land set the task to assumed
- **`unlanded` status** added to the transition table (and so the lifecycle
  diagram): `unverified → unlanded` on a passing verify; `unlanded → assumed`
  on land; `unlanded → unverified` when the branch moves past the passed
  commit.
- **Who sets `unlanded` before E-2263 exists:** `endless task verify` sets it
  on a pass **only when the run is not an agent's** (the same agent/user
  distinction `agentenv` already draws). An agent's working runs during
  implementation never move the status. *Proposed — confirm in review.*
- **The pass records its commit.** Any `endless` read of the task compares it
  with the branch's latest commit and records `unlanded → unverified` when they
  differ (Q15 B). No git hook.
- **Land:** refuses unless the task is `unlanded` and its passed commit is the
  branch's latest commit. On success it sets `assumed` when the task's type
  settles on land.
- **Settles-on-land is a column on `task_types`** (Q1 A), like
  `auto_spawnable`: true for `todo` and `bugfix`, and for future types such as
  `docs`.
- **Land refuses `research` and `brainstorm`** outright (Q16): their
  deliverable is the outcome text, never files. Their worktrees stay for now.
- **Opt-out:** `land --keep-status` only (Q3 C). No per-task field here.
- Implemented where land lives today, in Python (Q14 A).

### E-2263 — Run a task's verify in the user's tmux context
- **Command:** `endless verify E-N`, a top-level alias of `endless task
  verify` (dropping `task` for a common verb). Run on an agent's request, it
  opens a background tmux window named `E-NNNN-verify` in the user's current
  session (Q4 A).
- **Environment:** the command runs in a fresh login, interactive shell — the
  same as Mike opening a pane and typing it (Q5 B).
- **Result:** the command blocks on `tmux wait-for` until the run ends, then
  prints the result and the CTRF path; the agent runs it as a background
  command so the wait costs nothing (Q6 A). Later, once messaging via Claude
  Mods is in common use, a completion message replaces the block (Q6 C).
- **On pass:** the window closes and the full log is kept beside the CTRF
  report (Q4b A); the task becomes `unlanded`. **On failure:** the window
  stays.
- **`--show` / `--hide`:** `endless verify E-N --show` brings the run's pane
  into the task's own tmux window — splitting the bottom-right pane and placing
  verify on its right — via `join-pane`; `--hide` moves it back out with
  `break-pane`. With no run in progress, `--show` opens a pane on the last
  run's log. Neither flag starts a run; they are mutually exclusive.
- **Handoff runs are limited, working runs are not (Q7 C):** only runs the
  agent requests through this command after declaring the work ready count.
  After 3 failed handoff runs, stop and ask Mike. A handoff run after the
  task's `verify.sh` changed since the first handoff failure waits for Mike's
  approval. (An adversarial agent writing the suite is a later task.)
- **A pass means:** one complete run of the suite at a recorded commit with a
  clean worktree, every assertion passing (Q8 C).
- Prerequisite check: confirm Claude Code lets the agent's Bash create the
  window (probed OK in the E-2225 session) and `join-pane` into Mike's window.

### E-2265 — Wait out an unreachable external service in verify and land
- **Hosts are declared in `verify.toml`** and probed before the suite starts;
  the run waits and retries until they answer (Q9 C). Land's own network
  calls, if any, get the same wait.
- **The wait limit is configurable,** default 30 minutes (Q10 B). On timeout
  the failure names the service that was down.

### E-2264 — Let an agent land tasks marked for auto-land (later)
- **`auto_land` field on tasks, nullable**: null means the project default
  (true); true or false only when set on purpose (Q11 B).
- An agent may land an `unlanded` task whose `auto_land` resolves true, by
  running land in the user's tmux context through E-2263's mechanism.
- **Edit ED-1605's landing sentence** to say so (Q12 A).

## Not in this epic
- Requiring every plan deviation to be approved before a verify is requested
  — Mike's process task, still to be filed.
- A `prototype` task type — trialled first as research in E-2267.
