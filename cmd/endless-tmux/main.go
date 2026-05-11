// Command endless-tmux drives the second tmux status line (E-1236).
//
// Subcommands:
//
//	apply        Ephemerally configure the running tmux server: enable a
//	             second status line, wire status-format[1] to the printer,
//	             install hotkey and right-click popup menus. No file I/O;
//	             survives until tmux server restart.
//	status-line  Print one styled line to stdout for tmux's status-format[1].
//	             Invoked by tmux on each status-interval refresh.
//
// Tmux config invokes the status-line subcommand directly so the binary's
// fast startup (~5ms) stays well under the <50ms latency budget. The
// `endless tmux <verb>` Python wrapper is provided for ergonomics; tmux
// itself should never go through it.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "apply":
		runApply(os.Args[2:])
	case "status-line":
		runStatusLine(os.Args[2:])
	case "active-id":
		runActiveID(os.Args[2:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-tmux: unknown command %q\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, "Usage: endless-tmux <command> [flags]\n")
	fmt.Fprintf(w, "Commands:\n")
	fmt.Fprintf(w, "  apply        Configure the running tmux server (ephemeral)\n")
	fmt.Fprintf(w, "  status-line  Print one styled line for status-format[1]\n")
}
