// Package markdowncmd implements the `endless-go markdown` subcommand: a pure
// stdin→stdout markdown-to-ANSI transform used by `endless task show` to
// colorize its multiline markdown fields for terminal display (E-1746).
//
// It touches no database — it is a filter, registered in the dispatcher
// alongside the other endless-go subcommands.
package markdowncmd

import (
	"fmt"
	"io"
	"os"

	"github.com/mikeschinkel/endless/internal/mdterm"
)

// Run dispatches the `markdown` subcommand's inner verbs.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "render":
		if err := runRender(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go markdown: unknown command %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

// runRender reads markdown from in and writes colorized ANSI to out.
func runRender(in io.Reader, out io.Writer) error {
	src, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("markdown render: read stdin: %w", err)
	}
	if _, err := io.WriteString(out, mdterm.RenderString(string(src))); err != nil {
		return fmt.Errorf("markdown render: write stdout: %w", err)
	}
	return nil
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go markdown <command>")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  render   read markdown on stdin, write colorized ANSI to stdout")
}
