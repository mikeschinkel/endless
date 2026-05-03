# E-1114 — Isolate `endless-sandbox enter` subshell in its own process group

## Context

On 2026-05-02 during E-1074 verify, a leaked `endless-sandbox enter probe` (Go parent PID 36088) survived from 02:52 AM for hours. Its subshell descendants kept writing to `~/.cache/endless/sandboxes/probe/` even after `endless-sandbox destroy probe` succeeded — files kept reappearing. Mike got stuck in a destroy/enter loop until he killed the parent Go PID by hand.

**Root cause:** `cmd/endless-sandbox/enter.go` runs `exec.Command(shell, "-i").Run()` with no process-group isolation and no signal handler. Any descendant the subshell forks (zsh hook daemons, prompt jobs) inherits the parent's pgroup, and when the parent Go binary dies via SIGKILL (or simply isn't told to clean up its tree on shutdown) those descendants are reparented to init and outlive everything. `cmd/endless-sandbox/run.go` has the same defect — its existing signal handler kills only the direct child, not the tree.

**Why now:** E-1078 (pytest sandbox fixture) will hit this on every test that opens a shell-like child. E-1079 (orphan warning) needs reliable cleanup or its count is meaningless. Hardening before those land prevents normalizing the leak.

**Branch base:** E-1074 has not merged to main; `cmd/endless-sandbox/` only exists on `task/1074-sandbox-go-binary`. The worktree must be branched from that branch, not main.

## Approach

Introduce a `Supervisor` type that embeds `*exec.Cmd` and shadows its `Run()` to add process-group isolation, signal forwarding, and guaranteed descendant cleanup. Wire `enter.go` and `run.go` to construct supervisors instead of bare `exec.Cmd`s. Extend `sandbox_test.go` with three tests proving the contract.

The embedded `*exec.Cmd` means callers configure `sup.Stdin`, `sup.Env`, `sup.Stdout`, etc. exactly as they would on a plain `exec.Cmd` — drop-in shape — but the outer `sup.Run()` wins via Go method resolution, so it's hard to accidentally bypass the supervision. After `Run()` returns, `sup.ProcessState` carries the exit info exactly like the std-lib pattern.

### New file: `cmd/endless-sandbox/supervisor.go`

```go
package main

import (
    "os"
    "os/exec"
    "syscall"
    "time"

    "golang.org/x/term"
)

// Supervisor wraps *exec.Cmd to run a child process in its own process group,
// forward signals to the whole group, and guarantee all descendants are
// dead before Run() returns. It mirrors the exec.Cmd API: configure fields
// (Env, Stdin, Stdout, Stderr) on the embedded Cmd, optionally set Signals,
// then call Run. ProcessState carries exit info on return.
type Supervisor struct {
    *exec.Cmd

    // Signals, if non-nil, is consumed once: when a signal arrives while
    // the child is alive, it's forwarded to the whole process group.
    // A nil channel disables forwarding (Run waits only for natural exit).
    Signals <-chan os.Signal
}

// NewSupervisor mirrors exec.Command — name plus args.
func NewSupervisor(name string, args ...string) *Supervisor {
    return &Supervisor{Cmd: exec.Command(name, args...)}
}

// Run shadows (*exec.Cmd).Run. Method resolution picks this one whenever a
// caller has a *Supervisor; bypassing it requires explicitly calling sup.Cmd.Run.
func (s *Supervisor) Run() error {
    if s.SysProcAttr == nil {
        s.SysProcAttr = &syscall.SysProcAttr{}
    }
    s.SysProcAttr.Setpgid = true
    // Only claim controlling TTY when stdin is one. In tests stdin is a
    // pipe; Foreground+Ctty against a non-TTY fails at fork.
    if term.IsTerminal(int(os.Stdin.Fd())) {
        s.SysProcAttr.Foreground = true
        s.SysProcAttr.Ctty = int(os.Stdin.Fd())
    }

    if err := s.Cmd.Start(); err != nil {
        return err
    }
    pgid := s.Process.Pid // Setpgid w/ Pgid=0 → child's pid IS the new pgid

    waitCh := make(chan error, 1)
    go func() { waitCh <- s.Cmd.Wait() }()

    var err error
    select {
    case err = <-waitCh:
    case sig := <-s.Signals: // nil channel blocks forever — desired
        if syssig, ok := sig.(syscall.Signal); ok {
            _ = syscall.Kill(-pgid, syssig)
        } else {
            _ = syscall.Kill(-pgid, syscall.SIGTERM)
        }
        err = <-waitCh
    }

    killGroup(pgid)
    return err
}

// killGroup SIGTERMs the group, waits up to ~250ms for it to drain, then
// SIGKILLs whatever's left. Both signals are best-effort — ESRCH means
// the group is already empty, which is the success case.
func killGroup(pgid int) {
    _ = syscall.Kill(-pgid, syscall.SIGTERM)
    deadline := time.Now().Add(250 * time.Millisecond)
    for time.Now().Before(deadline) {
        if err := syscall.Kill(-pgid, 0); err != nil {
            return
        }
        time.Sleep(20 * time.Millisecond)
    }
    _ = syscall.Kill(-pgid, syscall.SIGKILL)
}
```

