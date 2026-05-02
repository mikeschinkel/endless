package main

import (
	"flag"
	"fmt"
	"os"
)

func destroyCmd(args []string) {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "endless-sandbox destroy: expected exactly one positional arg <name>")
		os.Exit(1)
	}
	name := rest[0]

	sb, err := Load(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}
	if err := sb.Destroy(); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox destroy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Destroyed: %s\n", name)
}
