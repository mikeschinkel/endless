# Give verify suites a person default and an as_agent wrapper

## Why

E-2266 found that 14 of 32 "passed for the agent, failed for Mike" cases had one
cause: a suite runs as whoever called the runner. `isolatedEnv` replaces only
`HOME` and `XDG_CONFIG_HOME`, so the caller's identity variables reach the suite,
and the `endless task verify` front door adds `ENDLESS_AUDIENCE=agent` on top.
Called by an agent, every check sees the agent's world: the hook gate is open,
refusals and footers render in agent form, and session resolution goes through
`CLAUDE_CODE_SESSION_ID`. Called by Mike, every check sees his. So no single run
tests both experiences, and the same suite gives different verdicts to
different callers. Each case so far was patched in its own suite. E-2272
(2026-10-08) shows that doesn't hold up: its suite deliberately simulated a
person, missed `ENDLESS_AUDIENCE`, and still passed only for the agent.

A verify suite should test BOTH the person's experience and the agent's, whoever
runs it: during implementation and in the handoff run E-2263 covers. This task
does that in the runner, so it applies to both. E-2263 stays the post-milestone
run in the user's context, and the two are related, not overlapping.

## Scope boundary: landed suites are history

Do NOT go back and change any landed task's verify suite. A landed suite is a
pre-land proof of one task at one moment, and it is never run again unless that
task is revisited. If it is, that task's own session fixes its own suite.
Unlanded suites that already set these variables by hand keep working unchanged:
their inline settings still win. They are not migrated either.

## Changes

self_dev note: almost all of the evidence comes from Endless verifying Endless,
where the system under test reads these variables. The changes are still generic
(PRODUCT): in any project a suite's verdict must not depend on who started it,
and no project's suite should be able to reach the user's live tmux server.


### 1. The suite's default environment is a person's

In `internal/verifycmd`, build the suite env from the caller's env minus the
caller-identity variables, and do it in one named list next to `replaceEnv`:

- harness identity: `CLAUDECODE`, `CLAUDE_CODE_*` (and any other `CLAUDE_*`),
  `AI_AGENT`, `__CFBundleIdentifier`
- audience: `ENDLESS_AUDIENCE`
- session routing: `ENDLESS_SESSION_ID`
- tmux context: `TMUX`, `TMUX_PANE` (replaced by a private server, see 1b)

Take the harness variable names from `internal/agentenv`, so the detector table
and this list cannot drift. Apply the same stripped env to the sandbox reset/seed
that runs before isolation (it keeps the real `HOME`, which it needs to read the
main database). Then the seeded sandbox session is the same whoever runs the
suite: today it takes the agent's `CLAUDE_CODE_SESSION_ID` or the null UUID,
depending on the caller.

Front door: `endless task verify` stops exporting `ENDLESS_AUDIENCE` into the
runner's environment. The runner strips it anyway; this removes the second source.

### 1b. A private tmux server per run, with `with_tmux` to opt in

Removing `TMUX`/`TMUX_PANE` on its own isolates nothing. A `tmux` subprocess with
neither variable set still connects to the user's live server on the default
socket (checked 2026-10-08: `env -u TMUX -u TMUX_PANE tmux list-sessions` listed
the live sessions). And about 45 sites read these variables directly, through no
shared accessor, so the caller's pane changes session resolution, hook pane
recording, spawn/resume windows, status line and autospawn. E-2266's probe got
three different session answers from three panes, and E-2071's sweep bound a
fixture session to Mike's live pane %413.

So the runner gives each run its own empty tmux server:

- Unset `TMUX` and `TMUX_PANE`, and set `TMUX_TMPDIR` to a per-run directory
  created under **`/tmp`** (`/tmp/endless-verify-<short id>/`), not under the OS
  temp dir. On macOS `os.MkdirTemp("")` uses `$TMPDIR`
  (`/var/folders/…/T/`), and with the canonicalized run dir that pushes tmux's
  socket path (`<dir>/tmux-<uid>/default`) past the ~104-byte limit, which fails
  with "File name too long". Under `/tmp` (which resolves to `/private/tmp` on
  macOS) it is about 51 bytes on any machine and independent of `HOME`. It
  stays out of `~/.config/endless`, so the suite is not handed a path to the
  real config (ED-1583), and a socket is runtime state, not config (E-2186).
- Every `tmux` call in the suite then reaches the private server, never the
  user's. With no server started it fails the same way for everyone, and every
  Endless read site takes its supported "not in tmux" branch.
