package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func destroyCmd(args []string) {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	force := fs.Bool("force", false, "Destroy even if processes still hold files in the sandbox")
	ifExists := fs.Bool("if-exists", false, "Exit 0 silently if the sandbox does not exist (idempotent script use)")
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
			if *ifExists {
				return
			}
			fmt.Fprintf(os.Stderr, "endless-sandbox destroy: sandbox %q does not exist\n", name)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}

	// Defense in depth against the original E-1114 incident: even with
	// pgroup isolation, an external process (an editor with a sandbox file
	// open, a daemon descended from a different shell) could still hold
	// files. Refuse and name the offender(s) unless --force.
	if !*force {
		writers := findLiveWriters(dir)
		if len(writers) > 0 {
			fmt.Fprintf(os.Stderr,
				"endless-sandbox destroy: refusing to destroy %q — %d process(es) still have files open in it:\n",
				name, len(writers))
			for _, w := range writers {
				fmt.Fprintf(os.Stderr, "    PID %d: %s\n", w.PID, w.Name)
			}
			fmt.Fprintln(os.Stderr,
				"    Exit them (or 'kill <PID>') and retry, or pass --force to destroy anyway.")
			os.Exit(1)
		}
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
