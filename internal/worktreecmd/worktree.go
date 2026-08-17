// Package worktreecmd implements the `endless-go worktree` subcommand: the
// worktree-lifecycle questions the Python CLI must ask but cannot answer
// itself.
//
// It exists because the answers are already implemented in Go, on the reaper's
// path, and `endless worktree drop` is Python (E-1947). Porting a check across
// the language boundary would mean two implementations of a predicate that
// gates directory removal — the failure class that has already cost weeks. So
// the Python side shells out here instead.
//
// DB context: this subcommand READS the sessions table, so it must see the
// database the caller resolved. It is deliberately NOT in cmd/endless-go's
// PinMainDB group — it takes the `--config-dir` that ConsumeDBContextFlag
// strips, exactly as `event` and `session-query` do, and the Python caller
// threads `config.go_db_context_args()`. Pinning main instead would answer a
// self-dev worktree's question from the real ledger and report no live session.
package worktreecmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Exit codes. `in-use` reports its verdict through the status code so a shell
// caller needs no parsing, with the in-use case distinct from a plain failure.
const (
	exitNotInUse     = 0
	exitUndetermined = 1
	exitUsage        = 2
	exitInUse        = 3
)

func Run(args []string) {
	if len(args) < 1 {
		usage(os.Stderr)
		os.Exit(exitUsage)
	}
	switch args[0] {
	case "in-use":
		os.Exit(runInUse(args[1:]))
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go worktree: unknown verb %q\n", args[0])
		usage(os.Stderr)
		os.Exit(exitUsage)
	}
}

// inUseJSON is the wire shape of the verdict under --json. Reason is the
// human-readable string the Python refusal prints verbatim, so the two sides
// never word the same guard differently.
type inUseJSON struct {
	Dir    string `json:"dir"`
	TaskID int64  `json:"task_id"`
	InUse  bool   `json:"in_use"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

// runInUse answers monitor.WorktreeInUse for one directory.
//
//	endless-go worktree in-use --dir <path> [--task <id>] [--json]
//
// --task is optional: 0 (absent) means the directory has no owning task, so
// only the live-process probe applies. Every endless-managed e-NNN worktree
// has one and the caller passes it.
func runInUse(args []string) int {
	fs := flag.NewFlagSet("in-use", flag.ContinueOnError)
	dir := fs.String("dir", "", "worktree directory to inspect (required)")
	taskID := fs.Int64("task", 0, "owning task id; 0 skips the session probe")
	asJSON := fs.Bool("json", false, "emit the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "endless-go worktree in-use: --dir is required")
		return exitUsage
	}

	db, err := monitor.DB()
	if err != nil {
		// Fail closed, as WorktreeInUse itself does: a caller about to remove a
		// directory must read "could not tell" as "do not remove".
		return report(*asJSON, inUseJSON{
			Dir: *dir, TaskID: *taskID, InUse: true,
			Reason: string(monitor.ReasonUndetermined), Error: err.Error(),
		})
	}

	inUse, reason, err := monitor.WorktreeInUse(db, *dir, *taskID)
	out := inUseJSON{
		Dir: *dir, TaskID: *taskID, InUse: inUse, Reason: string(reason),
	}
	if err != nil {
		out.Error = err.Error()
	}
	return report(*asJSON, out)
}

// report writes the verdict and maps it to the exit code. An error is always
// accompanied by InUse=true (WorktreeInUse fails closed), but the exit code
// still distinguishes the two so a caller can tell "in use" from "broken".
func report(asJSON bool, out inUseJSON) int {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else if out.Reason != "" {
		fmt.Fprintln(os.Stdout, out.Reason)
	}
	if out.Error != "" {
		fmt.Fprintf(os.Stderr, "endless-go worktree in-use: %s\n", out.Error)
		return exitUndetermined
	}
	if out.InUse {
		return exitInUse
	}
	return exitNotInUse
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go worktree <verb> [flags]")
	fmt.Fprintln(w, "Verbs:")
	fmt.Fprintln(w, "  in-use --dir <path> [--task <id>] [--json]")
	fmt.Fprintln(w, "         exit 0 not in use, 3 in use (reason on stdout), 1 undetermined")
}
