package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/gatekind"
)

// session_gate.go holds the direct-db.Exec helpers backing the session gates:
// the pause-on-revisit hook (E-1542) and the verbatim-report-relay checkpoint
// (E-1901). session_gates is ephemeral session-scoped state — like sessions,
// activity, and channels it is written directly here, not through the event
// ledger (its audit lives in the table's own triggered_at/cleared_* cols).
// Each kind owns its own helper set; they share only the table and the
// supersede-on-insert discipline that keeps at most one open row per
// (session_id, kind_id).

// NearestRevisitEpicAncestor walks up tasks.parent_id from taskID and returns
// the id of the nearest ancestor that is an epic currently in status='revisit'.
// taskID itself is included in the walk (depth 0). The depth is capped at 32 so
// a malformed parent_id cycle terminates instead of looping forever. found is
// false when no such ancestor exists.
func NearestRevisitEpicAncestor(taskID int64) (epicID int64, found bool, err error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}
	const q = `
		WITH RECURSIVE ancestry(id, parent_id, type_id, status, depth) AS (
			SELECT id, parent_id, type_id, status, 0 FROM live_tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_id, t.type_id, t.status, a.depth + 1
			FROM live_tasks t JOIN ancestry a ON t.id = a.parent_id
			WHERE a.depth < 32
		)
		SELECT a.id
		FROM ancestry a
		JOIN task_types tt ON tt.id = a.type_id
		WHERE tt.slug = 'epic' AND a.status = 'revisit'
		ORDER BY a.depth
		LIMIT 1`
	err = db.QueryRow(q, taskID).Scan(&epicID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve revisit epic ancestor for E-%d: %w", taskID, err)
	}
	return epicID, true, nil
}

// SetRevisitGate opens a revisit gate for the session against epicID. Any prior
// open revisit gate for the session is first cleared with cleared_by='superseded'
// so at most one open row exists per (session_id, kind=revisit).
func SetRevisitGate(sessionID, epicID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err = db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by='superseded'
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, sessionID, int(gatekind.GateKindRevisit),
	); err != nil {
		return fmt.Errorf("supersede open revisit gate for session %d: %w", sessionID, err)
	}
	if _, err = db.Exec(
		`INSERT INTO session_gates (session_id, kind_id, epic_id, triggered_at)
		 VALUES (?, ?, ?, ?)`,
		sessionID, int(gatekind.GateKindRevisit), epicID, now,
	); err != nil {
		return fmt.Errorf("insert revisit gate for session %d: %w", sessionID, err)
	}
	return nil
}

// PendingRevisitGate returns the epic id of the session's open revisit gate, if
// any. found is false when the session has no open revisit gate.
func PendingRevisitGate(sessionID int64) (epicID int64, found bool, err error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}
	var epic sql.NullInt64
	err = db.QueryRow(
		`SELECT epic_id FROM session_gates
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL
		 ORDER BY id DESC LIMIT 1`,
		sessionID, int(gatekind.GateKindRevisit),
	).Scan(&epic)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query pending revisit gate for session %d: %w", sessionID, err)
	}
	return epic.Int64, true, nil
}

// ClearRevisitGate closes the session's open revisit gate(s) with the given
// cleared_by reason (revisit_continue, revisit_pause, or revisit_resolved) and
// returns the number of rows cleared — 0 means there was no pending prompt.
func ClearRevisitGate(sessionID int64, clearedBy string) (cleared int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by=?
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, clearedBy, sessionID, int(gatekind.GateKindRevisit),
	)
	if err != nil {
		return 0, fmt.Errorf("clear revisit gate for session %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revisit gate rows affected: %w", err)
	}
	return int(n), nil
}

// --- 'relay' kind: the verbatim-report-relay checkpoint (E-1901) -------------

// RelayBounceLimit caps how many times the Stop gate may block one checkpoint
// before it gives up and lets the turn end. It exists because Claude Code's
// stop_hook_active flag is undocumented: relying on it alone to break the
// re-prompt loop would stake a livelock on unspecified behavior, so the loop
// guard is a counter we own. Two bounces is the whole budget — the first names
// the violation, the second catches a careless re-send; a third would be a model
// that is not going to comply, and holding the turn hostage past that point
// costs the user more than the appended prose did.
const RelayBounceLimit = 2

