package servecmd

import (
	"fmt"
	"os"
	"strconv"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/web"
)

func Run(args []string) {
	port := 8484
	if len(args) > 0 {
		if p, err := strconv.Atoi(args[0]); err == nil {
			port = p
		}
	}

	if err := web.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
