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

func enterCmd(args []string) {
	fs := refusal.NewFlags("enter")
	clone := fs.Bool("clone", false, "Deep-clone live state (deferred; see E-1087)")
	parseVerbFlags(fs, "enter", args)
	rest := fs.Args()
	if len(rest) != 1 {
		refusal.NoReport(
			"endless-go sandbox enter: expected exactly one positional arg <name>",
			"Pass exactly one sandbox name and retry",
		).Command("sandbox enter").Exit(1)
	}
	name := rest[0]

	if *clone {
		refusal.NoReport(
			"endless-go sandbox enter: warning: --clone deep-copy not yet implemented (see E-1087); sandbox starts empty",
			"Nothing to do: the subshell starts in an empty sandbox; drop --clone next time",
		).Command("sandbox enter").Print()
	}

	sb, err := Load(name)
	if err != nil {
		// Not loaded → provision as keep-mode (named, persistent).
		sb, err = Provision(name, modeKeep)
		if err != nil {
			relayed("enter", err).Exit(1)
		}
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	// Ignore SIGTTOU/SIGTTIN on the parent so it can write to the TTY
	// during cleanup after the foreground subshell exits.
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)

	// Auto-inject 'eval $(endless shell-init)' so esu/esp/esf are defined
	// in the sandbox subshell with zero user setup. (E-1182.)
	inject := buildShellInjection(shell)
	defer inject.Clean()

	// -i forces interactive mode. Without it, bash/zsh launched via exec
	// can decide they are non-interactive and exit immediately, defeating
	// the subshell semantics from E-1072.
	shellArgs := append(inject.Args, "-i")
	sup := NewSupervisor(shell, shellArgs...)
	sup.Env = append(os.Environ(), sb.Env()...)
	sup.Env = append(sup.Env, inject.Env...)
	sup.Stdin = os.Stdin
	sup.Stdout = os.Stdout
	// The subshell is the user's own shell: everything it writes is its
	// output, not Endless's.
	sup.Stderr = refusal.Passthrough()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	sup.Signals = sigCh

	if err := sup.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			// The subshell never started. $SHELL, or the controlling terminal
			// this verb claims for it, is the user's environment — and `enter`
			// is an interactive verb an agent has no use for anyway.
			refusal.Report(
				fmt.Sprintf("endless-go sandbox enter: %v", err),
				"how to fix the $SHELL or controlling-terminal environment the subshell could not start in",
			).Command("sandbox enter").Exit(1)
		}
	}
	if sup.ProcessState != nil {
		os.Exit(sup.ProcessState.ExitCode())
	}
}
