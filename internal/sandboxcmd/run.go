package sandboxcmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/mikeschinkel/endless/internal/refusal"
)

func runCmd(args []string) {
	fs := refusal.NewFlags("run")
	clone := fs.Bool("clone", false, "Deep-clone live state (deferred; see E-1087)")
	name := fs.String("name", "", "Sandbox name (random hex if empty)")
	keep := fs.Bool("keep", false, "Keep sandbox after exit (default: ephemeral, auto-destroyed)")
	parseVerbFlags(fs, "run", args)

	rest := fs.Args()
	if len(rest) == 0 {
		refusal.NoReport(
			"endless-go sandbox run: missing command after --",
			"Re-run as `endless-go sandbox run [flags] -- <cmd> [args]`",
		).Command("sandbox run").Exit(1)
	}

	if *clone {
		refusal.NoReport(
			"endless-go sandbox run: warning: --clone deep-copy not yet implemented (see E-1087); sandbox starts empty",
			"Nothing to do: the command runs in an empty sandbox; drop --clone next time",
		).Command("sandbox run").Print()
	}

	mode := modeEphemeral
	if *keep {
		mode = modeKeep
	}

	sb, err := Provision(*name, mode)
	if err != nil {
		relayed("run", err).Exit(1)
	}

	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		if mode == modeEphemeral {
			if err := sb.Destroy(); err != nil {
				refusal.NoReport(
					fmt.Sprintf("endless-go sandbox run: cleanup error: %v", err),
					"Nothing to do: the command ran, and `endless-go sandbox prune` sweeps the leftover sandbox",
				).Command("sandbox run").Print()
			}
		}
	}
	defer cleanup()

	sup := NewSupervisor(rest[0], rest[1:]...)
	sup.Env = append(os.Environ(), sb.Env()...)
	sup.Stdin = os.Stdin
	sup.Stdout = os.Stdout
	// The child is whatever the caller asked to run: its stderr is its own
	// output, already classified by whoever wrote it.
	sup.Stderr = refusal.Passthrough()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	sup.Signals = sigCh

	runErr := sup.Run()
	cleanup()

	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			refuseUnstartableCommand(runErr)
		}
	}
	if sup.ProcessState != nil {
		os.Exit(sup.ProcessState.ExitCode())
	}
}

// refuseUnstartableCommand classifies a child that never ran, resolving in code
// the branch the message itself cannot show: a command that is not on PATH, is
// missing, or is not executable is an argv the caller can fix and retry, and
// nothing about that needs the user. Anything else — a fork that failed, a
// process group or controlling TTY the kernel refused — is Endless unable to
// start a process at all.
func refuseUnstartableCommand(err error) {
	line := fmt.Sprintf("endless-go sandbox run: %v", err)
	if errors.Is(err, exec.ErrNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrPermission) {
		refusal.NoReport(line, "Correct the command name or its permissions and retry").
			Command("sandbox run").Exit(1)
	}
	refusal.Fault(err).Command("sandbox run").Text(line).Exit(1)
}
