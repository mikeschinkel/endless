package monitor

import (
	"database/sql"
	"errors"
	"fmt"
)

// report_turn.go holds the per-TURN state the minimizer gate reasons over
// (E-1953), as opposed to the per-REPORT state in session_gate.go.
//
// The distinction is what the lifetimes are. A `session_gates` row is permanent
// — it is a corpus sample, and it outlives the turn that produced it. The three
// columns here are scratch: they describe the turn currently in flight and are
// reset the moment the user speaks again, because a turn is exactly the span
// between two user prompts.
//
// They live on `sessions` rather than in the hook process because each hook
// firing is a separate process. UserPromptSubmit stages the prompt and the
// `$FULL` license; Stop reads them back. Nothing in memory survives that gap.

// ReportBounceLimit caps how many times the Stop gate may block a turn for
// never having produced a report before it gives up and lets the turn end.
//
// Mirrors RelayBounceLimit, and deliberately a separate constant even though
// both are 2: they guard different failures. RelayBounceLimit counts on a
// checkpoint row and catches an agent that reported and then embellished;
// this one has no row to count on and catches an agent that never reported at
// all — the case where the agent may simply not understand the instruction, and
// where an uncapped block is a livelock rather than a correction.
const ReportBounceLimit = 2

// StageUserPrompt records the turn's prompting message and resets the per-turn
// report state. Called at UserPromptSubmit, which is the only event that can
// mean "a new turn started".
//
// The reset is the load-bearing half. Without it a session that exhausted its
// bounce budget on one turn would carry the spent budget forever and never be
// gated again, which is the silent-surrender failure this gate is supposed to
// make visible rather than inherit.
func StageUserPrompt(sessionID int64, prompt string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	if _, err = db.Exec(
		`UPDATE sessions
		 SET last_user_prompt=?, report_bounces=0, report_exempt=0, report_runs=0
		 WHERE id=?`,
		nullString(prompt), sessionID,
	); err != nil {
		return fmt.Errorf("stage user prompt for session %d: %w", sessionID, err)
	}
	return nil
}

// ReportRunsThisTurn returns how many times `task report` has produced output
// since the user last spoke. 0 means the turn has not reported; 1 means it has
// and still holds its appeal; ReportRunLimit or more means the appeal is spent.
func ReportRunsThisTurn(sessionID int64) (runs int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	err = db.QueryRow(`SELECT report_runs FROM sessions WHERE id=?`, sessionID).Scan(&runs)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read report runs for session %d: %w", sessionID, err)
	}
	return runs, nil
}

// ReportRunLimit is how many times one turn may run the minimizer: once for the
// report, once for the appeal.
//
// The appeal has to be bounded and bounded hard. An agent that can re-run the
// minimizer freely will keep re-drafting until something it likes survives,
// which is not an appeal — it is the self-judgment the minimizer replaced,
// reintroduced through the back door. One bite, and the appeal text goes through
// the minimizer too, so arguing for the cut content costs the same scrutiny as
// saying it did.
const ReportRunLimit = 2

// LastUserPrompt returns the prompting message staged for the turn in flight.
// found is false when the session has never had one staged — a session whose
// first event was not UserPromptSubmit, which is normal at SessionStart.
func LastUserPrompt(sessionID int64) (prompt string, found bool, err error) {
	db, err := DB()
	if err != nil {
		return "", false, err
	}
	var p sql.NullString
	err = db.QueryRow(`SELECT last_user_prompt FROM sessions WHERE id=?`, sessionID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query last user prompt for session %d: %w", sessionID, err)
	}
	return p.String, p.Valid, nil
}

// GrantReportExemption marks the turn in flight as exempt from the gate — the
// `$FULL` license (E-1953). Set at UserPromptSubmit and consumed at Stop.
//
// `$FULL` licenses ONE response, not a mode, which is why the flag is cleared by
// StageUserPrompt on the next prompt as well as by the Stop that consumes it. A
// license that outlived its turn would be indistinguishable from the gate being
// off, and the user has no way to see which they are in.
func GrantReportExemption(sessionID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}
	if _, err = db.Exec(`UPDATE sessions SET report_exempt=1 WHERE id=?`, sessionID); err != nil {
		return fmt.Errorf("grant report exemption for session %d: %w", sessionID, err)
	}
	return nil
}

// ConsumeReportExemption reports whether the turn in flight holds a `$FULL`
// license, clearing it in the same breath so it cannot cover a second turn.
//
// Read-and-clear rather than read-then-clear-later: a Stop that returns early
// (an error, a build that does not populate the final message) must still burn
// the license, or an exempt turn that never reached the comparison would leave
// the flag set for whatever comes next.
func ConsumeReportExemption(sessionID int64) (exempt bool, err error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	var flag int
	err = db.QueryRow(`SELECT report_exempt FROM sessions WHERE id=?`, sessionID).Scan(&flag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query report exemption for session %d: %w", sessionID, err)
	}
	if flag == 0 {
		return false, nil
	}
	if _, err = db.Exec(`UPDATE sessions SET report_exempt=0 WHERE id=?`, sessionID); err != nil {
		return true, fmt.Errorf("clear report exemption for session %d: %w", sessionID, err)
	}
	return true, nil
}

// BumpReportBounce increments the turn's never-called bounce counter and returns
// the new value. The caller compares it against ReportBounceLimit to decide
// between blocking again and surrendering out loud.
func BumpReportBounce(sessionID int64) (bounces int, err error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	if _, err = db.Exec(
		`UPDATE sessions SET report_bounces = report_bounces + 1 WHERE id=?`, sessionID,
	); err != nil {
		return 0, fmt.Errorf("bump report bounce for session %d: %w", sessionID, err)
	}
	var n int
	if err = db.QueryRow(`SELECT report_bounces FROM sessions WHERE id=?`, sessionID).Scan(&n); err != nil {
		return 0, fmt.Errorf("read report bounces for session %d: %w", sessionID, err)
	}
	return n, nil
}
