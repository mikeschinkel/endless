// Tests for the decision END states (E-1920): decision.superseded,
// decision.obsoleted, and the single reversal decision.reinstated.
//
// The properties under test:
//
//   - both end states are reachable ONLY from 'accepted'. That is the whole
//     of their meaning — only an accepted decision governs, so only an
//     accepted one can stop governing — and it is what makes one shared
//     reversal unambiguous;
//   - obsoleted stores its reason, and refuses an empty one: the reason is
//     the only thing distinguishing a rule retired deliberately from one that
//     quietly stopped being mentioned;
//   - superseded does NOT store the successor on the row (the `supersedes`
//     relation is the record) but refuses a payload that names none, so a
//     ledger entry can never say "superseded by nothing";
//   - reinstate clears obsolete_reason, mirroring unreject clearing
//     rejection_reason;
//   - the whole cycle round-trips, so a reinstated decision can be retired
//     again.
//
// Concurrency: withExecutorDB rebinds package-level monitor state; no
// t.Parallel() in this file.
package events_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// endStateEvent builds a decision event carrying an arbitrary payload. The
// end-state kinds are not all empty-payload like the E-1864 reversals, so
// newDecisionEvent's hardcoded `{}` will not do for supersede/obsolete.
func endStateEvent(t *testing.T, kind events.Kind, id int64, payload any) *events.Event {
	t.Helper()
	evt := newDecisionEvent(t, kind, id)
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind, err)
	}
	evt.Payload = body
	return evt
}

// endState reads back the two columns the end states touch.
func endState(t *testing.T, db *sql.DB, id int64) (status string, obsoleteReason sql.NullString) {
	t.Helper()
	err := db.QueryRow(
		"SELECT status, obsolete_reason FROM decisions WHERE id = ?", id,
	).Scan(&status, &obsoleteReason)
	if err != nil {
		t.Fatalf("read decision %d: %v", id, err)
	}
	return status, obsoleteReason
}

func TestDecisionSuperseded_AcceptedIsRetired(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	evt := endStateEvent(t, events.KindDecisionSuperseded, 1,
		events.DecisionSupersededPayload{BySupersedingID: 2})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	status, reason := endState(t, db, 1)
	if status != "superseded" {
		t.Errorf("status = %q, want superseded", status)
	}
	// The successor lives in decision_relations, not on the row. A column
	// here would be a second copy of the same fact, free to disagree.
	if reason.Valid {
		t.Errorf("obsolete_reason = %q, want NULL — supersede has no reason to store",
			reason.String)
	}
}

// The forward guard: every status other than 'accepted' is refused, and the
// error names the one it found so the caller is not left guessing which of
// the several wrong statuses it hit.
func TestDecisionSuperseded_OnlyAcceptedIsAllowed(t *testing.T) {
	for _, status := range []string{"proposed", "rejected", "superseded", "obsolete"} {
		t.Run(status, func(t *testing.T) {
			db := withExecutorDB(t)
			seedDecision(t, db, 1, status, nil)

			evt := endStateEvent(t, events.KindDecisionSuperseded, 1,
				events.DecisionSupersededPayload{BySupersedingID: 2})
			_, err := events.Execute(evt, nil)
			if err == nil {
				t.Fatalf("Execute succeeded on %q; want a refusal", status)
			}
			if !strings.Contains(err.Error(), `"`+status+`"`) {
				t.Errorf("error = %v, want it to name the actual status (%s)", err, status)
			}

			got, _ := endState(t, db, 1)
			if got != status {
				t.Errorf("status = %q, want the row left untouched at %q", got, status)
			}
		})
	}
}

// "Superseded" with no successor is the dead end E-1920 exists to close, so
// it must be unrepresentable in the ledger, not merely discouraged at the CLI.
func TestDecisionSuperseded_RequiresASuccessor(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	evt := endStateEvent(t, events.KindDecisionSuperseded, 1,
		events.DecisionSupersededPayload{})
	_, err := events.Execute(evt, nil)
	if err == nil {
		t.Fatal("Execute succeeded with no by_superseding_id; want a refusal")
	}
	if !strings.Contains(err.Error(), "by_superseding_id") {
		t.Errorf("error = %v, want it to name the missing field", err)
	}

	status, _ := endState(t, db, 1)
	if status != "accepted" {
		t.Errorf("status = %q, want the row left untouched at accepted", status)
	}
}

func TestDecisionSuperseded_SelfSupersessionIsRefused(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	evt := endStateEvent(t, events.KindDecisionSuperseded, 1,
		events.DecisionSupersededPayload{BySupersedingID: 1})
	if _, err := events.Execute(evt, nil); err == nil {
		t.Fatal("Execute succeeded superseding a decision by itself; want a refusal")
	}

	status, _ := endState(t, db, 1)
	if status != "accepted" {
		t.Errorf("status = %q, want the row left untouched at accepted", status)
	}
}

func TestDecisionObsoleted_AcceptedIsRetiredWithReason(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	evt := endStateEvent(t, events.KindDecisionObsoleted, 1,
		events.DecisionObsoletedPayload{Reason: "the subsystem it governed was deleted"})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	status, reason := endState(t, db, 1)
	if status != "obsolete" {
		t.Errorf("status = %q, want obsolete", status)
	}
	if reason.String != "the subsystem it governed was deleted" {
		t.Errorf("obsolete_reason = %q, want the stored reason", reason.String)
	}
}

