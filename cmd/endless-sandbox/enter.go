package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func enterCmd(args []string) {
	fs := flag.NewFlagSet("enter", flag.ExitOnError)
	clone := fs.Bool("clone", false, "Deep-clone live state (deferred; see E-1087)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "endless-sandbox enter: expected exactly one positional arg <name>")
		os.Exit(1)
	}
	name := rest[0]

	if *clone {
		fmt.Fprintln(os.Stderr, "endless-sandbox: warning: --clone deep-copy not yet implemented (see E-1087); sandbox starts empty")
	}

	sb, err := Load(name)
	if err != nil {
		// Not loaded → provision as keep-mode (named, persistent).
		sb, err = Provision(name, modeKeep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-sandbox enter: %v\n", err)
			os.Exit(1)
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
}
