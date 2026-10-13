// Command visprobe is a visibility-gated live view that records each render
// as a line in a file, so TestVisibilityEndToEnd can tell which of several
// monitors is doing work (E-2229). Run as `visprobe monitor-wake` it is the
// hook command too, exactly as endless-go is.
//
// It lives under testdata so `go build ./...` never builds it.
//
//	visprobe <render-log> <safety-net>
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitorvis"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == monitorvis.WakeVerb {
		monitorvis.RunWake(os.Args[2:])
		return
	}
	logPath := os.Args[1]
	safetyNet, err := time.ParseDuration(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var vis liveview.Visibility
	if gate := monitorvis.NewGate(os.Getenv("TMUX_PANE")); gate != nil {
		vis = gate
	}
	n := 0
	liveview.Loop(liveview.LoopConfig{
		Render: func(w io.Writer, cols int, color bool) (int, error) {
			n++
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return 0, err
			}
			defer f.Close()
			fmt.Fprintln(f, n)
			fmt.Fprintln(w, "render", n)
			return 1, nil
		},
		FallbackCols: 80,
		Tick:         50 * time.Millisecond,
		Visibility:   vis,
		SafetyNet:    safetyNet,
	})
}
