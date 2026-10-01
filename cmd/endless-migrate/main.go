// Command endless-migrate applies Endless's schema changes and versioned
// migrations, and does nothing else.
//
// ED-1571's third thing. In self_dev a candidate binary and an installed binary
// coexist against one real ledger, and during the pre-land window neither of
// them may migrate it: the candidate must not (ED-1567 — an unlanded build
// mutating the production schema is the 2026-08-10 outage exactly), and the
// installed one does not carry the change being landed. `worktree land` used to
// square that circle by handing the apply step the WORKTREE's own endless-go,
// whose embedded schema matches the rows the land just wrote (E-1664) — but that
// binary is a candidate by definition, so the invariant and the prohibition
// point opposite ways.
//
// This executable is the way out. It is compiled from the landing branch, so it
// carries that branch's change set; it opens the database FILE directly, so it
// never reaches the application's connect; and it has no schema of its own to
// apply, no enum mirror to verify and no business data to read, so there is
// nothing about the database it can expect and therefore nothing the database
// can disappoint. That is the whole of its safety, and it is why it is allowed
// where a candidate endless-go is not.
//
// Usage:
//
//	endless-migrate [--db main | --db-dir <dir>] apply <change-file>
//	endless-migrate [--db main | --db-dir <dir>] up
//
// The flags are internal/dbcontext's, the same vocabulary endless-go takes
// (E-2157), minus one word. `--db sandbox` is REFUSED here rather than
// resolved: it names a database by where the caller is standing, and this
// executable resolves its target only from what the caller named. That is not
// a second dialect, it is the one value ED-1571 forbids, and refusing it by
// name is what tells a reader who learned `--db` from the guide why it does
// not apply — which "unknown command" could not.
//
// There are two subcommands, one per kind of schema step a landing branch can
// carry. `apply` applies one internal/schema/changes/ file. `up` brings the
// database to the newest goose migration this binary embeds, then reconciles
// the enum mirrors — internal/schema's own Migrate, the same call every other
// opener makes, run here without the application around it (E-2192). Without
// `up` a branch's goose migration reached the real ledger only when an
// installed binary next connected, so the candidate that records the landing
// could meet a database missing its own new column; E-2188 was that.
//
// The absence of every other subcommand is a property rather than an omission:
// no hook, no task, no event, no query, no tmux. internal/schemachange/
// executable_test.go asserts both halves — the surface this exposes, and that
// its build links nothing but the migration machinery. internal/schema is part
// of that machinery: it carries the embedded migration set and the seeds, and
// links no other Endless package.
//
// Scope is self_dev ONLY. Every other project has one installed binary and no
// land at all, so it carries and applies its own migrations under ED-1570
// through `endless-go event apply-change`; no separate executable exists there
// and nothing here assumes one does.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbcontext"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/schemachange"
)

// upResult is the JSON document `up` prints on stdout: schema.UpResult — the
// version the database was at, the version it is at now, and "migrated" or
// "current" — plus the file.
type upResult struct {
	schema.UpResult
	DB dt.Filepath `json:"db"`
}

// result is the JSON document an apply prints on stdout. It is
// schemachange.Result — the same shape `endless-go event apply-change` prints,
// so a caller parses one document whichever program it invoked — plus the
// database that was actually opened, because a migration tool that does not say
// which file it changed is asking to be trusted about the one thing worth
// checking.
type result struct {
	schemachange.Result
	DB dt.Filepath `json:"db"`
}

func main() {
	args, flags, err := dbcontext.ConsumeFlags(os.Args)
	if err != nil {
		errUsage(err.Error(), dbTargetRemedy)
	}

	if len(args) < 2 {
		// The usage page ALONE is what this has always written here, so it goes
		// to Text rather than Detail: Detail would stack a summary line above a
		// page no reader has ever seen one above. The summary exists for the
		// verdict only.
		refusal.NoReport("endless-migrate: no command given", commandRemedy).
			Command("migrate").Text(usageText()).Exit(2)
	}

	switch args[1] {
	case "-h", "--help", "help":
		// Resolved in the apply arm rather than here, so --help answers
		// without a database context. The reader who typed the wrong flag
		// needs the usage text most, and making them satisfy the flag in
		// order to read about the flag is a loop.
		fmt.Fprint(os.Stdout, usageText())
		return
	case "apply":
		dir, err := configDir(flags)
		if err != nil {
			errUsage(err.Error(), dbTargetRemedy)
		}
		res, err := runApply(args[2:], dir)
		if err != nil {
			emitError(res.Name, err)
		}
		emit(res)
		return
	case "up":
		dir, err := configDir(flags)
		if err != nil {
			errUsage(err.Error(), dbTargetRemedy)
		}
		res, err := runUp(args[2:], dir)
		if err != nil {
			emitError("", err)
		}
		emit(res)
		return
	}

	// The blank line between the message and the usage page is deliberate and
	// is part of the bytes a person reads, which is why the whole thing is
	// assembled into Text instead of being left to joinLines.
	//
	// NO-REPORT and not the version-skew CONDITIONAL endless-go's dispatcher
	// carries: this executable is built by the land that runs it, from the same
	// branch, and the only caller passes `apply` or `up` from code. A command
	// it does not know came from a hand invocation, and naming a real one is
	// the whole of the fix.
	refusal.NoReport(
		fmt.Sprintf("endless-migrate: unknown command %q", args[1]),
		commandRemedy,
	).Command("migrate").
		Text(fmt.Sprintf("endless-migrate: unknown command %q\n\n%s",
			args[1], usageText())).
		Exit(2)
}

