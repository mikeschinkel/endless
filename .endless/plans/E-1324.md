You are working on **E-1324** (Fix `endless session status add` hang after successful insert).

## What the bug looks like

`endless session status add` is invoked with valid XML. The Go-side flow completes successfully — the row lands in `session_statuses`, child rows in `session_status_tasks`, the markdown render is computed and (in observed cases) ALREADY flushed to the calling shell's stdout buffer. But the CLI process does not terminate. From the calling shell's perspective the command appears hung; in Claude Code's Bash tool this shows as "Channeling..." for 1–2+ minutes until a manual interrupt.

Three concrete observed incidents in session 389:

- First reproduction: ~2m 15s before manual interrupt. Row inserted at `2026-05-14T20:52:48`; output already in stdout buffer showing the full markdown render including "## Notes" / "Four dogfood misses..." (visible in the Bash tool output, 46 lines / 1m 56s elapsed).
- Second reproduction: identical symptom after retrying the same `add` invocation with slightly reworded XML (the rewording is incidental — the hang reproduces independently). Inserted at `2026-05-14T20:54:48`, hung until manual interrupt.
- Reproduction at file-time of this task: same shape, same hang. The bug is consistent.

The INSERT succeeded each time. Verify with:

```
endless sql "SELECT id, created_at FROM session_statuses WHERE session_id = 389 ORDER BY id DESC LIMIT 5"
```

## What's likely happening (your starting hypothesis to validate or refute)

`src/endless/event_bridge.py:emit_event` calls `subprocess.run(cmd, capture_output=True)`. `capture_output=True` blocks until BOTH the subprocess exits AND its stdout/stderr pipes drain to EOF. If the child process holds an inherited file descriptor open past its main goroutine's return, the pipe never closes, and `run()` blocks forever (well, until the parent dies or interrupts).

Candidate fds that could leak in `endless-event`:

- `*sql.DB` connection pool — if `Execute()` returns without explicitly closing, the pool's keep-open connection might keep the process from exiting. Go's `database/sql` should clean up on `main()` exit but sqlite drivers sometimes have shutdown quirks.
- Stdout/stderr buffers that aren't explicitly `Sync()`'d before `os.Exit()`.
- An inherited fd from `endless-event`'s own subprocess (it shells to `git` for the ledger commit — `internal/events/commit.go` `CommitLedgerSegment` uses `exec.Command.CombinedOutput`; if THAT subprocess leaks fds back to the parent, the parent's stdout pipe could stay alive).

The third hypothesis is the most likely. `CombinedOutput()` reads stdout+stderr fully and waits for the git subprocess to exit. But if git inherits the parent's stdout fd and doesn't close it before exiting, that fd remains alive in the parent. The Python `subprocess.run(..., capture_output=True)` is reading from the parent's stdout pipe; the pipe's write end is still held by... someone (the git fd in the worst case, or a stray Go runtime fd).

## 1. Get oriented

```
endless guide
endless guide sessions
endless task show E-1324 --text
endless task show E-1312                       # original CLI ship — most of the suspect code
endless task show E-1206                       # ledger-commit path (where CombinedOutput is called)
endless task show E-1315                       # related deadlock fix (different cause, similar shape)
endless task claim E-1324
```

Read the suspect code paths:

- `src/endless/event_bridge.py` — `emit_event` function; the `subprocess.run(cmd, capture_output=True)` call is the symptom site
- `cmd/endless-event/main.go` — `runEmit` / `run` flow; check the exit path after Execute returns
- `internal/events/executor.go` — `Execute()` opens a `*sql.DB` transaction; check commit + return path
- `internal/events/commit.go` — `CommitLedgerSegment` invokes git subprocess via `exec.Command.CombinedOutput`; suspect for fd inheritance
- `internal/events/session_status.go` — `execSessionStatusRecorded`; this handler is what session_status emits trigger

## 2. Do the work

Enter plan mode.

Diagnostic steps before designing the fix:

1. **Reproduce with the simplest possible XML.** A `<session-status><headline>test</headline></session-status>` payload. If the hang reproduces, the bug is independent of payload complexity.
2. **Reproduce outside Claude Code's Bash tool.** Run from a vanilla shell. If it hangs there too, the bug is in endless-event, not in Claude Code's tool integration.
3. **Strace/dtrace the endless-event process** at hang time:
   ```
   sudo dtrace -p $(pgrep endless-event) -n 'syscall:::entry { ustack(); }'
   ```
   or use `lsof -p <pid>` to see what fds are open.
4. **py-spy or strace the Python parent** to see where `subprocess.run` is blocked:
   ```
   py-spy dump --pid $(pgrep -f 'endless session status add')
   ```
5. **Check if endless-event terminates AT ALL** — `ps` for the pid; does the process actually exit, or is it sitting in some final stage?

Once the cause is isolated, candidate fixes (don't apply until you've validated the root cause):

- **Explicit `os.Exit(0)` at end of `runEmit`** — forces process termination regardless of background goroutines. Cheap; effective. May lose deferred cleanup.
- **Close stdout/stderr explicitly** before return in Go (`os.Stdout.Sync()` then `os.Stdout.Close()`).
- **Switch Python `subprocess.run` to `Popen` + read-then-wait with timeout.** Drain stdout/stderr explicitly, then wait. Bound the wait at a few seconds; if exceeded, kill the subprocess.
- **Explicitly close `*sql.DB` in `Execute()`'s caller** before returning from `endless-event`. Sqlite drivers can hold a daemon goroutine on the connection pool.
- **Pass `exec.Command.Stdin = nil` AND explicit nil for stdout/stderr** to git subprocesses so they can't inherit fds back from the parent. Worth trying as a no-cost defensive measure.

## 3. Closing

When implementation is complete:

- `endless task update E-1324 --status verify` with how-to-test commands. Test should be: run `endless session status add` with a real payload and confirm the CLI returns within 1–2 seconds (not minutes).
- **Dogfood the fix** by recording a session_status row via the now-fixed CLI. The fact that you can record without hanging is the verify.

Don't run `endless worktree land` without asking Mike. Don't mark `confirmed` yourself.

Goal: `endless session status add` returns promptly after the markdown is rendered, with no orphaned subprocess and no fd leak. After this lands, the dogfood reflex is materially cheaper (instant feedback instead of 2-minute hangs), and recordings stop accidentally producing duplicates from retry-after-interrupt.