// SetRelayCheckpoint opens a relay checkpoint for the session, recording the
// exact text the session owes the user as its final message. Any prior open
// relay gate is first cleared with cleared_by='relay_superseded' so at most one
// open row exists per (session_id, kind=relay) — re-running `task report` in the
// same turn legitimately replaces the sanctioned text (that is the prescribed
// way to add a note or question), and only the newest text can be owed.
func SetRelayCheckpoint(sessionID int64, sanctioned string) error {
	return SetReportCheckpoint(sessionID, ReportCheckpoint{
		Variants: []ReportVariant{{Sanctioned: sanctioned}},
	})
}

// ReportVariant is one minimization of one draft: the text it produced, and the
// variant bundle that produced it.
//
// Slot is "" for an ordinary turn and "A"/"B" on a paired one. Each variant gets
// its OWN corpus row, because the judge scores a minimization, not a
// presentation — scoring the combined A/B block would measure the experiment's
// packaging rather than either prompt.
type ReportVariant struct {
	Sanctioned string // this variant's minimized output
	Slot       string // "", "A" or "B"
	VariantHash string
	Bypassed   bool // fell under the variant's bypass threshold; never minimized
}

// ReportCheckpoint is one reported turn: the minimizer's output (which the
// session now owes the user verbatim) plus everything a paired replay needs to
// re-run the turn later. TaskID is optional — an id-less report is legitimate,
// so the row keys on the session and the task is attribution only (E-1953).
type ReportCheckpoint struct {
	Variants   []ReportVariant
	Emitted    string // what the command PRINTED, when it differs from a single variant
	RawDraft   string // the agent's whole freeform draft, before minimization
	UserPrompt string // the message that prompted the turn
	// Context is the JSON record of what the fetch policy asked for and what came
	// back. Recording is not optional bookkeeping: a minimizer that fetches is
	// nondeterministic in its INPUTS, so replay must serve context from here
	// rather than re-fetch state that has since moved (E-1975).
	Context  string
	TaskType string
	TaskID   *int64
}

