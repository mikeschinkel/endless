# An untargeted `tmux new-window` lands in whoever's session is in front

## The product defect

Anything in endless that shells out to `tmux new-window` without a
`-t <session>` target lands the window in the most-recently-active tmux session.
On this machine that is Mike's live `active` session. On a downstream user's
machine it is whatever they happen to have in front — a stray window appears in
an unrelated session with no indication of where it came from or what it belongs
to.

Find the call sites with `grep -rn 'new-window' src/ internal/`. One is in
`src/endless/session_cmd.py` (a `new-window -d -c <worktree>` invocation); the
others are the Go spawn path in `internal/spawnlaunchcmd` (`tmux_driver.go` and
`spawn_window.go`). The Go one is downstream of `emit_event`, so it is invisible
from the Python side and will not be found by reading the caller.

## The reproducer, which is a second and separable defect

`tests/test_task_claim_worktree.py::test_claim_binds_sibling_claude_session` is
the only test in that file that calls the ATTENDED path — `claim_item(tid)` with
no `unattended=True`. It patches `_resolve_session_id_with_prompt` to return a
fake sibling session id and then does real claim work: `create_task_worktree` in
a tmp project, an event emit, and whatever the spawn path does next.

So `just test` opens real tmux windows in the operator's live session, each
launching a Claude Code trust prompt for a pytest tmp_path. Observed while
E-1957 was running the suite: `pytest-173/174/175/177`, directory name truncated
by pytest to `test_claim_binds_sibling_claud0`, and worktree `e-1` because each
test gets a fresh isolated database in which the seeded task is always id 1.

## Why the existing isolation does not help

`tests/conftest.py` already deletes `TMUX` and `TMUX_PANE` to keep the resolver
helpers off the real server. That only stops code which BRANCHES on the env var.
The tmux CLI reaches the running server through the default socket regardless,
so anything that shells out unconditionally is unaffected.

## Fix the cause, not only the trigger

A test-only fix — making the test unattended, or stubbing the spawn boundary —
stops the noise here and leaves the untargeted `new-window` intact for every
downstream user. Both halves are needed:

- every `new-window` call site takes an explicit target;
- the test gets its own tmux server on a private socket (`tmux -L <name>`, torn
  down afterwards) or a stubbed boundary, so a test can never reach the
  operator's live session again.

## Provenance

Surfaced during E-1957, which ran the project-wide pytest suite as a regression
step. Diagnosis by the session in worktree e-2106, which traced the tmp_path
name back to the test and identified the two candidate call sites.