Notes:
- `Setpgid: true` with `Pgid: 0` makes the child its own pgroup leader, so `cmd.Process.Pid == pgid`.
- `Foreground: true` requires `Setpgid` and a `Ctty` fd — without it an interactive subshell would be backgrounded by the kernel and SIGTTIN itself the moment it tries to read stdin.
- A nil receive on `s.Signals` blocks forever; this is the standard Go idiom for "this channel is optional, ignore if absent."
- `golang.org/x/term` is already in `go.mod` as an indirect dep (v0.40.0); using it here promotes it to direct.
- `syscall.Kill(-pgid, sig)` signals every process in the group. Negative pid is the documented Unix interface; works identically on Linux and macOS.
- `syscall.Kill(-pgid, 0)` returns `ESRCH` when the group is empty; that's our drain-detection signal.

### Why a struct over a package function

| Aspect | Struct + embedded `*exec.Cmd` | Package function `supervise(cmd, sigCh)` |
| --- | --- | --- |
| Call-site shape | `sup := NewSupervisor(...); sup.Stdin = ...; sup.Run()` — mirrors `exec.Cmd` exactly | `cmd := exec.Command(...); cmd.Stdin = ...; supervise(cmd, sigCh)` — bolted-on |
| Discoverability | `Supervisor` shows up in `go doc`, IDEs autocomplete `sup.` | Function exists somewhere in the package; no obvious link to `exec.Cmd` |
| Safety from bypass | `sup.Run()` shadows `Cmd.Run()`; bypass requires `sup.Cmd.Run()` (deliberate) | Easy to call `cmd.Run()` directly and lose supervision |
| Std-lib idiom match | `bufio.Reader`/`bufio.Writer` pattern | Less common in Go's stdlib |
| Future extension | Easy to add `Pgid()`, `Start()`, etc. without churning callers | Each extension is a new function with its own signature |
| Test footprint | Identical | Identical |

### Modify `cmd/endless-sandbox/enter.go`

Replace `exec.Command(...).Run()` with a `Supervisor`. Add a signal channel for SIGINT/SIGTERM/SIGHUP. Ignore SIGTTOU/SIGTTIN at parent so post-cleanup writes don't stop the parent if it lost foreground.

Shape:
```go
signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)

sup := NewSupervisor(shell, "-i")
sup.Env = append(os.Environ(), sb.Env()...)
sup.Stdin = os.Stdin
sup.Stdout = os.Stdout
sup.Stderr = os.Stderr

sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
sup.Signals = sigCh

if err := sup.Run(); err != nil {
    var exitErr *exec.ExitError
    if !errors.As(err, &exitErr) {
        fmt.Fprintf(os.Stderr, "endless-sandbox enter: %v\n", err)
        os.Exit(1)
    }
}
if sup.ProcessState != nil {
    os.Exit(sup.ProcessState.ExitCode())
}
```

