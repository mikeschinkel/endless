package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	clone := fs.Bool("clone", false, "Deep-clone live state (deferred; see E-1087)")
	name := fs.String("name", "", "Sandbox name (random hex if empty)")
	keep := fs.Bool("keep", false, "Keep sandbox after exit (default: ephemeral, auto-destroyed)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "endless-sandbox run: missing command after --")
		os.Exit(1)
	}

	if *clone {
		fmt.Fprintln(os.Stderr, "endless-sandbox: warning: --clone deep-copy not yet implemented (see E-1087); sandbox starts empty")
	}

	mode := modeEphemeral
	if *keep {
		mode = modeKeep
	}

	sb, err := Provision(*name, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox run: %v\n", err)
		os.Exit(1)
	}

	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		if mode == modeEphemeral {
			if err := sb.Destroy(); err != nil {
				fmt.Fprintf(os.Stderr, "endless-sandbox run: cleanup error: %v\n", err)
			}
		}
	}
	defer cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigCh
		cleanup()
		os.Exit(130)
	}()

	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Env = append(os.Environ(), sb.Env()...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if asExitErr(err, &exitErr) {
			cleanup()
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "endless-sandbox run: %v\n", err)
		cleanup()
		os.Exit(1)
	}
}

// asExitErr is a small helper around errors.As to keep the call site readable.
func asExitErr(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}
