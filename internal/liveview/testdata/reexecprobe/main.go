// Command reexecprobe is a live view with nothing to show but which build it is.
// TestLoopReexecsIntoReplacedBinary builds it several times with different
// -X main.build values and swaps them under a running loop (E-2193).
//
// It lives under testdata so `go build ./...` never builds it.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/liveview"
)

var (
	// build names this binary, and is set at link time.
	build = "unset"
	// probeFails, when set at link time, makes `--help` fail — a replacement
	// that cannot start.
	probeFails = ""
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		if probeFails != "" {
			fmt.Fprintln(os.Stderr, "reexecprobe: refusing to start")
			os.Exit(3)
		}
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: reexecprobe <marker-file> [extra...]")
		os.Exit(2)
	}
	marker := os.Args[1]
	// Strip everything after the marker, as main strips its --db flags before
	// dispatch. A re-exec that ran os.Args instead of the original command line
	// would come back without them, and the marker would say so.
	extra := len(os.Args) > 2
	os.Args = os.Args[:2]

	liveview.Loop(liveview.LoopConfig{
		Render: func(w io.Writer, cols int, color bool) (int, error) {
			fmt.Fprintf(w, "build %s pid %d\n", build, os.Getpid())
			return 1, os.WriteFile(marker, fmt.Appendf(nil, "%s %d extra=%v\n", build, os.Getpid(), extra), 0o644)
		},
		FallbackCols: 80,
		Tick:         50 * time.Millisecond,
	})
}
