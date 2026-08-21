package monitor

import (
	"testing"
)

// seedGateSession gives the gate tests a project and a session to hang rows on.
func seedGateSession(t *testing.T) int64 {
	t.Helper()
	db := dbConn
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')`,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	return insertSessionWithID(t, db, 900, 1)
}

// TestReportCheckpoint_PairKeepsOneOpenRow pins the invariant that makes the
// Stop gate's lookup unambiguous while still writing BOTH variants to the
// corpus.
//
// Both rows are corpus — the judge scores a minimization, not a presentation, so
// scoring the combined block would measure the experiment's packaging rather
// than either prompt. Only one can be the checkpoint, because "at most one open
// row per (session, kind)" is what the gate's ORDER BY ... LIMIT 1 rests on.
func TestReportCheckpoint_PairKeepsOneOpenRow(t *testing.T) {
	db := withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetReportCheckpoint(sessionID, ReportCheckpoint{
		Emitted:  "combined block",
		RawDraft: "the draft",
		TaskType: "todo",
		Context:  `[{"source":"task_plan","ok":true,"text":"plan"}]`,
		Variants: []ReportVariant{
			{Sanctioned: "reply A", Slot: "A", VariantHash: "aaa"},
			{Sanctioned: "reply B", Slot: "B", VariantHash: "bbb"},
		},
	}); err != nil {
		t.Fatalf("set paired checkpoint: %v", err)
	}

	var open int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_gates WHERE session_id=? AND kind_id=2 AND cleared_at IS NULL`,
		sessionID,
	).Scan(&open); err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 1 {
		t.Errorf("open rows = %d, want exactly 1", open)
	}

	var total int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_gates WHERE session_id=? AND kind_id=2`, sessionID,
	).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total != 2 {
		t.Errorf("corpus rows = %d, want 2 — both variants are corpus", total)
	}

	// A pair is ONE run against the turn's appeal budget. The doubling is the
	// experiment's cost, not a second bite at the appeal.
	var runs int
	if err := db.QueryRow(`SELECT report_runs FROM sessions WHERE id=?`, sessionID).Scan(&runs); err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if runs != 1 {
		t.Errorf("report_runs = %d, want 1", runs)
	}
}

// TestPendingReportCheckpoint_AcceptsAnyOfN pins what the gate is allowed to
// accept on a paired turn: the combined block the agent owes, plus either
// variant on its own.
func TestPendingReportCheckpoint_AcceptsAnyOfN(t *testing.T) {
	withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetReportCheckpoint(sessionID, ReportCheckpoint{
		Emitted: "combined block",
		Variants: []ReportVariant{
			{Sanctioned: "reply A", Slot: "A"},
			{Sanctioned: "reply B", Slot: "B"},
		},
	}); err != nil {
		t.Fatalf("set paired checkpoint: %v", err)
	}

	cp, _, found, err := PendingReportCheckpoint(sessionID)
	if err != nil || !found {
		t.Fatalf("pending checkpoint: found=%v err=%v", found, err)
	}
	if cp.Owed != "combined block" {
		t.Errorf("Owed = %q, want the emitted block", cp.Owed)
	}
	want := map[string]bool{"combined block": true, "reply A": true, "reply B": true}
	if len(cp.Accepted) != len(want) {
		t.Fatalf("Accepted = %v, want %d entries", cp.Accepted, len(want))
	}
	for _, got := range cp.Accepted {
		if !want[got] {
			t.Errorf("unexpected accepted text %q", got)
		}
	}
}

// An ORDINARY turn must be exactly what it was before E-1975: one row, one
// accepted text, and PendingRelayCheckpoint answering with it.
func TestPendingReportCheckpoint_SingleTurnIsUnchanged(t *testing.T) {
	withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetRelayCheckpoint(sessionID, "the only reply"); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	owed, _, found, err := PendingRelayCheckpoint(sessionID)
	if err != nil || !found {
		t.Fatalf("pending checkpoint: found=%v err=%v", found, err)
	}
	if owed != "the only reply" {
		t.Errorf("owed = %q", owed)
	}
	cp, _, _, _ := PendingReportCheckpoint(sessionID)
	if len(cp.Accepted) != 1 {
		t.Errorf("Accepted = %v, want exactly one text on an unpaired turn", cp.Accepted)
	}
}