// SetReportCheckpoint opens a relay checkpoint for the session, recording the
// exact text the session owes the user as its final message alongside the raw
// draft, prompting message and fetched context it was minimized from.
//
// Superseding CLOSES the older row, it does not delete it. That is deliberate:
// the corpus wants every draft the session produced this turn, including the one
// the agent thought better of, because an agent that re-runs the minimizer is
// itself a signal about the first output.
//
// A paired turn writes TWO rows and leaves only the FIRST open. Both are corpus;
// only one can be the checkpoint, because "at most one open row per (session,
// kind)" is what makes the Stop gate's lookup unambiguous. The B row is closed
// immediately as 'relay_pair' and reached through pair_id, which is what lets
// the gate accept any of N sanctioned texts without a second open row.
func SetReportCheckpoint(sessionID int64, cp ReportCheckpoint) error {
	db, err := DB()
	if err != nil {
		return err
	}
	if len(cp.Variants) == 0 {
		return fmt.Errorf("report checkpoint for session %d carries no variants", sessionID)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err = db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by='relay_superseded'
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, sessionID, int(gatekind.GateKindRelay),
	); err != nil {
		return fmt.Errorf("supersede open relay checkpoint for session %d: %w", sessionID, err)
	}

	var leadID int64
	for i, v := range cp.Variants {
		// Only the lead row stays open, and only it carries the emitted text —
		// the gate reads one row and follows pair_id for the rest.
		var closedAt, closedBy any
		var emitted any
		if i > 0 {
			closedAt, closedBy = now, "relay_pair"
		} else if cp.Emitted != "" && cp.Emitted != v.Sanctioned {
			emitted = cp.Emitted
		}
		res, ierr := db.Exec(
			`INSERT INTO session_gates
			   (session_id, kind_id, sanctioned_text, emitted_text, raw_draft, user_prompt,
			    task_id, task_type, fetched_context, variant_hash, pair_slot, bypassed,
			    bounces, triggered_at, cleared_at, cleared_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
			sessionID, int(gatekind.GateKindRelay), v.Sanctioned, emitted,
			nullString(cp.RawDraft), nullString(cp.UserPrompt), cp.TaskID,
			nullString(cp.TaskType), nullString(cp.Context), nullString(v.VariantHash),
			nullString(v.Slot), boolInt(v.Bypassed), now, closedAt, closedBy,
		)
		if ierr != nil {
			return fmt.Errorf("insert relay checkpoint for session %d: %w", sessionID, ierr)
		}
		id, ierr := res.LastInsertId()
		if ierr != nil {
			return fmt.Errorf("relay checkpoint id for session %d: %w", sessionID, ierr)
		}
		if i == 0 {
			leadID = id
		}
	}

	if len(cp.Variants) > 1 {
		if _, err = db.Exec(
			`UPDATE session_gates SET pair_id=? WHERE id>=? AND session_id=? AND kind_id=?`,
			leadID, leadID, sessionID, int(gatekind.GateKindRelay),
		); err != nil {
			return fmt.Errorf("group relay pair for session %d: %w", sessionID, err)
		}
	}

	// Count the run against the turn's appeal budget. Incremented HERE rather
	// than at the command's entry point so only a run that actually produced
	// output spends the budget — a run that failed to reach the minimizer must
	// not cost the agent its one appeal. A pair is ONE run: the doubling is the
	// experiment's cost, not a second bite at the appeal.
	if _, err = db.Exec(
		`UPDATE sessions SET report_runs = report_runs + 1 WHERE id=?`, sessionID,
	); err != nil {
		return fmt.Errorf("count report run for session %d: %w", sessionID, err)
	}
	return nil
}

// boolInt maps a Go bool onto SQLite's 0/1.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullString maps "" to a SQL NULL so an absent leg of the corpus triple is
// distinguishable from a genuinely empty one. `--raw` needs that distinction:
// "no draft was persisted" and "the draft was blank" call for different answers.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LatestReportDraft returns the raw draft of the session's most recent report,
// open or already cleared. Backs `task report --raw`, so it deliberately ignores
// cleared_at: the agent asks for the raw draft precisely when the minimized
// version turned out to be missing something, which is usually after the
// checkpoint has been consumed.
func LatestReportDraft(sessionID int64) (draft string, found bool, err error) {
	db, err := DB()
	if err != nil {
		return "", false, err
	}
	var raw sql.NullString
	err = db.QueryRow(
		`SELECT raw_draft FROM session_gates
		 WHERE session_id=? AND kind_id=? AND raw_draft IS NOT NULL
		 ORDER BY id DESC LIMIT 1`,
		sessionID, int(gatekind.GateKindRelay),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query latest report draft for session %d: %w", sessionID, err)
	}
	return raw.String, true, nil
}

// LabelLatestReport attaches a `$CUT`/`$BLOAT`/`$WRONG`/`$GOOD` label to the
// session's most recent corpus row and returns whether a row was found.
//
// "Most recent" is the right target because the label arrives on the turn AFTER
// the one it judges: the user reads the minimized output, then types their
// complaint as the next prompt. By then the row is closed, so this too ignores
// cleared_at.
func LabelLatestReport(sessionID int64, label, text string) (found bool, err error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	res, err := db.Exec(
		`UPDATE session_gates SET label=?, label_text=?
		 WHERE id = (SELECT id FROM session_gates
		             WHERE session_id=? AND kind_id=? AND sanctioned_text IS NOT NULL
		             ORDER BY id DESC LIMIT 1)`,
		label, nullString(text), sessionID, int(gatekind.GateKindRelay),
	)
	if err != nil {
		return false, fmt.Errorf("label latest report for session %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("report label rows affected: %w", err)
	}
	return n > 0, nil
}

// PendingRelayCheckpoint returns the text the session owes, and the bounce
// count, for its open relay checkpoint. found is false when the session owes no
// report — the overwhelmingly common case, since a session only owes one between
// running `task report` and ending that turn.
//
// Owed is what the command PRINTED, which on a paired turn is the combined A/B
// presentation rather than either variant.
func PendingRelayCheckpoint(sessionID int64) (owed string, bounces int, found bool, err error) {
	cp, bounces, found, err := PendingReportCheckpoint(sessionID)
	if err != nil || !found {
		return "", bounces, found, err
	}
	return cp.Owed, bounces, true, nil
}

// PendingCheckpoint is the Stop gate's view of one open checkpoint: the text the
// agent owes verbatim, plus every OTHER text that is also an acceptable final
// message.
//
// Accepted is any-of-N rather than exactly-one, and that is bought deliberately
// rather than needed today. Under the shipped A/B shape the agent relays the
// combined block, so Owed alone would do. It is the single blocker between that
// shape and both later ones — an AskUserQuestion preview where the agent sends
// the WINNING variant, and a left/right TUI — and paying for it now costs one
// slice comparison instead of a schema change later.
type PendingCheckpoint struct {
	Owed     string
	Accepted []string
}

// PendingReportCheckpoint returns the session's open checkpoint together with
// its paired siblings.
func PendingReportCheckpoint(sessionID int64) (cp PendingCheckpoint, bounces int, found bool, err error) {
	db, err := DB()
	if err != nil {
		return cp, 0, false, err
	}
	var id int64
	var sanctioned, emitted sql.NullString
	var pairID sql.NullInt64
	err = db.QueryRow(
		`SELECT id, sanctioned_text, emitted_text, pair_id, bounces FROM session_gates
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL
		 ORDER BY id DESC LIMIT 1`,
		sessionID, int(gatekind.GateKindRelay),
	).Scan(&id, &sanctioned, &emitted, &pairID, &bounces)
	if errors.Is(err, sql.ErrNoRows) {
		return cp, 0, false, nil
	}
	if err != nil {
		return cp, 0, false, fmt.Errorf("query pending relay checkpoint for session %d: %w", sessionID, err)
	}

	cp.Owed = sanctioned.String
	if emitted.Valid && emitted.String != "" {
		cp.Owed = emitted.String
	}
	cp.Accepted = []string{cp.Owed}
	if sanctioned.Valid && sanctioned.String != cp.Owed {
		cp.Accepted = append(cp.Accepted, sanctioned.String)
	}
	if !pairID.Valid {
		return cp, bounces, true, nil
	}

	rows, err := db.Query(
		`SELECT sanctioned_text FROM session_gates
		 WHERE pair_id=? AND id<>? AND kind_id=? AND sanctioned_text IS NOT NULL
		 ORDER BY id`,
		pairID.Int64, id, int(gatekind.GateKindRelay),
	)
	if err != nil {
		return cp, bounces, true, fmt.Errorf("query relay pair for session %d: %w", sessionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if serr := rows.Scan(&text); serr != nil {
			return cp, bounces, true, fmt.Errorf("scan relay pair for session %d: %w", sessionID, serr)
		}
		cp.Accepted = append(cp.Accepted, text)
	}
	return cp, bounces, true, rows.Err()
}

// ClearRelayCheckpoint closes the session's open relay checkpoint(s) with the
// given cleared_by reason (relay_complied, relay_exhausted, or relay_superseded)
// and returns the number of rows cleared — 0 means nothing was owed.
func ClearRelayCheckpoint(sessionID int64, clearedBy string) (cleared int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`UPDATE session_gates SET cleared_at=?, cleared_by=?
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		now, clearedBy, sessionID, int(gatekind.GateKindRelay),
	)
	if err != nil {
		return 0, fmt.Errorf("clear relay checkpoint for session %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("relay checkpoint rows affected: %w", err)
	}
	return int(n), nil
}

// BumpRelayBounce increments the open relay checkpoint's bounce counter and
// returns the new value. Called each time the Stop gate blocks, so the next Stop
// can tell a first offense from an exhausted budget.
func BumpRelayBounce(sessionID int64) (bounces int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	if _, err = db.Exec(
		`UPDATE session_gates SET bounces = bounces + 1
		 WHERE session_id=? AND kind_id=? AND cleared_at IS NULL`,
		sessionID, int(gatekind.GateKindRelay),
	); err != nil {
		return 0, fmt.Errorf("bump relay bounce for session %d: %w", sessionID, err)
	}
	_, bounces, _, err = PendingRelayCheckpoint(sessionID)
	return bounces, err
}
