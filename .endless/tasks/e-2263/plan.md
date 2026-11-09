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

## The reconciliation gate (from E-2280)

- Before it opens a window, the verify request calls the shared reconciliation
  gate (`internal/reconcilegate`, the same check `task update --status
  unverified` runs) and refuses while the task's reconciliation is stale or a
  discrepancy question is unanswered. When this task's plan is itemized, this
  is its own plan item.

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



## Rebase onto main before the run (from E-2577)

- Before it runs the suite, verify rebases the branch onto main, using the
  rebase preparation the land already has. The pass is then taken on the tree
  that will land, so the land's own rebase onto an unmoved main changes nothing.
- The pass records the main commit it was rebased onto. Under E-2577 the land
  refuses and names verify when main has moved past that commit (a sibling
  task under E-2261).
- **A rebase conflict** stops the run before the suite starts. Verify writes no
  pass and moves no status. It tells the owning session through the error
  triage job's routes: a live, idle session gets a SendMessage from the
  throwaway `claude -p` sender, and an ended session is resumed in a background
  window with the message as its first prompt. Pull those routes out of
  `internal/triagejob` into a shared piece both jobs call; do not copy them.
- The message names the conflicted files and asks the session to resolve them
  and request verify again. The user is asked nothing.
- "The task becomes `unlanded`" above changes with the sibling task that
  derives `unlanded`: a pass is recorded, and whether it stands is computed.
