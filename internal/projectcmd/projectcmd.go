// Package projectcmd implements `endless-go project`: the Go owner of the
// projects registry's "not a project" state (E-2251). The Python CLI's
// register, discover, reconcile, unregister, purge and ignore commands all come
// through here, so there is one answer to "is this directory ignored?" and one
// writer of the ignored status.
//
// These writes go straight to the database rather than through `event emit`:
// the projects table is a machine-local registry of paths, never projected from
// a ledger, and a directory that is not a project has no ledger to record in.
package projectcmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// Run dispatches `endless-go project <verb>`.
func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go project: no subcommand given",
			"Re-run with a subcommand from the printed list",
		).Command("project").Text(usageText()).Exit(2)
	}
	verb, rest := args[0], args[1:]
	var err error
	switch verb {
	case "resolve":
		err = runResolve(rest)
	case "ignore":
		err = runIgnore(rest)
	case "activate":
		err = runActivate(rest)
	case "clear":
		err = runClear(rest)
	case "list-ignored":
		err = runListIgnored(rest)
	case "-h", "--help", "help":
		refusal.Info(usageText()).Print()
		return
	default:
		refusal.ReportIf(
			fmt.Sprintf("endless-go project: unknown subcommand %q", verb),
			"an `endless` verb relayed this rather than you typing endless-go yourself",
			"retry with a subcommand from the printed list",
			"the installed endless-go is older than the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("project").Detail(usageText()).Exit(2)
	}
	if err != nil {
		refusal.From(err).Command("project " + verb).Exit(1)
	}
}

func usageText() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Usage: endless-go project <subcommand> [args...]")
	fmt.Fprintln(&b, "Subcommands:")
	fmt.Fprintln(&b, "  resolve <dir>...   JSON verdict per directory: project, ignored or none")
	fmt.Fprintln(&b, "  ignore <dir>       record <dir> (and its subtree) as not a project")
	fmt.Fprintln(&b, "  activate <dir>     re-activate <dir>'s existing ignored row")
	fmt.Fprintln(&b, "  clear <dir>        delete <dir>'s ignored row (refused if it holds tasks)")
	fmt.Fprintln(&b, "  list-ignored       JSON list of ignored rows")
	return b.String()
}

// oneDir checks a verb got exactly one directory and resolves it.
func oneDir(verb string, args []string) (string, error) {
	if len(args) != 1 {
		return "", refusal.NoReport(
			fmt.Sprintf("endless-go project %s: expected exactly one directory, got %d", verb, len(args)),
			"Pass one directory and retry")
	}
	return monitor.ResolvedProjectPath(args[0])
}

func runResolve(args []string) error {
	if len(args) == 0 {
		return refusal.NoReport("endless-go project resolve: no directory given",
			"Pass one or more directories and retry")
	}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	verdicts := make([]monitor.DirVerdict, 0, len(args))
	for _, a := range args {
		dir, err := monitor.ResolvedProjectPath(a)
		if err != nil {
			return err
		}
		v, err := monitor.ResolveDirectory(db, dir)
		if err != nil {
			return err
		}
		verdicts = append(verdicts, v)
	}
	return dbprovenance.Encode(os.Stdout, verdicts)
}

type writeResult struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

func runIgnore(args []string) error {
	dir, err := oneDir("ignore", args)
	if err != nil {
		return err
	}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	stored, existed, err := monitor.SetDirectoryIgnored(db, dir)
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, writeResult{Path: stored, Existed: existed})
}

func runActivate(args []string) error {
	dir, err := oneDir("activate", args)
	if err != nil {
		return err
	}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	stored, err := monitor.SetDirectoryActive(db, dir)
	if errors.Is(err, monitor.ErrNoProject) {
		return refusal.NoReport(err.Error(),
			"Register the directory with `endless project register` instead")
	}
	if err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, writeResult{Path: stored, Existed: true})
}

func runClear(args []string) error {
	dir, err := oneDir("clear", args)
	if err != nil {
		return err
	}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	stored, err := monitor.ClearIgnoredDirectory(db, dir)
	switch {
	case errors.Is(err, monitor.ErrIgnoredHasHistory):
		return refusal.NoReport(err.Error(),
			"Re-register it with `endless project register` to make it a project again")
	case errors.Is(err, monitor.ErrNoProject):
		return refusal.NoReport(err.Error(), "Nothing to clear — check `endless project ignore` for the list")
	case errors.Is(err, monitor.ErrNotIgnored):
		return refusal.NoReport(err.Error(), "Use `endless project unregister` to ignore a registered project")
	case err != nil:
		return err
	}
	return dbprovenance.Encode(os.Stdout, writeResult{Path: stored, Existed: true})
}

type ignoredRow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Tasks int    `json:"tasks"`
}

func runListIgnored(args []string) error {
	if len(args) != 0 {
		return refusal.NoReport("endless-go project list-ignored: takes no arguments",
			"Drop the arguments and retry")
	}
	db, err := monitor.DB()
	if err != nil {
		return err
	}
	rows, err := db.Query(
		`SELECT p.id, p.name, p.path,
		        (SELECT count(*) FROM tasks t WHERE t.project_id = p.id)
		   FROM projects p WHERE p.status = ? ORDER BY p.path`,
		monitor.ProjectStatusIgnored,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []ignoredRow{}
	for rows.Next() {
		var r ignoredRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Path, &r.Tasks); err != nil {
			return err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return dbprovenance.Encode(os.Stdout, out)
}
