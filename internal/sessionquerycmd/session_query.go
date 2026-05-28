// Package sessionquerycmd implements the `endless-go session-query`
// subcommand: an internal helper that exposes monitor.* read operations
// as JSON for the Python CLI. It exists so the Python side can avoid
// extending the legacy `db.query` pattern (E-894). Subcommands are
// intentionally narrow — one verb per Python need.
package sessionquerycmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
	_ "modernc.org/sqlite"
)

func Run(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "list-live":
		if err := runListLive(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "task-text":
		if err := runTaskText(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: endless-go session-query <subcommand>")
	fmt.Fprintln(os.Stderr, "subcommands:")
	fmt.Fprintln(os.Stderr, "  list-live --project-root <path>   JSON array of live sessions for the project")
	fmt.Fprintln(os.Stderr, "  task-text --id <task-id>          raw tasks.text for the task (empty if none)")
}

// runTaskText prints the raw tasks.text for a task id to stdout, so the Python
// side can materialize a plan file at claim time without a Python DB read
// (E-894 / E-1445). Output is the raw text (not JSON) — it is written verbatim
// to <worktree>/.endless/plans/E-NNN.md. Empty output means "no plan".
func runTaskText(args []string) error {
	fs := flag.NewFlagSet("task-text", flag.ContinueOnError)
	id := fs.Int64("id", 0, "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	text, err := monitor.TaskText(*id)
	if err != nil {
		return fmt.Errorf("read task text for E-%d: %w", *id, err)
	}
	_, err = os.Stdout.WriteString(text)
	return err
}

func runListLive(args []string) error {
	fs := flag.NewFlagSet("list-live", flag.ContinueOnError)
	projectRoot := fs.String("project-root", "", "absolute path of the project root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectRoot == "" {
		return fmt.Errorf("--project-root is required")
	}

	projectID, _, err := monitor.ProjectIDForPath(*projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project for %s: %w", *projectRoot, err)
	}
	if projectID == 0 {
		// Unregistered cwd: empty result rather than error so the Python
		// caller can treat "no project" and "no live sessions" uniformly.
		return json.NewEncoder(os.Stdout).Encode([]monitor.LiveSession{})
	}

	sessions, err := monitor.ListLiveSessions(projectID)
	if err != nil {
		return fmt.Errorf("list live sessions: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(sessions)
}