Note: `enter` doesn't auto-destroy on exit (it's keep-mode by design when invoked without an existing sandbox), so no `Destroy()` integration is needed here. The pgroup-kill is the entirety of the new behavior on the enter path.

### Modify `cmd/endless-sandbox/run.go`

Replace `exec.Command(...).Run()` and the existing inline signal goroutine with a `Supervisor`. Existing `cleanup()` (sandbox dir removal) still runs as `defer`; supervisor handles the process tree before we reach the cleanup call.

Shape:
```go
sup := NewSupervisor(rest[0], rest[1:]...)
sup.Env = append(os.Environ(), sb.Env()...)
sup.Stdin = os.Stdin
sup.Stdout = os.Stdout
sup.Stderr = os.Stderr

sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
sup.Signals = sigCh

runErr := sup.Run()
cleanup() // explicit pre-exit; deferred copy is no-op via the `cleaned` guard

if runErr != nil {
    var exitErr *exec.ExitError
    if !errors.As(runErr, &exitErr) {
        fmt.Fprintf(os.Stderr, "endless-sandbox run: %v\n", runErr)
        os.Exit(1)
    }
}
if sup.ProcessState != nil {
    os.Exit(sup.ProcessState.ExitCode())
}
```

Drop `asExitErr` — call sites use `errors.As` directly now.

### Extend `cmd/endless-sandbox/sandbox_test.go`

Add three tests against `Supervisor` directly. Tests use `sh` so they don't depend on building the binary.

```go
// TestSupervisorSetsProcessGroup asserts the child becomes its own pgroup leader.
func TestSupervisorSetsProcessGroup(t *testing.T) {
    sup := NewSupervisor("sh", "-c", "echo $$; sleep 30")
    var stdout bytes.Buffer
    sup.Stdout = &stdout
    sigCh := make(chan os.Signal, 1)
    sup.Signals = sigCh

    done := make(chan struct{})
    go func() {
        _ = sup.Run()
        close(done)
    }()

    // Wait for child to print its PID
    waitFor(t, func() bool { return strings.TrimSpace(stdout.String()) != "" })
    childPID := mustAtoi(t, strings.TrimSpace(stdout.String()))

    pgid, err := syscall.Getpgid(childPID)
    if err != nil {
        t.Fatalf("Getpgid: %v", err)
    }
    if pgid != childPID {
        t.Fatalf("expected pgid==%d (own group), got %d", childPID, pgid)
    }
    if pgid == os.Getpid() {
        t.Fatalf("child shares pgroup with test parent (%d)", os.Getpid())
    }

    sigCh <- syscall.SIGTERM
    <-done
}

// TestSupervisorKillsDescendants asserts a backgrounded grandchild is reaped
// after the foreground child exits.
func TestSupervisorKillsDescendants(t *testing.T) {
    sup := NewSupervisor("sh", "-c", "sleep 60 & echo $!; exit 0")
    var stdout bytes.Buffer
    sup.Stdout = &stdout

    _ = sup.Run()

    grandchild := mustAtoi(t, strings.TrimSpace(stdout.String()))
    if err := syscall.Kill(grandchild, 0); err == nil {
        t.Fatalf("grandchild PID %d still alive after Run returned", grandchild)
    } else if !errors.Is(err, syscall.ESRCH) {
        t.Fatalf("kill(grandchild,0): want ESRCH, got %v", err)
    }
}

// TestSupervisorSignalForwarding asserts a signal on Signals terminates the
// whole group within a bounded wait.
func TestSupervisorSignalForwarding(t *testing.T) {
    sup := NewSupervisor("sh", "-c", "sleep 60 & echo $!; sleep 60")
    var stdout bytes.Buffer
    sup.Stdout = &stdout
    sigCh := make(chan os.Signal, 1)
    sup.Signals = sigCh

    done := make(chan struct{})
    go func() {
        _ = sup.Run()
        close(done)
    }()

    waitFor(t, func() bool { return strings.TrimSpace(stdout.String()) != "" })
    grandchild := mustAtoi(t, strings.TrimSpace(stdout.String()))

    sigCh <- syscall.SIGTERM
    select {
    case <-done:
    case <-time.After(2 * time.Second):
        t.Fatal("Supervisor.Run did not return within 2s of signal")
    }

    if err := syscall.Kill(grandchild, 0); !errors.Is(err, syscall.ESRCH) {
        t.Fatalf("grandchild not reaped: kill(%d,0)=%v", grandchild, err)
    }
}

// waitFor polls cond every 20ms up to 2s, fatal if it never becomes true.
func waitFor(t *testing.T, cond func() bool) {
    t.Helper()
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if cond() {
            return
        }
        time.Sleep(20 * time.Millisecond)
    }
    t.Fatal("condition not met within 2s")
}

func mustAtoi(t *testing.T, s string) int {
    t.Helper()
    n, err := strconv.Atoi(s)
    if err != nil {
        t.Fatalf("atoi(%q): %v", s, err)
    }
    return n
}
```

