package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), sb.Env()...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "endless-sandbox enter: %v\n", err)
		os.Exit(1)
	}
}
