package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"
)

// Supervisor wraps *exec.Cmd to run a child process in its own process group,
// forward signals to the whole group, and guarantee all descendants are dead
// before Run returns. Configure fields (Env, Stdin, Stdout, Stderr) on the
// embedded Cmd, optionally set Signals, then call Run. ProcessState carries
// exit info on return, just like exec.Cmd.
type Supervisor struct {
	*exec.Cmd

	// Signals, if non-nil, is read once: when a signal arrives while the
	// child is alive, it is forwarded to the whole process group. A nil
	// channel disables forwarding (Run waits only for natural exit).
	Signals <-chan os.Signal
}

// NewSupervisor mirrors exec.Command.
func NewSupervisor(name string, args ...string) *Supervisor {
	return &Supervisor{Cmd: exec.Command(name, args...)}
}

// Run shadows (*exec.Cmd).Run. Method resolution picks this when called on
// *Supervisor; bypassing it requires explicitly calling sup.Cmd.Run.
func (s *Supervisor) Run() error {
	if s.SysProcAttr == nil {
		s.SysProcAttr = &syscall.SysProcAttr{}
	}
	s.SysProcAttr.Setpgid = true
	// Foreground+Ctty against a non-TTY stdin fails at fork, so only claim
	// the controlling TTY when stdin actually is one.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		s.SysProcAttr.Foreground = true
		s.SysProcAttr.Ctty = int(os.Stdin.Fd())
	}

	if err := s.Cmd.Start(); err != nil {
		return err
	}
	pgid := s.Process.Pid

	waitCh := make(chan error, 1)
	go func() { waitCh <- s.Cmd.Wait() }()

	var err error
	select {
	case err = <-waitCh:
	case sig := <-s.Signals:
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

// killGroup SIGTERMs the process group, waits up to ~250ms for it to drain,
// then SIGKILLs whatever survives. ESRCH from kill(-pgid,0) means the group
// is empty — that's the success case.
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
