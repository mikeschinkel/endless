# Run a task's verify in the user's tmux context

## Command

- **`endless verify E-N`**, a top-level alias of `endless task verify`
  (`task verify` stays, so existing handoffs keep working).
- Run on an agent's request, it opens a **background tmux window named
  `E-NNNN-verify`** in the user's current session.
- The command runs in a **fresh login, interactive shell** — the same as Mike
  opening a pane and typing it — not the tmux server's stored environment.

## Result

- The command **blocks on `tmux wait-for`** until the run ends, then prints the
  result and the CTRF path. The agent runs it as a background command, so the
  wait costs no tokens. (Later, once messaging via Claude Mods is common, a
  completion message replaces the block.)
- **On pass:** the window closes, the full log is kept beside the CTRF report,
  and the task becomes `unlanded`. **On failure:** the window stays.
- **A pass** is one complete run of the suite at a recorded commit with a clean
  worktree, every assertion passing.

## `--show` / `--hide`

- `endless verify E-N --show` brings the run's pane into the task's own tmux
  window — splitting the bottom-right pane and placing verify on its right —
  via `join-pane`. `--hide` moves it back out with `break-pane`.
- With no run in progress, `--show` opens a pane on the last run's log.
- Neither flag starts a run; they are mutually exclusive.

## Handoff runs are limited; working runs are not

- Only runs the agent requests through this command after declaring the work
  ready count. An agent's own runs during implementation are not counted.
- After **3 failed handoff runs**, stop and ask Mike.
- A handoff run after the task's `verify.sh` changed since the first handoff
  failure **waits for Mike's approval**. (An adversarial agent writing the
  suite is a later task.)

## First

Confirm that Claude Code lets the agent's Bash create the window (probed OK in
the E-2225 session) and `join-pane` into Mike's window.

## Verify

A suite proving: the window is named for the task and runs in a login shell
(no agent-only variables in its environment); the command blocks and returns
the result and CTRF path; a pass closes the window, keeps the log and sets
`unlanded`; a failure keeps the window; `--show`/`--hide` join and break the
pane and refuse together; the fourth failed handoff run and a run after a
`verify.sh` change both stop for approval.
