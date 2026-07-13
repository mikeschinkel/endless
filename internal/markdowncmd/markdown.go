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
	"strconv"
	"strings"

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
		width, err := parseWidth(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		err = runRender(os.Stdin, os.Stdout, width)
		if err != nil {
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

// parseWidth reads an optional `--width N` (or `--width=N`) flag, governing
// table column layout. Absent → 0, meaning the renderer's default width.
func parseWidth(args []string) (width int, err error) {
	var arg string
	var val string
	i := 0
	for i < len(args) {
		arg = args[i]
		val = ""
		switch {
		case arg == "--width":
			if i+1 >= len(args) {
				err = fmt.Errorf("markdown render: --width needs a value")
				goto end
			}
			val = args[i+1]
			i += 2
		case strings.HasPrefix(arg, "--width="):
			val = strings.TrimPrefix(arg, "--width=")
			i++
		default:
			err = fmt.Errorf("markdown render: unknown argument %q", arg)
			goto end
		}
		width, err = strconv.Atoi(val)
		if err != nil {
			err = fmt.Errorf("markdown render: invalid --width %q", val)
			goto end
		}
	}
end:
	return width, err
}

// runRender reads markdown from in and writes colorized ANSI to out, laying out
// tables for the given terminal width (0 → renderer default).
func runRender(in io.Reader, out io.Writer, width int) (err error) {
	var src []byte
	src, err = io.ReadAll(in)
	if err != nil {
		err = fmt.Errorf("markdown render: read stdin: %w", err)
		goto end
	}
	if width < 1 {
		width = mdterm.DefaultWidth
	}
	_, err = io.WriteString(out, mdterm.RenderStringWidth(string(src), width))
	if err != nil {
		err = fmt.Errorf("markdown render: write stdout: %w", err)
		goto end
	}
end:
	return err
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go markdown <command>")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  render [--width N]   read markdown on stdin, write colorized ANSI to stdout")
}
