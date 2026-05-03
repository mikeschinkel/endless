package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func destroyCmd(args []string) {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "endless-sandbox destroy: expected exactly one positional arg <name>")
		os.Exit(1)
	}
	name := rest[0]
	if err := validateName(name); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}

	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "endless-sandbox destroy: sandbox %q does not exist\n", name)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}

	// Remove unconditionally — a missing or corrupt meta file is exactly
	// the case where destroy is most needed. Sanity is provided by the
	// validateName check + the path being rooted at sandboxesDir().
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Destroyed: %s\n", name)
}
