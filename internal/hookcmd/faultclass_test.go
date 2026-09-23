package hookcmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// Classifying and recording a hook failure (E-1887).
//
// The behaviour under test is the one the four-week ES-1055 outage needed and
// did not have: a hook that cannot reach the database says so somewhere a
// person will look, WITHOUT gaining the ability to block a tool call.

// bindFaultStore points the faults package at a real schema in memory and a
// temp log directory, and returns the DB so a test can read the table directly.
func bindFaultStore(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close db: %v", cerr)
		}
	})
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })

	return db
}

func TestFaultCodeFor_ClassifiesAtTheRaiseSite(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"a write the hook exists to make", dbWriteFailed(errors.New("touching session: boom")), "ERR-0015"},
		{"a read the hook needs first", dbReadFailed(errors.New("looking up project: boom")), "ERR-0016"},
		{"the harness envelope", payloadUnreadable(errors.New("parsing payload: boom")), "ERR-0017"},
		{"unclassified", errors.New("worktree adoption: boom"), "ERR-0018"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := faultCodeFor(tc.err).ID; got != tc.want {
				t.Errorf("faultCodeFor = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFaultCodeFor_SurvivesTheEventTag(t *testing.T) {
	// runClaude tags every error with its event on the way out, which wraps
	// whatever classification the raise site applied. The sink reads both, so
	// the wrapping must not hide either.
	tagged := taggedWithEvent("PreToolUse", "sess-1",
		dbWriteFailed(fmt.Errorf("touching session: %w", errors.New("no such column"))))

	if got := faultCodeFor(tagged).ID; got != "ERR-0015" {
		t.Errorf("faultCodeFor through the event tag = %s, want ERR-0015", got)
	}
	event, session := hookEventContext(tagged)
	if event != "PreToolUse" {
		t.Errorf("event = %q, want PreToolUse", event)
	}
	if session != "sess-1" {
		t.Errorf("session = %q, want sess-1", session)
	}
}

func TestClassed_KeepsTheInnermostClassification(t *testing.T) {
	// The specific class is the one raised closest to the failure. A broader
	// wrapper re-labelling it on the way out would be the string-sniffing
	// problem this design exists to avoid, in a different costume.
	inner := dbWriteFailed(errors.New("touching session: boom"))
	outer := dbReadFailed(fmt.Errorf("while handling PreToolUse: %w", inner))

	if got := faultCodeFor(outer).ID; got != "ERR-0015" {
		t.Errorf("faultCodeFor = %s, want ERR-0015 (the inner class)", got)
	}
}

func TestClassed_PreservesErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	wrapped := dbWriteFailed(fmt.Errorf("touching session: %w", sentinel))

	if !errors.Is(wrapped, sentinel) {
		t.Error("classification broke errors.Is against the cause")
	}
	if wrapped.Error() != "touching session: sentinel" {
		t.Errorf("Error() = %q; classification must not change the text", wrapped.Error())
	}
}

func TestClassed_NilInNilOut(t *testing.T) {
	for name, fn := range map[string]func(error) error{
		"dbWriteFailed":     dbWriteFailed,
		"dbReadFailed":      dbReadFailed,
		"payloadUnreadable": payloadUnreadable,
	} {
		if err := fn(nil); err != nil {
			t.Errorf("%s(nil) = %v, want nil", name, err)
		}
	}
}

func TestRecordHookFault_RecordsOneIncidentWithTheExpectedCode(t *testing.T) {
	db := bindFaultStore(t)

	err := taggedWithEvent("PreToolUse", "sess-42",
		dbWriteFailed(errors.New("touching session: upsert session: table sessions has no column named process")))
	recordHookFault("claude", err)

	incidents, lerr := faults.List(faults.AllProjects, false, 0)
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	if len(incidents) != 1 {
		t.Fatalf("recorded %d incidents, want 1", len(incidents))
	}

	incident := incidents[0]
	if incident.Code != "ERR-0015" {
		t.Errorf("code = %s, want ERR-0015", incident.Code)
	}
	if incident.Source != "hook:claude" {
		t.Errorf("source = %s, want hook:claude", incident.Source)
	}
	if incident.Severity != faults.SeverityError {
		t.Errorf("severity = %s, want error", incident.Severity)
	}
	if incident.Summary != "claude hook: "+err.Error() {
		t.Errorf("summary = %q, want the hook name and the error", incident.Summary)
	}

	details, derr := faults.Details(incident.ID)
	if derr != nil {
		t.Fatalf("details: %v", derr)
	}
	if len(details) != 1 {
		t.Fatalf("logged %d occurrences, want 1", len(details))
	}
	fields := details[0].Fields
	if fields["hook_event"] != "PreToolUse" {
		t.Errorf("fields[hook_event] = %v, want PreToolUse", fields["hook_event"])
	}
	if fields["session_id"] != "sess-42" {
		t.Errorf("fields[session_id] = %v, want sess-42", fields["session_id"])
	}
	if fields["binary"] == nil {
		t.Error("fields[binary] is absent; which endless-go wrote this is the first " +
			"question a reader asks, and the whole of the ES-1055 diagnosis")
	}

	var rows int
	if qerr := db.QueryRow(`SELECT count(*) FROM errors`).Scan(&rows); qerr != nil {
		t.Fatalf("count rows: %v", qerr)
	}
	if rows != 1 {
		t.Errorf("errors table holds %d rows, want 1", rows)
	}
}

func TestRecordHookFault_RepeatsCollapseIntoOneIncident(t *testing.T) {
	bindFaultStore(t)

	// One stale binary fails identically on every event in every pane. Keyed by
	// session or pane this would raise an incident per session and bury the
	// shared cause; keyed by the error text it is ONE incident with a count.
	const failure = "touching session: table sessions has no column named process"
	for i, pane := range []string{"%1", "%2", "%3"} {
		t.Setenv("TMUX_PANE", pane)
		for j := 0; j < 7; j++ {
			recordHookFault("claude", taggedWithEvent(
				"PreToolUse", fmt.Sprintf("sess-%d", i), dbWriteFailed(errors.New(failure))))
		}
	}

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("raised %d incidents across 3 panes, want 1 — the fingerprint is "+
			"the error text precisely so one cause is one incident", len(incidents))
	}
	if incidents[0].Occurrences != 21 {
		t.Errorf("occurrences = %d, want 21", incidents[0].Occurrences)
	}
}

