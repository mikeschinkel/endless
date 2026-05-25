package main

import (
	"fmt"
	"os"
	"strconv"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/web"
)

func main() {
	// E-1429: accept the explicit DB context the Python CLI threads through
	// (--db main|worktree -> --config-dir <dir>).
	monitor.ConsumeDBContextFlag()

	port := 8484
	if len(os.Args) > 1 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil {
			port = p
		}
	}

	if err := web.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
