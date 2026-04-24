package main

import (
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: endless-hook <command> [args...]")
		fmt.Fprintln(os.Stderr, "Commands: prompt, claude, codex")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "prompt":
		err = runPrompt(os.Args[2:])
	case "claude":
		err = runClaude(os.Args[2:])
	case "codex":
		err = runCodex(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}

	if err != nil {
		log.Printf("%s: %v", os.Args[1], err)
		os.Exit(1)
	}
}
