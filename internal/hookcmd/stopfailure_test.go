package hookcmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/outages"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// E-2145. The second end-of-turn event.
//
// These drive the handler with SYNTHETIC payloads rather than a live harness,
// for the reason the Notification suite gives: the fact under test is what
// Endless does with a `StopFailure`, and Claude Code's willingness to emit one
// is not something a test here can establish either way.
//
// The headline test goes through runClaude rather than calling the handler
// directly, and that is the point of it. A handler test would have passed the
// day before the fix — by testing a function that did not exist. Only the full
// dispatch can witness the actual defect, which was an event falling through a
// switch with no `default` and no case for it.

// stopFailureEnv seeds a project and one session in `state`, points both the
// monitor singleton and the fault store at a throwaway database, and returns it.
//
// One database for both, deliberately: `errors.project_id` is a foreign key into
// `projects`, so a fault attributed to the hook's project is only accepted if
// the project the session lives in is in the same file the faults package
// writes to — which is exactly the arrangement in a real process, where
// faults.Bind is handed monitor.DB.
func stopFailureEnv(t *testing.T, state string) (db *sql.DB, projectPath string) {
	t.Helper()

	projectPath = t.TempDir()
	db = newSchemaDB(t)

	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil, nil) })

	// No test may reach a real status page: Anthropic reports no incident
	// unless a test says otherwise (stubAnthropic).
	stubAnthropic(t, false)

	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj', ?)", projectPath,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, task_id,
		                       started_at, last_activity)
		 VALUES (1, 'sess-A', 1, 'claude', ?, NULL,
		         '2026-09-01T00:00:00', '2026-09-01T00:00:00')`, state,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return db, projectPath
}

// fireStopFailure drives the WHOLE hook the way Claude Code does: a JSON payload
// on stdin, through runClaude, into the event switch.
func fireStopFailure(t *testing.T, projectPath, errorType string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	payload, err := json.Marshal(map[string]any{
		"session_id":      "sess-A",
		"cwd":             projectPath,
		"hook_event_name": "StopFailure",
		"error_type":      errorType,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err = w.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	w.Close()

	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })

	if err = runClaude(nil); err != nil {
		t.Fatalf("runClaude on StopFailure: %v", err)
	}
}

func stopFailureSessionState(t *testing.T, db *sql.DB) (state string, lastActivity string) {
	t.Helper()
	if err := db.QueryRow(
		"SELECT state, last_activity FROM sessions WHERE session_id='sess-A'",
	).Scan(&state, &lastActivity); err != nil {
		t.Fatalf("read session: %v", err)
	}
	return state, lastActivity
}

type recordedFault struct {
	code        string
	severity    string
	source      string
	summary     string
	occurrences int
	projectID   sql.NullInt64
}

func openFaults(t *testing.T, db *sql.DB) (found []recordedFault) {
	t.Helper()
	rows, err := db.Query(
		`SELECT code, severity, source, summary, occurrences, project_id
		   FROM errors WHERE cleared_at IS NULL ORDER BY id`,
	)
	if err != nil {
		t.Fatalf("read errors: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f recordedFault
		if err = rows.Scan(&f.code, &f.severity, &f.source, &f.summary,
			&f.occurrences, &f.projectID); err != nil {
			t.Fatalf("scan error row: %v", err)
		}
		found = append(found, f)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate errors: %v", err)
	}
	return found
}

// TestStopFailure_IdlesASessionLeftWorkingByADeadTurn is the defect, reproduced.
//
// Before the fix this test failed with state = "working": Claude Code fires
// `StopFailure` instead of `Stop`, the switch had no case for it, and nothing
// else moves a live session — so the row asserted work in flight on a turn that
// had died, indefinitely.
func TestStopFailure_IdlesASessionLeftWorkingByADeadTurn(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	fireStopFailure(t, projectPath, "rate_limit")

	state, _ := stopFailureSessionState(t, db)
	if state != sessionstate.Idle {
		t.Errorf("state after a failed turn = %q, want %q — the session is "+
			"paused waiting on a person, which is what `idle` means", state,
			sessionstate.Idle)
	}
}

// TestStopFailure_DoesNotLeaveAFreshTimestampOnALiveState is why installing the
// event alone would have been worse than not installing it.
//
// TouchSession runs BEFORE the event switch, so merely hooking the event
// refreshes `last_activity` and binds the pane. A bare install would therefore
// have left the session reading `working` with a timestamp saying it was active
// seconds ago — `project monitor`'s age column vouching for a dead turn.
//
// Both halves are asserted together on purpose: the timestamp moving is correct
// (the hook did just run), and it is only honest because the state moved with
// it.
func TestStopFailure_DoesNotLeaveAFreshTimestampOnALiveState(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	_, before := stopFailureSessionState(t, db)
	fireStopFailure(t, projectPath, "overloaded")
	state, after := stopFailureSessionState(t, db)

	if after == before {
		t.Fatalf("precondition: last_activity did not move (%q); this test is "+
			"asserting that a refreshed timestamp is accompanied by an honest "+
			"state, and there is nothing to assert if nothing refreshed it", after)
	}
	if state == sessionstate.Working {
		t.Errorf("last_activity refreshed to %q while the state stayed %q — "+
			"exactly the board-lies-about-a-dead-turn case", after, state)
	}
}

// TestStopFailure_RecordsTheFailureAsAFault: the session is honest, and the
// REASON it stopped is not thrown away. No new session state — the failure goes
// where failures already go.
func TestStopFailure_RecordsTheFailureAsAFault(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	fireStopFailure(t, projectPath, "max_output_tokens")

	found := openFaults(t, db)
	if len(found) != 1 {
		t.Fatalf("recorded %d faults, want exactly 1: %+v", len(found), found)
	}
	f := found[0]
	if f.source != "hook:stopfailure" {
		t.Errorf("source = %q, want %q", f.source, "hook:stopfailure")
	}
	if !strings.Contains(f.summary, "max_output_tokens") {
		t.Errorf("summary = %q, want it to NAME the error type — the fault row "+
			"renders this line, and 'a turn failed' without saying how is not "+
			"worth a row", f.summary)
	}
	if !f.projectID.Valid || f.projectID.Int64 != 1 {
		t.Errorf("project_id = %v, want the hook's own project (1) — one "+
			"database holds every project on the machine", f.projectID)
	}
}

// TestStopFailure_SeverityFollowsTheErrorType pins the mapping.
//
// Severity buys PROMINENCE, not longevity: the fault row renders a single line
// and the most severe open incident wins it. A failure nothing can self-heal
// should take that line from a rate limit that will have passed by the time
// anyone looks.
func TestStopFailure_SeverityFollowsTheErrorType(t *testing.T) {
	cases := []struct {
		errorType string
		wantCode  string
		wantSev   string
		why       string
	}{
		{"rate_limit", "WARN-0013", "warning", "waiting is the whole remedy"},
		{"overloaded", "WARN-0013", "warning", "the server recovers on its own"},
		{"server_error", "WARN-0013", "warning", "transient by definition"},
		{"max_output_tokens", "WARN-0013", "warning", "an ordinary outcome of a long turn"},
		{"authentication_failed", "ERR-0014", "error", "nobody's retry fixes a credential"},
		{"billing_error", "ERR-0014", "error", "a fact about the account"},
		{"oauth_org_not_allowed", "ERR-0014", "error", "an org policy needs a person"},
		{"account_on_hold", "ERR-0014", "error", "a hold does not lift itself"},
		{"invalid_request", "WARN-0013", "warning", "not in the fatal set, so transient"},
		{"model_not_found", "WARN-0013", "warning", "not in the fatal set, so transient"},
		{"cloud_credential_error", "WARN-0013", "warning", "not in the fatal set, so transient"},
		{"unknown", "WARN-0013", "warning", "an unknown failure is more likely passing than fatal"},
		{"a_type_from_a_future_claude_code", "WARN-0013", "warning",
			"an unrecognised type must not outrank a real error for the fault row's one line"},
	}

	for _, tc := range cases {
		t.Run(tc.errorType, func(t *testing.T) {
			db, projectPath := stopFailureEnv(t, sessionstate.Working)

			fires := 1
			if selfClearingTurnErrorTypes[tc.errorType] {
				// Transient per session (E-2589): a fault only at the threshold.
				fires = TurnFailureThreshold
			}
			for i := 0; i < fires; i++ {
				fireStopFailure(t, projectPath, tc.errorType)
			}

			found := openFaults(t, db)
			if len(found) != 1 {
				t.Fatalf("recorded %d faults, want 1: %+v", len(found), found)
			}
			if found[0].code != tc.wantCode {
				t.Errorf("code = %s, want %s (%s)", found[0].code, tc.wantCode, tc.why)
			}
			if found[0].severity != tc.wantSev {
				t.Errorf("severity = %s, want %s (%s)", found[0].severity, tc.wantSev, tc.why)
			}
		})
	}
}

// TestStopFailure_AnAbsentErrorTypeIsRecordedAsUnknown. `unknown` is a value
// Claude Code documents and emits itself, and a missing field says the same
// thing — the harness did not name the failure. Collapsing the two is the honest
// grouping, and a fault with no error type at all would be a row saying nothing.
func TestStopFailure_AnAbsentErrorTypeIsRecordedAsUnknown(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	for i := 0; i < TurnFailureThreshold; i++ {
		fireStopFailure(t, projectPath, "")
	}

	state, _ := stopFailureSessionState(t, db)
	if state != sessionstate.Idle {
		t.Errorf("state = %q, want %q — an unnamed failure is still a failed turn",
			state, sessionstate.Idle)
	}
	found := openFaults(t, db)
	if len(found) != 1 {
		t.Fatalf("recorded %d faults, want 1: %+v", len(found), found)
	}
	if !strings.Contains(found[0].summary, unreportedTurnErrorType) {
		t.Errorf("summary = %q, want it to say %q", found[0].summary, unreportedTurnErrorType)
	}
}

// TestStopFailure_RepeatsOfOneErrorTypeAreOneIncident. The fingerprint is the
// error type, not the session: a machine whose shared rate limit hit six
// sessions at once must raise ONE incident with six occurrences, not six rows
// competing for the fault row's single line.
func TestStopFailure_RepeatsOfOneErrorTypeAreOneIncident(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	fireStopFailure(t, projectPath, "max_output_tokens")
	fireStopFailure(t, projectPath, "max_output_tokens")
	fireStopFailure(t, projectPath, "max_output_tokens")

	found := openFaults(t, db)
	if len(found) != 1 {
		t.Fatalf("three identical failures opened %d incidents, want 1: %+v",
			len(found), found)
	}
	if found[0].occurrences != 3 {
		t.Errorf("occurrences = %d, want 3", found[0].occurrences)
	}
}

// TestStopFailure_DifferentErrorTypesAreDifferentIncidents is the other half:
// grouping by error type must not collapse a billing failure into a rate limit,
// because the two have nothing in common but the event that reported them.
func TestStopFailure_DifferentErrorTypesAreDifferentIncidents(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	fireStopFailure(t, projectPath, "max_output_tokens")
	fireStopFailure(t, projectPath, "billing_error")

	found := openFaults(t, db)
	if len(found) != 2 {
		t.Fatalf("two distinct failures opened %d incidents, want 2: %+v",
			len(found), found)
	}
}

// TestTurnFailureCode_IsTotal guards the classifier itself: every input must
// come back with a real catalog entry, including one nobody has seen. A zero
// Code would record a fault with an empty ID and no severity — a row in the
// errors table that no surface could explain.
func TestTurnFailureCode_IsTotal(t *testing.T) {
	for _, errorType := range []string{
		"rate_limit", "authentication_failed", "", "unknown", "something_new",
	} {
		code := turnFailureCode(errorType)
		if _, ok := faults.LookupCode(code.ID); !ok {
			t.Errorf("turnFailureCode(%q) = %+v, which is not in the catalog",
				errorType, code)
		}
	}
}

// stubAnthropic makes Anthropic's status page answer confirmed or not, and
// returns how many times it was asked.
func stubAnthropic(t *testing.T, confirmed bool) *int {
	t.Helper()
	var asks int
	prev := confirmOutage
	confirmOutage = func(_ context.Context, p outages.Provider, _ string) bool {
		if p.Name != outages.Anthropic.Name {
			t.Errorf("asked %s about a turn failure", p.Name)
		}
		asks++
		return confirmed
	}
	t.Cleanup(func() { confirmOutage = prev })
	return &asks
}

// fireStop drives a normal end of turn through the whole hook.
func fireStop(t *testing.T, projectPath string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	payload, err := json.Marshal(map[string]any{
		"session_id":      "sess-A",
		"cwd":             projectPath,
		"hook_event_name": "Stop",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err = w.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })
	if err = runClaude(nil); err != nil {
		t.Fatalf("runClaude on Stop: %v", err)
	}
}

// P8: a self-clearing API error is transient per session — no fault below the
// threshold, WARN-0013 at it.
func TestStopFailure_SelfClearingErrorsAreTransientPerSession(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	for i := 0; i < TurnFailureThreshold-1; i++ {
		fireStopFailure(t, projectPath, "unknown")
	}
	if found := openFaults(t, db); len(found) != 0 {
		t.Fatalf("below the threshold: %d faults, want 0: %+v", len(found), found)
	}
	fireStopFailure(t, projectPath, "overloaded")
	found := openFaults(t, db)
	if len(found) != 1 || found[0].code != "WARN-0013" {
		t.Fatalf("at the threshold: %+v, want one WARN-0013", found)
	}
}

// P2: a normal turn after self-clearing errors closes the session's run
// without a fault, and the count starts over.
func TestStop_ClosesTheSessionsTransientRun(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)

	for i := 0; i < TurnFailureThreshold-1; i++ {
		fireStopFailure(t, projectPath, "server_error")
	}
	fireStop(t, projectPath)
	for i := 0; i < TurnFailureThreshold-1; i++ {
		fireStopFailure(t, projectPath, "server_error")
	}
	if found := openFaults(t, db); len(found) != 0 {
		t.Fatalf("a normal turn between did not reset the run: %+v", found)
	}
	var closed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transient_runs
	                        WHERE source = 'hook:stopfailure' AND closed_at IS NOT NULL
	                          AND faulted_at IS NULL`).Scan(&closed); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if closed != 1 {
		t.Errorf("closed unfaulted runs = %d, want 1", closed)
	}
}