// The remedies this file's usage refusals hand an agent. They are constants
// because each is shared by two or three call sites, and a remedy that drifts
// between them is a remedy a reader cannot trust.
const (
	commandRemedy = "Re-run naming a command: `apply <change-file>` or `up`"

	dbTargetRemedy = "Re-run with " + dbcontext.DBDirFlag + " <dir> to name " +
		"the config directory outright, or " + dbcontext.DBFlag + " main for " +
		"the project's database"
)

// errSandboxNotRoutable is the refusal for `--db sandbox`.
//
// It states the rule, then names the two flags that do work here — a refusal
// that names no remedy is worse than terse, and this one is met by exactly the
// reader who learned the vocabulary from the guide and had no way to know that
// this binary is different.
var errSandboxNotRoutable = errors.New(
	dbcontext.DBFlag + " sandbox names a database by where the caller is " +
		"standing, and this executable resolves its target only from what " +
		"the caller named (ED-1571) — so there is no \"which sandbox\" for " +
		"it to answer. Pass " + dbcontext.DBDirFlag + " <dir> to name a " +
		"sandbox outright, or " + dbcontext.DBFlag + " main for the " +
		"project's database.")

// configDir resolves the parsed flags to the directory holding the database to
// migrate. Every arm but the refusal is internal/dbcontext's own resolution, so
// "the main database" means here exactly what it means to endless-go.
//
// No flag keeps the historical default (XDG_CONFIG_HOME, else ~/.config)
// rather than refusing. A land always names its target, so the default is
// reached only by a hand invocation, and the relative-path check in runApply is
// what catches the case where that default resolves to nothing meaningful.
func configDir(flags dbcontext.Flags) (dir dt.DirPath, err error) {
	switch flags.Choice {
	case dbcontext.ChoiceSandbox:
		err = errSandboxNotRoutable
	case dbcontext.ChoiceMain:
		dir, err = dbcontext.MainConfigDir()
	case dbcontext.ChoiceDir:
		dir = flags.Dir
	default:
		dir = dbcontext.ConfigDir("")
	}
	return dir, err
}

// runApply applies exactly one change file to exactly one database.
//
// One file per invocation, matching what `worktree land` has always done: a
// change set can be several files, one can apply and the next fail, and the
// caller needs to know which — so it names them one at a time and reports each.
func runApply(args []string, dir dt.DirPath) (res result, err error) {
	var db *sql.DB
	var path dt.Filepath

	if len(args) != 1 {
		err = errUsage("apply requires exactly one <change-file>",
			"Pass exactly one change file per invocation and retry")
		goto end
	}

	path, err = dt.Filepath(args[0]).Abs()
	if err != nil {
		err = fmt.Errorf("resolving %s: %w", args[0], err)
		goto end
	}

	db, res.DB, err = openTarget(dir)
	if err != nil {
		goto end
	}
	defer closeDB(db)

	// A .go change's own log lines are its own stream, not a refusal of ours:
	// Apply hands this writer to the change, and what the change says about
	// its own work is already in whatever words its author chose.
	res.Result, err = schemachange.Apply(db, res.DB, path, refusal.Passthrough())

end:
	return res, err
}

// runUp brings exactly one database to the newest migration this binary embeds.
//
// It is schema.Up — goose Up, then the enum-mirror seeds — and nothing of its
// own, so the database `up` leaves is the one any other opener would have left.
// `endless db upgrade` (E-2020) runs the same call from endless-go. Against a
// database already at the latest version it changes no version and reports
// "current".
func runUp(args []string, dir dt.DirPath) (res upResult, err error) {
	var db *sql.DB

	if len(args) != 0 {
		err = errUsage("up takes no arguments",
			"Re-run `up` with no arguments")
		goto end
	}

	db, res.DB, err = openTarget(dir)
	if err != nil {
		goto end
	}
	defer closeDB(db)

	res.UpResult, err = schema.Up(context.Background(), db)

end:
	return res, err
}

// openTarget resolves dir to its database file and opens it through
// schema.OpenExisting, which refuses a relative path and a file that does not
// exist. Both subcommands come through here, so neither can migrate a database
// the other would have refused.
//
// OpenExisting opens the FILE: no schema application, no enum seed, no
// integrity gate, no worktree-build check, no sandbox routing. That is the line
// that separates this executable from `endless-go event apply-change`, which
// opens through internal/monitor and brings the application's whole connect
// with it.
func openTarget(dir dt.DirPath) (db *sql.DB, path dt.Filepath, err error) {
	path = dt.FilepathJoin(dir, dbcontext.DBFileName)
	db, err = schema.OpenExisting(path)
	return db, path, err
}