// TestRecordReportLabels_WritesRowsAndMirrorsLegacy pins both halves of the
// free-form vocabulary's storage: a row per span, and the E-1953 columns kept
// answering as a projection of the first label.
func TestRecordReportLabels_WritesRowsAndMirrorsLegacy(t *testing.T) {
	db := withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetRelayCheckpoint(sessionID, "a reply"); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	found, err := RecordReportLabels(sessionID, []ReportLabel{
		{Token: "JARGON", Span: "load-bearing", Note: "stop using these"},
		{Token: "JARGON", Span: "at its core", Note: "stop using these"},
	})
	if err != nil || !found {
		t.Fatalf("record labels: found=%v err=%v", found, err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM report_labels`).Scan(&n); err != nil {
		t.Fatalf("count labels: %v", err)
	}
	if n != 2 {
		t.Errorf("label rows = %d, want one per span", n)
	}

	var label, labelText string
	if err := db.QueryRow(
		`SELECT label, label_text FROM session_gates WHERE session_id=? ORDER BY id DESC LIMIT 1`,
		sessionID,
	).Scan(&label, &labelText); err != nil {
		t.Fatalf("read legacy mirror: %v", err)
	}
	if label != "jargon" || labelText != "load-bearing" {
		t.Errorf("legacy mirror = (%q, %q), want the first label projected", label, labelText)
	}
}

// A session with nothing to annotate must say so rather than writing an orphan.
func TestRecordReportLabels_NoPriorReport(t *testing.T) {
	withTestDB(t)
	sessionID := seedGateSession(t)

	found, err := RecordReportLabels(sessionID, []ReportLabel{{Token: "CUT"}})
	if err != nil {
		t.Fatalf("record labels: %v", err)
	}
	if found {
		t.Error("a label was attached to a session with no reported turn")
	}
}

// TestRecordPick_MarksTheChosenSide pins the corpus's only real counterfactual.
func TestRecordPick_MarksTheChosenSide(t *testing.T) {
	db := withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetReportCheckpoint(sessionID, ReportCheckpoint{
		Emitted: "combined",
		Variants: []ReportVariant{
			{Sanctioned: "reply A", Slot: "A"},
			{Sanctioned: "reply B", Slot: "B"},
		},
	}); err != nil {
		t.Fatalf("set paired checkpoint: %v", err)
	}

	found, err := RecordPick(sessionID, "B")
	if err != nil || !found {
		t.Fatalf("record pick: found=%v err=%v", found, err)
	}
	rows, err := db.Query(
		`SELECT pair_slot, picked FROM session_gates WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		t.Fatalf("read pair: %v", err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var slot string
		var picked int
		if err := rows.Scan(&slot, &picked); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[slot] = picked
	}
	if got["A"] != 0 || got["B"] != 1 {
		t.Errorf("picked = %v, want only B marked", got)
	}
}

// A pick typed at an unpaired turn is not a pick. It is recorded as a label by
// the caller, and this must say plainly that there was no pair.
func TestRecordPick_UnpairedTurn(t *testing.T) {
	withTestDB(t)
	sessionID := seedGateSession(t)

	if err := SetRelayCheckpoint(sessionID, "a reply"); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	found, err := RecordPick(sessionID, "A")
	if err != nil {
		t.Fatalf("record pick: %v", err)
	}
	if found {
		t.Error("a pick was recorded against an unpaired turn")
	}
}

// TestNudgeABRate_LaddersBothWays pins the shape of the only control the user
// has over how often they see a pair.
//
// A ladder rather than arithmetic on a float, because the user is answering a
// yes/no question and a ladder makes each answer visible and reversible in the
// same number of steps.
func TestNudgeABRate_LaddersBothWays(t *testing.T) {
	withTestDB(t)

	start, err := ABRate()
	if err != nil {
		t.Fatalf("read rate: %v", err)
	}
	if start != DefaultABRate {
		t.Errorf("fresh rate = %v, want %v", start, DefaultABRate)
	}

	up, err := NudgeABRate(1)
	if err != nil {
		t.Fatalf("nudge up: %v", err)
	}
	if up <= start {
		t.Errorf("$MORE moved the rate to %v, which is not up from %v", up, start)
	}
	down, err := NudgeABRate(-1)
	if err != nil {
		t.Fatalf("nudge down: %v", err)
	}
	if down != start {
		t.Errorf("$LESS after $MORE = %v, want back to %v — the ladder must be reversible", down, start)
	}

	// The bottom rung is OFF. A user who never wants to see a pair again must be
	// able to say so with the same word they have been using.
	for i := 0; i < 10; i++ {
		if _, err := NudgeABRate(-1); err != nil {
			t.Fatalf("nudge down: %v", err)
		}
	}
	bottom, _ := ABRate()
	if bottom != 0 {
		t.Errorf("bottom rung = %v, want 0 (pairing off)", bottom)
	}

	// The top rung is not 1.0: pairing every turn stops being an experiment.
	for i := 0; i < 20; i++ {
		if _, err := NudgeABRate(1); err != nil {
			t.Fatalf("nudge up: %v", err)
		}
	}
	top, _ := ABRate()
	if top >= 1.0 {
		t.Errorf("top rung = %v, want strictly below 1.0", top)
	}
}