func TestRecordHookFault_DistinctCausesStayDistinct(t *testing.T) {
	bindFaultStore(t)

	recordHookFault("claude", dbWriteFailed(errors.New("touching session: no such column")))
	recordHookFault("claude", payloadUnreadable(errors.New("parsing payload: unexpected EOF")))

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 2 {
		t.Fatalf("raised %d incidents, want 2 — a malformed payload and a schema "+
			"drift have nothing in common but the hook they happened in", len(incidents))
	}

	codes := map[string]bool{}
	for _, incident := range incidents {
		codes[incident.Code] = true
	}
	if !codes["ERR-0015"] || !codes["ERR-0017"] {
		t.Errorf("codes = %v, want ERR-0015 and ERR-0017", codes)
	}
}

func TestRecordHookFault_LeavesTheExitCodeAlone(t *testing.T) {
	bindFaultStore(t)

	// The non-blocking contract is the reason this failure was invisible in the
	// first place, and recording must not have bought visibility by trading it
	// away: a hook that starts blocking tool calls on a database hiccup is a
	// considerably worse bug than the one being fixed.
	tests := []struct {
		event string
		want  int
	}{
		{"PreToolUse", exitBlocking},
		{"PostToolUse", exitBlocking},
		{"Stop", exitNonBlocking},
		{"SubagentStop", exitNonBlocking},
		{"", exitNonBlocking},
	}

	for _, tc := range tests {
		err := dbWriteFailed(errors.New("touching session: boom"))
		if tc.event != "" {
			err = taggedWithEvent(tc.event, "sess-1", err)
		}

		before := hookExitCode(err)
		recordHookFault("claude", err)
		after := hookExitCode(err)

		if before != tc.want || after != tc.want {
			t.Errorf("event %q: exit code %d before and %d after recording, want %d both",
				tc.event, before, after, tc.want)
		}
	}
}

func TestRecordHookFault_SurvivesAnUnboundStore(t *testing.T) {
	faults.Bind(nil, nil, nil)

	// A hook runs in processes that never wired a fault store. Recording is
	// best-effort by contract, so this must be a silent no-op rather than a
	// panic on the hot path of every tool call.
	recordHookFault("claude", dbWriteFailed(errors.New("touching session: boom")))
}

func TestRecordHookFault_RecordsWhenTheDatabaseIsTheThingThatFailed(t *testing.T) {
	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return nil, os.ErrPermission },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })

	// The case the whole fallback exists for: the hook failed BECAUSE the
	// database is unreachable, so the fault cannot be indexed in it. The report
	// must still survive.
	recordHookFault("claude", dbWriteFailed(errors.New("touching session: database is locked")))

	details, err := faults.Unindexed()
	if err != nil {
		t.Fatalf("Unindexed: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("recorded %d unindexed occurrences, want 1", len(details))
	}
	if details[0].Code != "ERR-0015" {
		t.Errorf("code = %s, want ERR-0015", details[0].Code)
	}
	if !details[0].Unindexed {
		t.Error("the occurrence is not marked unindexed")
	}
}