// closeDB reports a close failure on stderr rather than swallowing it. It cannot
// change the outcome — the work has already committed or rolled back — but a
// database that would not close is worth saying out loud once.
func closeDB(db *sql.DB) {
	err := db.Close()
	if err != nil {
		// Nothing normally reads this line: `worktree land` captures this
		// stream and throws it away on success, and on failure prefers the JSON
		// error document on stdout. It is classified anyway, and as a WARN
		// rather than a fault, because of WHEN it can happen — the migration has
		// already committed or already rolled back, so nothing is blocked and
		// there is nothing to retry. A fault here would tell an agent Endless
		// broke and the outcome is unknown, when in fact the outcome is whatever
		// the result document on stdout says it is.
		refusal.Warn(
			fmt.Sprintf("endless-migrate: closing the database: %v", err),
			"Continue; the migration had already committed or rolled back, and "+
				"the result document on stdout says which",
		).Command("migrate").Print()
	}
}

// errUsage is a failure in how this was invoked rather than in what it was asked
// to do. It exits 2, as the dispatch above does, so a caller can tell "you asked
// wrong" from "it did not work".
func errUsage(msg, remedy string) error {
	// Text reproduces the three pieces this has always printed in the order it
	// printed them: the message, the blank line, the usage page. remedy and the
	// summary are additions an agent reads and a person does not.
	refusal.NoReport("endless-migrate: "+msg, remedy).
		Command("migrate").
		Text(fmt.Sprintf("endless-migrate: %s\n\n%s", msg, usageText())).
		Exit(2)
	return nil
}

// emit prints the result document on stdout and exits 0.
func emit(res any) {
	b, err := json.Marshal(res)
	if err != nil {
		// The result document is the only thing that tells the caller what
		// happened, and a shape built two lines above out of plain strings and
		// ints cannot normally fail to marshal — so if it did, the migration
		// may well have taken effect with nobody told. That is not a retry: a
		// second `apply` would report "already applied" and prove nothing.
		refusal.Report(
			fmt.Sprintf("endless-migrate: encoding the result: %v", err),
			"what state the database is in; the migration ran but its result "+
				"document could not be encoded, so nothing was reported",
		).Command("migrate").Exit(1)
	}
	fmt.Println(string(b))
}

// emitError prints a failure as the same kind of document a success gets, on
// stdout, and exits 1 — the shape `endless-go event apply-change` established
// and the Python caller parses.
func emitError(name string, cause error) {
	out := map[string]any{"status": "error", "error": cause.Error()}
	if name != "" {
		out["name"] = name
	}
	b, err := json.Marshal(out)
	if err != nil {
		// The error document itself could not be built, so this line is all
		// the caller gets about a schema change that failed after the land had
		// already merged. The database lags the code until someone resolves
		// the cause, and which way to resolve it is theirs to choose.
		refusal.Report(
			fmt.Sprintf("endless-migrate: %v", cause),
			"how to resolve a schema change that failed to apply; the error "+
				"document could not be encoded, so this line is the whole report",
		).Command("migrate").Exit(1)
	}
	fmt.Println(string(b))
	os.Exit(1)
}

// usageText is the usage page as a string rather than as writes to a stream.
//
// It is a string because its two readers want it in two different places.
// `--help` prints it on stdout and exits 0; a usage refusal hands it to
// refusal.Error.Text, which renders it for whichever audience is reading and
// brackets it with a verdict for an agent. A func(io.Writer) could serve only
// the first of those.
func usageText() string {
	return strings.Join([]string{
		"endless-migrate — apply Endless schema changes and migrations, and nothing else.",
		"",
		"Usage:",
		"  endless-migrate [--db main | --db-dir <dir>] apply <change-file>",
		"  endless-migrate [--db main | --db-dir <dir>] up",
		"",
		"Commands:",
		"  apply <change-file>   Apply one internal/schema/changes/<name>.{sql,go}",
		"                        file and record it in _schema_version. Already",
		"                        applied changes are skipped.",
		"  up                    Apply every versioned migration this binary",
		"                        embeds that the database lacks, then reconcile",
		"                        the enum mirrors. A current database is a no-op.",
		"",
		"Flags:",
		"  --db main             The project's main database, ~/.config/endless",
		"                        (follows $HOME, ignores XDG_CONFIG_HOME).",
		"  --db-dir <dir>        The Endless config directory holding the database",
		"                        to migrate. Defaults to XDG_CONFIG_HOME/endless,",
		"                        else ~/.config/endless.",
		"",
		"  --db sandbox is refused: this binary resolves its target from what you",
		"  name, never from where you are standing, so it cannot say which sandbox",
		"  you meant. Name one with --db-dir.",
		"",
		"This binary carries the migration set and nothing else: it serves no hook,",
		"runs no task command, answers no query, and touches no business data outside",
		"a migration. That is what lets `endless worktree land` run it against the",
		"real ledger where an unlanded endless-go build may not.",
	}, "\n") + "\n"
}