## Files modified / added

| File | Change |
| ---- | ------ |
| `cmd/endless-sandbox/supervisor.go` | NEW — `Supervisor` type + `NewSupervisor` + `killGroup` |
| `cmd/endless-sandbox/enter.go` | Replace `exec.Command(...).Run()` with `Supervisor`; add signal channel; `signal.Ignore(SIGTTOU, SIGTTIN)` |
| `cmd/endless-sandbox/run.go` | Replace `exec.Command(...).Run()` + inline signal goroutine with `Supervisor`; drop `asExitErr` helper |
| `cmd/endless-sandbox/sandbox_test.go` | Add 3 tests + 2 helpers |
| `go.mod` | Promote `golang.org/x/term` from indirect to direct (auto via `go mod tidy`) |

## Out of scope (file as subsequent tasks)

- **Block `destroy` when sandbox has live writers.** This was the original symptom (destroy succeeded yet files reappeared). The pgroup hardening prevents the leak from happening in the first place, but a defense-in-depth check in `destroy.go` — list PIDs with the sandbox dir as their cwd, refuse with a clear error — would catch any future leak path. File as `endless task add "Block sandbox destroy when live writers detected" --parent E-1074` if pursuing. Add this task as phase=next.
- **Linux-specific verification.** The plan tests pass on macOS (the dev platform). Process-group semantics are POSIX, so Linux behavior should match, but the user runs no Linux CI on this code today. Not blocking.  Add this task as phase=later.

## Verification

1. **Worktree setup** (uses E-1074 branch as base, not main):
   ```sh
   git -C /Users/mikeschinkel/Projects/endless worktree add -b task/1114-sandbox-process-group \
       /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1114 task/1074-sandbox-go-binary
   cd /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1114
   just go-work-init
   endless task start E-1114
   ```

2. **Build & unit tests:**
   ```sh
   just build
   just test-go
   ```
   `just test-go` should run the three new tests and they should pass.

3. **Manual smoke (paste output into commit message):**
   ```sh
   SMOKE=$(mktemp -d) && export XDG_CACHE_HOME=$SMOKE
   ./bin/endless-sandbox run -- sh -c 'sleep 60 & echo started: $!; wait' &
   RUN_PID=$!
   sleep 1
   SLEEP_PID=$(ps -ef | grep '[s]leep 60' | awk '{print $2}')
   kill -TERM $RUN_PID
   sleep 1
   ps -p $SLEEP_PID > /dev/null && echo LEAK || echo CLEAN  # expect CLEAN
   rm -rf $SMOKE
   ```

4. **Regression check on the original incident shape:**
   ```sh
   ./bin/endless-sandbox enter probe-1114 &
   ENTER_PID=$!
   sleep 1
   # In another terminal or via expect: spawn a backgrounded writer inside the
   # subshell, then exit the shell. Confirm the writer is gone before destroy.
   kill -TERM $ENTER_PID
   sleep 1
   ./bin/endless-sandbox destroy probe-1114
   ./bin/endless-sandbox list | grep probe-1114 && echo LEAK || echo CLEAN
   ```

5. **Set status to `verify`** when build + tests + smoke all pass:
   ```sh
   endless task update E-1114 --status verify
   ```

## Switch back

```sh
tmux select-window -t '%0'
```