- **Teardown kills the private server** (`tmux kill-server` under the same
  `TMUX_TMPDIR`) and removes the directory, even when setup or a check fails,
  in the same deferred `teardown` as the run dir. Otherwise a server outlives
  the run.
- `_harness.sh` gains `with_tmux`. It starts a detached session on the private
  server (`tmux new-session -d`), exports `TMUX` and `TMUX_PANE` from that
  fixture pane, and lets the suite's checks run "inside tmux". The pane is a
  fixture, so the result is the same whoever runs the suite. It combines with
  `as_agent` for checks that test an agent inside tmux. `verify.toml` checks get
  the same through a per-check key (e.g. `tmux = true`).

### 2. `as_agent`: a check that tests the agent's side says so

- The runner writes the agent environment to a file in the per-run dir and
  exports its path (e.g. `ENDLESS_VERIFY_AGENT_ENV`). The values are synthesized,
  not copied from the caller, so they are identical whoever runs the suite:
  `CLAUDE_CODE_ENTRYPOINT=cli`, `CLAUDECODE=1`, a fixed fixture
  `CLAUDE_CODE_SESSION_ID`, `AI_AGENT`, `ENDLESS_AUDIENCE=agent`. Take them from
  agentenv's observed-environment table, and keep the session id fixed per run.
- `_harness.sh` gains `as_agent <cmd> [args…]`, which runs one command with that
  environment layered on the person default. The default needs no wrapper.
- `verify.toml` checks get the same through a per-check key (e.g.
  `as = "agent"`). The driver layers the file onto that check's env.
- PRODUCT: the runner is generic. In another project, any tool that changes
  behaviour under `CLAUDECODE` (the variable exists for that) diverges the same
  way, so the person default is not Endless-specific. `as_agent` gives "the
  supported harness" as agentenv defines it, not anything Endless-only.

### 3. Run a snapshot of `verify.sh`

bash reads a script as it runs, so an agent editing the suite during Mike's run
breaks his run (E-2232: "syntax error near unexpected token"). Copy the suite
into the per-run dir and run the copy. Keep the layout the suite sources through
(`$(dirname "${BASH_SOURCE[0]}")/../_harness.sh`, which sources `_guard.sh`) by
copying the suite's directory and the shared harness files with the same relative
layout. `ENDLESS_VERIFY_DIR` still names the real suite directory. `_guard.sh`'s
"not yours" check reads the worktree from the running file's path and fails
open on a path without one. The runner's own `guardOwnTaskOnly` already refuses
foreign suites before anything runs, so nothing is lost. Say so in a comment
where the copy is made.

### 4. Docs

`.endless/tasks/CLAUDE.md` and the guide's verify section: a check runs as a
person unless wrapped in `as_agent`; test both sides where they differ; never
read identity from the caller. Include the zsh trap: `env $VARS cmd` does not
word-split in zsh, so a hand-rolled strip silently does nothing.

## Verification

This task's own suite, run through `endless task verify`:

- The same suite gives the same verdict and the same per-check output from an
  agent's shell and from a shell with every identity variable removed (strip with
  bash or an array, not `env $VARS` in zsh).
- Default env: `endless task show E-99999999 --db sandbox` renders the person's
  refusal (no `Handle this yourself`), and the footer is `db:` rather than `# db:`.
  The env has no `CLAUDE*`, `AI_AGENT`, `ENDLESS_AUDIENCE` or `ENDLESS_SESSION_ID`.
- `as_agent`: the same command renders the agent form. A `endless-go hook claude`
  payload is acted on (hook gate open). `endless session id --db sandbox` resolves
  the fixture session.
- `verify.toml` with `as = "agent"` on one check: that check sees the agent env,
  and its sibling does not.
- tmux: by default `tmux list-sessions` in the suite reaches no live session
  (the private server is empty), and `TMUX`/`TMUX_PANE` are unset. Under
  `with_tmux`, `TMUX_PANE` names the fixture pane and session resolution in the
  sandbox gives the same answer from an agent's pane, a sibling pane and a
  shell outside tmux. After the run, no server is left on the private socket and
  its directory is gone, including after a failing check.
- Snapshot: a suite that rewrites its own `verify.sh` mid-run still completes as
  the original script.
- Unit tests in `internal/verifycmd` for the strip list, the agent env file and
  the snapshot layout. `just test-go` and `just test` stay green.
