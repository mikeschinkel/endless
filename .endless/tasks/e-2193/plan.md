# Restart a monitor in place when its binary is replaced

## Where

`liveview.Loop` (internal/liveview), the loop behind both `session monitor`
and `project monitor`. It is also what fires background jobs (`FireJobs`), so
the fix and the jobs it protects live in one place. No change to the Python
launcher is needed: it waits on the Go process, and an in-place exec keeps the
same PID.

## Detection (decided: file identity)

- At loop start, record the identity of the running binary: resolve
  `os.Executable()` through any symlink (`filepath.EvalSymlinks`), then stat it
  — device, inode, size, mtime.
- On every tick, resolve the SAME `os.Executable()` path again and stat the
  result. Re-resolving each time matters: an install may repoint the symlink
  (`/usr/local/bin/endless-go` → a checkout's `bin/`) instead of overwriting
  the file, and comparing against the file resolved at startup would miss it.
- Never look the binary up by name. E-1063 renames `endless-go` to `endless`;
  `os.Executable()` follows the rename, a hardcoded name would not.
- Treat it as replaced only when the new identity differs from the start
  identity AND has been the same for two consecutive ticks, so a half-written
  build is never exec'd.

## Action (decided: re-exec in place)

- Before exec, probe the new binary: run it with a cheap command (e.g. its
  version/help) under a short timeout; a non-zero exit or timeout is a failure.
- On success, restore the terminal state the loop set up (cursor, alternate
  screen, raw mode, whatever `liveview` changed), then `syscall.Exec` the
  resolved path with the original `os.Args` and `os.Environ()`. Same pane, same
  PID; the new binary redraws from scratch.
- Do not fire jobs on the tick that decides to exec.

## Failure (decided: stop jobs + notice)

If the probe or the exec fails, keep rendering with the old binary but:
- stop firing background jobs for the rest of this process's life — a stale
  binary must never again run a job the new install may have retired (the
  WARN-0001 `triage-sufficiency` failure that prompted this task);
- show a one-line notice in the frame naming what failed and saying to restart
  the monitor;
- record one fault (existing catalog code if one fits, else a new WARN code
  documented in docs/errors.md) so it reaches the fault row.

## Scope (decided: everywhere)

The same rule for every monitor, including a self-dev worktree's, whose
`bin/endless-go` is rebuilt by every `just build` — picking up each rebuild is
the desired behavior. PRODUCT: another project's monitors only ever see the
global install, so they restart once per upgrade.

## Tests

- Unit: the identity check (unchanged / changed once / changed and stable /
  symlink repointed with the target file untouched) against temp files and
  symlinks.
- Unit: a failed probe disables job firing and produces the notice; a
  successful one calls the exec seam (seam `syscall.Exec` so the test does not
  replace the test binary).
- A verify-suite check: start a monitor loop on a copy of the binary in a
  temp dir, replace the copy, and assert the process re-execs (e.g. a marker the
  new build writes, or the PID's executable identity changing).

## Acceptance

- After `just install`, every running `session monitor` / `project monitor`
  runs the new binary within a few ticks, with no pane restarted by hand.
- A monitor whose replacement cannot start keeps rendering, fires no jobs, and
  says why.
- Nothing names the binary by `endless-go`; the check survives the E-1063
  rename.
