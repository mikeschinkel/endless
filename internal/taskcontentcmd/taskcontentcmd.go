// Package taskcontentcmd implements the `endless-go task-content` subcommand:
// the read-only seam through which the Python CLI consumes internal/taskcontent
// (E-1531), the way taskstatuscmd serves internal/taskstatus.
//
// Touches no database — it is a pure lookup.
//
// Exit codes:
//
//	0  the verb succeeded
//	2  usage error — unknown verb or wrong arity
package taskcontentcmd

import (
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Run dispatches the `task-content` subcommand's inner verbs.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		usage(os.Stdout)
	case "names":
		// One kind per line, in display order, tab-separated: slug, label.
		// Line-oriented for the reason taskstatuscmd gives — the client parses
		// no structure it would otherwise have to know about.
		if len(rest) != 0 {
			fmt.Fprintf(os.Stderr, "endless-go task-content names: expected 0 argument(s), got %d\n", len(rest))
			usage(os.Stderr)
			os.Exit(2)
		}
		for _, n := range taskcontent.All() {
			fmt.Printf("%s\t%s\n", n.Slug(), n)
		}
	default:
		fmt.Fprintf(os.Stderr, "endless-go task-content: unknown command %q\n", verb)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go task-content <verb>")
	fmt.Fprintln(w, "Verbs:")
	fmt.Fprintln(w, "  names    every content kind in display order, TSV: slug label")
}
