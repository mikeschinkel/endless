package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func initCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	mode := fs.String("mode", "empty", "Initial state: empty | seed | clone")
	force := fs.Bool("force", false, "Recreate the sandbox if it already exists")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "endless-sandbox init: expected exactly one positional arg <name>")
		os.Exit(1)
	}
	name := rest[0]
	if err := validateName(name); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox init: %v\n", err)
		os.Exit(1)
	}

	switch *mode {
	case "empty":
		// supported
	case "seed":
		fmt.Fprintln(os.Stderr, "endless-sandbox init: --mode seed not yet implemented; use --mode empty")
		os.Exit(1)
	case "clone":
		fmt.Fprintln(os.Stderr, "endless-sandbox init: --mode clone not yet implemented (see E-1087); use --mode empty")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "endless-sandbox init: unknown --mode %q (want: empty | seed | clone)\n", *mode)
		os.Exit(1)
	}

	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err == nil {
		if *force {
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "endless-sandbox init: removing existing %s: %v\n", dir, err)
				os.Exit(1)
			}
		} else {
			// Idempotent: existing sandbox with the same name is treated as a no-op.
			fmt.Println(dir)
			return
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "endless-sandbox init: %v\n", err)
		os.Exit(1)
	}

	sb, err := Provision(name, modePersistent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox init: %v\n", err)
		os.Exit(1)
	}
	// Close the root handle now; init does not keep the sandbox open.
	if sb.root != nil {
		sb.root.Close()
		sb.root = nil
	}
	fmt.Println(sb.Dir)
}
