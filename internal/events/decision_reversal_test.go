// Tests for the decision status reversals (E-1864): decision.unaccepted and
// decision.unrejected, which take a settled decision back to 'proposed'.
//
// The properties under test:
//
//   - each reversal is guarded on the status it undoes, so aiming one at the
//     other terminal status fails loudly instead of silently performing the
//     wrong reversal;
//   - unreject clears rejection_reason (a proposed decision has not been
//     rejected, so a stale reason would render on `decision show`);
//   - unaccept leaves rejection_reason alone — it has nothing to clear;
//   - both round-trip, so a reversed decision can be settled again.
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

// seedDecision inserts a decision in the given status, with an optional
// rejection_reason (passed as nil for anything but a rejected decision).
func seedDecision(t *testing.T, db *sql.DB, id int64, status string, reason any) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO decisions
		   (id, project_id, title, description, status, rejection_reason,
		    created_at, updated_at)
		 VALUES (?, 1, 'a decision', '', ?, ?,
		         '2026-08-04T00:00:00', '2026-08-04T00:00:00')`,
		id, status, reason,
	); err != nil {
		t.Fatalf("seed decision %d: %v", id, err)
	}
}

// newDecisionEvent builds a minimal valid decision event with an empty
// payload (both reversals take DecisionUnacceptedPayload/UnrejectedPayload,
// which are empty structs).
func newDecisionEvent(t *testing.T, kind events.Kind, id int64) *events.Event {
	t.Helper()
	payload, err := json.Marshal(struct{}{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &events.Event{
		V:       events.Version,
		TS:      "5WYM00000001",
		Kind:    kind,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityDecision, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

// decisionState reads back the two columns the reversals touch.
func decisionState(t *testing.T, db *sql.DB, id int64) (status string, reason sql.NullString) {
	t.Helper()
	err := db.QueryRow(
		"SELECT status, rejection_reason FROM decisions WHERE id = ?", id,
	).Scan(&status, &reason)
	if err != nil {
		t.Fatalf("read decision %d: %v", id, err)
	}
	return status, reason
}

func TestDecisionUnaccepted_AcceptedGoesBackToProposed(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	if _, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnaccepted, 1), nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	status, _ := decisionState(t, db, 1)
	if status != "proposed" {
		t.Errorf("status = %q, want proposed", status)
	}
}

// A rejected decision must NOT be unaccept-able: the whole point of naming
// the status in the verb is that the wrong one errors.
func TestDecisionUnaccepted_RejectedIsRefused(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "rejected", "superseded")

	_, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnaccepted, 1), nil)
	if err == nil {
		t.Fatal("Execute succeeded; want an error naming the actual status")
	}
	if !strings.Contains(err.Error(), `"rejected"`) {
		t.Errorf("error = %v, want it to name the actual status (rejected)", err)
	}

	status, reason := decisionState(t, db, 1)
	if status != "rejected" {
		t.Errorf("status = %q, want the row left untouched at rejected", status)
	}
	if reason.String != "superseded" {
		t.Errorf("rejection_reason = %q, want it left untouched", reason.String)
	}
}

func TestDecisionUnaccepted_ProposedIsRefused(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "proposed", nil)

	_, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnaccepted, 1), nil)
	if err == nil {
		t.Fatal("Execute succeeded; want an error (nothing to unaccept)")
	}
	if !strings.Contains(err.Error(), `"proposed"`) {
		t.Errorf("error = %v, want it to name the actual status (proposed)", err)
	}
}

func TestDecisionUnaccepted_MissingDecisionIsRefused(t *testing.T) {
	withExecutorDB(t)

	_, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnaccepted, 404), nil)
	if err == nil {
		t.Fatal("Execute succeeded; want a not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want a not-found error", err)
	}
}

func TestDecisionUnrejected_RejectedGoesBackToProposedAndClearsReason(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "rejected", "superseded by E-1863")

	if _, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnrejected, 1), nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	status, reason := decisionState(t, db, 1)
	if status != "proposed" {
		t.Errorf("status = %q, want proposed", status)
	}
	if reason.Valid {
		t.Errorf("rejection_reason = %q, want NULL — a proposed decision has not been rejected",
			reason.String)
	}
}

func TestDecisionUnrejected_AcceptedIsRefused(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "accepted", nil)

	_, err := events.Execute(newDecisionEvent(t, events.KindDecisionUnrejected, 1), nil)
	if err == nil {
		t.Fatal("Execute succeeded; want an error naming the actual status")
	}
	if !strings.Contains(err.Error(), `"accepted"`) {
		t.Errorf("error = %v, want it to name the actual status (accepted)", err)
	}

	status, _ := decisionState(t, db, 1)
	if status != "accepted" {
		t.Errorf("status = %q, want the row left untouched at accepted", status)
	}
}

// Reversing must leave the decision genuinely re-settleable, not in some
// half-state that the forward verbs then refuse.
func TestDecisionReversal_RoundTrips(t *testing.T) {
	db := withExecutorDB(t)
	seedDecision(t, db, 1, "proposed", nil)

	steps := []struct {
		kind    events.Kind
		payload any
		want    string
	}{
		{events.KindDecisionAccepted, events.DecisionAcceptedPayload{}, "accepted"},
		{events.KindDecisionUnaccepted, events.DecisionUnacceptedPayload{}, "proposed"},
		{events.KindDecisionRejected, events.DecisionRejectedPayload{Reason: "not now"}, "rejected"},
		{events.KindDecisionUnrejected, events.DecisionUnrejectedPayload{}, "proposed"},
		{events.KindDecisionAccepted, events.DecisionAcceptedPayload{}, "accepted"},
	}

	for _, step := range steps {
		payload, err := json.Marshal(step.payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", step.kind, err)
		}
		evt := newDecisionEvent(t, step.kind, 1)
		evt.Payload = payload
		if _, err := events.Execute(evt, nil); err != nil {
			t.Fatalf("Execute %s: %v", step.kind, err)
		}
		status, _ := decisionState(t, db, 1)
		if status != step.want {
			t.Fatalf("after %s: status = %q, want %q", step.kind, status, step.want)
		}
	}
}