func TestDecisionObsoleted_OnlyAcceptedIsAllowed(t *testing.T) {
	for _, status := range []string{"proposed", "rejected", "superseded", "obsolete"} {
		t.Run(status, func(t *testing.T) {
			db := withExecutorDB(t)
			seedDecision(t, db, 1, status, nil)

			evt := endStateEvent(t, events.KindDecisionObsoleted, 1,
				events.DecisionObsoletedPayload{Reason: "gone"})
			_, err := events.Execute(evt, nil)
			if err == nil {
				t.Fatalf("Execute succeeded on %q; want a refusal", status)
			}
			if !strings.Contains(err.Error(), `"`+status+`"`) {
				t.Errorf("error = %v, want it to name the actual status (%s)", err, status)
			}
		})
	}
}

// An unexplained obsolete reproduces the gap this feature closes, so the
// empty reason is refused at the executor, not just at the CLI.
func TestDecisionObsoleted_RequiresAReason(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	evt := endStateEvent(t, events.KindDecisionObsoleted, 1,
		events.DecisionObsoletedPayload{})
	_, err := events.Execute(evt, nil)
	if err == nil {
		t.Fatal("Execute succeeded with an empty reason; want a refusal")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error = %v, want it to name the missing reason", err)
	}

	status, _ := endState(t, db, 1)
	if status != "accepted" {
		t.Errorf("status = %q, want the row left untouched at accepted", status)
	}
}

// One reversal, two end states — and it lands on `accepted`, not `proposed`.
// Reinstating is "put it back in force", not "put it back on the table".
func TestDecisionReinstated_BothEndStatesReturnToAccepted(t *testing.T) {
	for _, status := range []string{"superseded", "obsolete"} {
		t.Run(status, func(t *testing.T) {
			db := withExecutorDB(t)
			seedDecision(t, db, 1, status, nil)

			evt := endStateEvent(t, events.KindDecisionReinstated, 1,
				events.DecisionReinstatedPayload{})
			if _, err := events.Execute(evt, nil); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			got, _ := endState(t, db, 1)
			if got != "accepted" {
				t.Errorf("status = %q, want accepted", got)
			}
		})
	}
}

// Mirrors unreject clearing rejection_reason: a decision back in force has
// not been obsoleted, and a stale reason would render on `decision show`.
func TestDecisionReinstated_ClearsObsoleteReason(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	obsolete := endStateEvent(t, events.KindDecisionObsoleted, 1,
		events.DecisionObsoletedPayload{Reason: "the flag it governed was removed"})
	if _, err := events.Execute(obsolete, nil); err != nil {
		t.Fatalf("Execute obsolete: %v", err)
	}

	reinstate := endStateEvent(t, events.KindDecisionReinstated, 1,
		events.DecisionReinstatedPayload{})
	if _, err := events.Execute(reinstate, nil); err != nil {
		t.Fatalf("Execute reinstate: %v", err)
	}

	status, reason := endState(t, db, 1)
	if status != "accepted" {
		t.Errorf("status = %q, want accepted", status)
	}
	if reason.Valid {
		t.Errorf("obsolete_reason = %q, want NULL — a decision back in force has not been obsoleted",
			reason.String)
	}
}

func TestDecisionReinstated_LiveStatusesAreRefused(t *testing.T) {
	for _, status := range []string{"proposed", "accepted", "rejected"} {
		t.Run(status, func(t *testing.T) {
			db := withExecutorDB(t)
			seedDecision(t, db, 1, status, nil)

			evt := endStateEvent(t, events.KindDecisionReinstated, 1,
				events.DecisionReinstatedPayload{})
			_, err := events.Execute(evt, nil)
			if err == nil {
				t.Fatalf("Execute succeeded on %q; want a refusal", status)
			}
			if !strings.Contains(err.Error(), `"`+status+`"`) {
				t.Errorf("error = %v, want it to name the actual status (%s)", err, status)
			}
		})
	}
}

// Retiring must leave the decision genuinely re-retireable, not in a
// half-state the forward verbs then refuse.
func TestDecisionEndState_RoundTrips(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "proposed", nil)

	steps := []struct {
		kind    events.Kind
		payload any
		want    string
	}{
		{events.KindDecisionAccepted, events.DecisionAcceptedPayload{}, "accepted"},
		{events.KindDecisionSuperseded, events.DecisionSupersededPayload{BySupersedingID: 2}, "superseded"},
		{events.KindDecisionReinstated, events.DecisionReinstatedPayload{}, "accepted"},
		{events.KindDecisionObsoleted, events.DecisionObsoletedPayload{Reason: "gone"}, "obsolete"},
		{events.KindDecisionReinstated, events.DecisionReinstatedPayload{}, "accepted"},
		{events.KindDecisionUnaccepted, events.DecisionUnacceptedPayload{}, "proposed"},
	}

	for _, step := range steps {
		if _, err := events.Execute(endStateEvent(t, step.kind, 1, step.payload), nil); err != nil {
			t.Fatalf("Execute %s: %v", step.kind, err)
		}
		got, _ := endState(t, db, 1)
		if got != step.want {
			t.Fatalf("after %s: status = %q, want %q", step.kind, got, step.want)
		}
	}
}