// P9: max_output_tokens, authentication and billing errors are faults on
// first occurrence, and never ask the status page.
func TestStopFailure_ActionableErrorsAreFaultsAtOnce(t *testing.T) {
	for _, errorType := range []string{"max_output_tokens", "authentication_failed", "billing_error"} {
		t.Run(errorType, func(t *testing.T) {
			db, projectPath := stopFailureEnv(t, sessionstate.Working)
			asks := stubAnthropic(t, true)

			fireStopFailure(t, projectPath, errorType)

			if found := openFaults(t, db); len(found) != 1 {
				t.Fatalf("faults = %+v, want 1 on first occurrence", found)
			}
			if *asks != 0 {
				t.Errorf("asked the status page %d times", *asks)
			}
		})
	}
}

// P17: during a confirmed Anthropic incident a self-clearing error raises
// nothing and counts nothing.
func TestStopFailure_ConfirmedIncidentIsAnOutageNotAFault(t *testing.T) {
	db, projectPath := stopFailureEnv(t, sessionstate.Working)
	asks := stubAnthropic(t, true)

	for i := 0; i < TurnFailureThreshold+1; i++ {
		fireStopFailure(t, projectPath, "overloaded")
	}
	if found := openFaults(t, db); len(found) != 0 {
		t.Fatalf("faults = %+v, want none during an outage", found)
	}
	if *asks != TurnFailureThreshold+1 {
		t.Errorf("asks = %d", *asks)
	}
	var runs int
	_ = db.QueryRow(`SELECT COUNT(*) FROM transient_runs`).Scan(&runs)
	if runs != 0 {
		t.Errorf("transient runs = %d, want 0", runs)
	}
}
