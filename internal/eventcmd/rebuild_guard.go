package eventcmd

import (
	"database/sql"
	"fmt"
	"io"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-2062: `endless-go event rebuild-db --confirm` is refused on purpose.
//
// It could never run anyway. `sessions.task_id` is write-once (ED-1560,
// enforced by a BEFORE UPDATE trigger), the FK on that column is
// `ON DELETE SET NULL`, and an FK-driven SET NULL is an implicit UPDATE — so
// the command's `DELETE FROM tasks` aborts on any database where some session
// is bound to a task. That abort is an ACCIDENT, and it is load-bearing:
// without it the same DELETE cascades through four more tables that the
// copy-back never restores. A future sweep that "fixes" the abort by relaxing
// the trigger or the FK action would trade a loud refusal for silent,
// unrecoverable data loss.
//
// So the accident is replaced with an intention: refuse up front, before the
// projection is built and before any transaction opens, and print what would
// have been lost — counted from the live database, not described in the
// abstract. This does NOT repair the rebuild. That is E-799, the event-sourcing
// epic, which owns "SQLite as a rebuildable projection". The order is fixed and
// written down in E-2062's "Sequencing": guard first, repair second, relax the
// schema third. Step three looks obviously correct in isolation, which is
// exactly why it must come last.
//
// The dry run (no --confirm) is untouched: it is read-only, it builds the
// projection and prints its counts, and it is the useful half of the command.

// rebuildLoss counts, in the LIVE database, the rows a `rebuild-db --confirm`
// would destroy or orphan.
//
// runRebuildDB copies back exactly three tables — tasks, decisions,
// decision_relations — so everything counted here is reached by the cascade and
// then never put back. SessionGates is the one to read twice: it cascades
// ONWARD to report_judgments and report_labels, so a sweep that walks only the
// direct children of `tasks` misses two whole tables.
type rebuildLoss struct {
	TaskLandings    int64 // tasks(id) ON DELETE CASCADE — destroyed
	SessionGates    int64 // tasks(id) via epic_id, CASCADE — destroyed
	ReportJudgments int64 // session_gates(id) CASCADE — destroyed, second hop
	ReportLabels    int64 // session_gates(id) CASCADE — destroyed, second hop
	SessionBindings int64 // sessions.task_id SET NULL — forbidden by ED-1560

	// session_statuses is a dead feature being removed under separate work
	// (E-2062 "Not in scope"). Counted while it exists, absent from the report
	// once it does not, so its removal needs no edit here.
	SessionStatuses    int64
	HasSessionStatuses bool

	// Counted is false when the live database could not be read. The refusal
	// still stands — a database this command cannot even count is not one it
	// should be rewriting — but it says so instead of printing zeros, which
	// would read as "nothing to lose".
	Counted bool
	Err     error
}

// countTableRows returns the row count for table, and whether the table exists
// at all. Existence is checked first so a table retired after this code was
// written degrades to "not reported" rather than to an error.
//
// table and where are compile-time constants from countRebuildLoss below, never
// caller input — SQLite cannot parameterize an identifier.
func countTableRows(db *sql.DB, table, where string) (n int64, exists bool, err error) {
	var found int
	err = db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&found)
	if err != nil {
		return 0, false, err
	}
	if found == 0 {
		return 0, false, nil
	}

	stmt := "SELECT count(*) FROM " + table
	if where != "" {
		stmt += " WHERE " + where
	}
	if err = db.QueryRow(stmt).Scan(&n); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// countRebuildLoss measures the live database. It is READ-ONLY by construction:
// every statement it issues is a SELECT.
//
// The counts are database-wide rather than scoped to the project named by
// --project-root. Scoping would need the projected `projects` set, which only
// exists after the projection this guard deliberately runs before; and a
// refusal is a refusal at any count, so the number's job is to convey scale,
// not to gate a decision.
func countRebuildLoss(db *sql.DB) rebuildLoss {
	loss := rebuildLoss{}

	type probe struct {
		table string
		where string
		dest  *int64
		found *bool
	}
	probes := []probe{
		{table: "task_landings", dest: &loss.TaskLandings},
		{table: "session_gates", dest: &loss.SessionGates},
		{table: "report_judgments", dest: &loss.ReportJudgments},
		{table: "report_labels", dest: &loss.ReportLabels},
		{table: "sessions", where: "task_id IS NOT NULL", dest: &loss.SessionBindings},
		{table: "session_statuses", dest: &loss.SessionStatuses, found: &loss.HasSessionStatuses},
	}

	for _, p := range probes {
		n, exists, err := countTableRows(db, p.table, p.where)
		if err != nil {
			loss.Err = fmt.Errorf("counting %s: %w", p.table, err)
			return loss
		}
		*p.dest = n
		if p.found != nil {
			*p.found = exists
		}
	}

	loss.Counted = true
	return loss
}

// writeRebuildRefusal renders the refusal. Separate from the exit so tests can
// read it, and so the message stays one block of text instead of a trail of
// Fprintf calls through the command body.
func writeRebuildRefusal(w io.Writer, loss rebuildLoss) {
	fmt.Fprint(w, "rebuild-db --confirm is disabled: it would destroy data the ledger cannot\nrestore.\n\n")

	if !loss.Counted {
		fmt.Fprintf(w, "  (could not count what would be lost: %v)\n\n", loss.Err)
	} else {
		row := func(count string, label string, note string) {
			fmt.Fprintf(w, "  %6s %-21s %s\n", count, label, note)
		}
		num := func(n int64) string { return fmt.Sprintf("%d", n) }

		row(num(loss.TaskLandings), "task_landings rows", "(landing history)")
		row(num(loss.SessionGates), "session_gates rows",
			fmt.Sprintf("(and %d report_judgments, %d report_labels)",
				loss.ReportJudgments, loss.ReportLabels))
		row(num(loss.SessionBindings), "sessions bindings", "(violates ED-1560)")
		if loss.HasSessionStatuses {
			row(num(loss.SessionStatuses), "session_statuses rows", "(task attribution nulled)")
		}
		row("", "task_deps", "not rebuilt at all — left stale")
		fmt.Fprintln(w)
	}

	fmt.Fprint(w, `The copy-back restores only tasks, decisions and decision_relations, so nothing
above is put back: the counted rows are destroyed or orphaned by the cascade,
and task_deps is simply never written. The projection feeding the copy-back is
also unreliable while Endless task E-1041 is open.

Repairing this is Endless task E-799. Do not remove the write-once trigger on
sessions.task_id, or its `+"`ON DELETE SET NULL`"+`, to make this command run:
that converts a loud refusal into silent data loss. See "Sequencing" in E-2062.

The dry run — this command without --confirm — is unaffected.
`)
}

// refuseRebuildDBConfirm prints the refusal and exits non-zero. It never
// returns, so no caller can fall through into the destructive path.
//
// A database that cannot be opened is still refused, and refused FIRST: the
// error is reported inside the refusal rather than short-circuiting to the
// generic "error:" exit, so the reason the command stopped is always the
// same reason.
func refuseRebuildDBConfirm() {
	loss := rebuildLoss{}
	db, err := monitor.DB()
	if err != nil {
		loss.Err = err
	} else {
		loss = countRebuildLoss(db)
	}
	writeRebuildRefusal(os.Stderr, loss)
	os.Exit(1)
}
