package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/planitem"
)

// planHarness drives the live path the way `event emit` does — prepare under
// the lock, then dispatch — and keeps every event so a test can replay them.
type planHarness struct {
	t      *testing.T
	db     *sql.DB
	events []*Event
	clock  time.Time
}

func newPlanHarness(t *testing.T) *planHarness {
	return &planHarness{t: t, db: newLandingTestDB(t), clock: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (h *planHarness) setStatus(status string) {
	h.t.Helper()
	if _, err := h.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, questionTask); err != nil {
		h.t.Fatalf("set status: %v", err)
	}
}

func (h *planHarness) event(kind Kind, payload any) *Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	// Every event in the same second: staleness must not depend on the clock.
	return &Event{
		V: 1, TS: kairosTS(h.clock), Kind: kind, Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(questionTask, 10)},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host"},
		Payload: raw,
	}
}

func (h *planHarness) run(evt *Event) {
	h.t.Helper()
	if _, err := dispatch(h.db, evt, nil); err != nil {
		h.t.Fatalf("dispatch %s: %v", evt.Kind, err)
	}
	h.events = append(h.events, evt)
}

func (h *planHarness) items(ops ...PlanItemOp) (TaskPlanItemsChangedPayload, error) {
	p := TaskPlanItemsChangedPayload{Ops: ops}
	if err := PreparePlanItemsChanged(h.db, questionTask, &p); err != nil {
		return p, err
	}
	h.run(h.event(KindTaskPlanItemsChanged, p))
	return p, nil
}

func (h *planHarness) mustItems(ops ...PlanItemOp) TaskPlanItemsChangedPayload {
	h.t.Helper()
	p, err := h.items(ops...)
	if err != nil {
		h.t.Fatalf("plan items: %v", err)
	}
	return p
}

func (h *planHarness) baseline() {
	h.t.Helper()
	if err := PreparePlanBaselined(h.db, questionTask); err != nil {
		h.t.Fatalf("baseline: %v", err)
	}
	h.run(h.event(KindTaskPlanBaselined, TaskPlanBaselinedPayload{}))
}

func (h *planHarness) reconcile(entries ...ReconciliationEntry) (TaskReconciledPayload, error) {
	p := TaskReconciledPayload{Fingerprint: "fp", Entries: entries}
	if err := PrepareReconciled(h.db, questionTask, &p); err != nil {
		return p, err
	}
	h.run(h.event(KindTaskReconciled, p))
	return p, nil
}

func (h *planHarness) mustReconcile(entries ...ReconciliationEntry) TaskReconciledPayload {
	h.t.Helper()
	p, err := h.reconcile(entries...)
	if err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	return p
}

func (h *planHarness) answer(qid int64) {
	h.t.Helper()
	evt := resolvedEvent(h.t, qid, TaskQuestionResolvedPayload{Status: "answered", Answer: "keep it", AnsweredBy: "user"})
	h.run(evt)
}

func add(kind, statement string) PlanItemOp {
	return PlanItemOp{Op: PlanOpAdd, Kind: &kind, Statement: &statement}
}

func str(s string) *string { return &s }

func entry(seq int64, verdict, note string) ReconciliationEntry {
	return ReconciliationEntry{ItemSeq: seq, Verdict: verdict, Note: note}
}

func origins(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT seq, origin, state FROM task_plan_items ORDER BY seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int64
		var origin, state string
		if err := rows.Scan(&seq, &origin, &state); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("P%d:%s:%s", seq, origin, state))
	}
	return strings.Join(out, " ")
}

func TestPlanItems_FirstItemsArePlannedEvenWhenUnderway(t *testing.T) {
	h := newPlanHarness(t) // the seeded task is underway
	h.mustItems(add("deliverable", "a"), add("exclusion", "b"))
	h.mustItems(add("deliverable", "c"))
	if got, want := origins(t, h.db), "P1:planned:active P2:planned:active P3:grown:active"; got != want {
		t.Errorf("origins = %q, want %q", got, want)
	}
}

func TestPlanItems_AddsBeforeClaimArePlanned(t *testing.T) {
	h := newPlanHarness(t)
	h.setStatus("ready")
	h.mustItems(add("deliverable", "a"))
	h.mustItems(add("deliverable", "b"))
	if got, want := origins(t, h.db), "P1:planned:active P2:planned:active"; got != want {
		t.Errorf("origins = %q, want %q", got, want)
	}
}

func TestPlanItems_RefusesBadInput(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"))
	cases := map[string]PlanItemOp{
		"unknown kind":      add("wish", "x"),
		"empty statement":   add("deliverable", " "),
		"two lines":         add("deliverable", "x\ny"),
		"unknown item":      {Op: PlanOpWithdraw, Seq: 9},
		"edit changes none": {Op: PlanOpEdit, Seq: 1},
		"caller numbered":   {Op: PlanOpAdd, Seq: 4, Kind: str("deliverable"), Statement: str("x")},
		"unknown op":        {Op: "rename", Seq: 1},
	}
	for name, op := range cases {
		if _, err := h.items(op); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlanItems_FrozenFromUnverifiedOn(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"))
	for _, st := range []string{"unverified", "assumed", "confirmed"} {
		h.setStatus(st)
		if _, err := h.items(add("deliverable", "b")); err == nil {
			t.Errorf("%s: items not frozen", st)
		}
		if _, err := h.reconcile(entry(1, "as_planned", "")); err == nil {
			t.Errorf("%s: reconciliation not frozen", st)
		}
	}
	h.setStatus("revisit")
	h.mustItems(add("deliverable", "b"))
}

func TestPlanItems_EditAndWithdrawApprovedItemWhileUnderwayIsAllowed(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"), add("deliverable", "b"))
	h.baseline()
	h.mustItems(PlanItemOp{Op: PlanOpEdit, Seq: 1, Statement: str("a2")}, PlanItemOp{Op: PlanOpWithdraw, Seq: 2})
	var stmt, approved string
	if err := h.db.QueryRow(`SELECT statement, approved_statement FROM task_plan_items WHERE seq = 1`).Scan(&stmt, &approved); err != nil {
		t.Fatal(err)
	}
	if stmt != "a2" || approved != "a" {
		t.Errorf("statement %q approved %q, want a2 / a", stmt, approved)
	}
}

func TestPlanBaselined_RefusesGatedTaskWithoutDeliverable(t *testing.T) {
	h := newPlanHarness(t)
	if err := PreparePlanBaselined(h.db, questionTask); err == nil {
		t.Error("baseline of a todo with no items accepted")
	}
	h.mustItems(add("exclusion", "no ledger change"))
	if err := PreparePlanBaselined(h.db, questionTask); err == nil {
		t.Error("baseline of a todo with only an exclusion accepted")
	}
}

func TestReconcile_RefusesIncompleteOrMismatchedEntries(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"), add("exclusion", "b"))
	cases := map[string][]ReconciliationEntry{
		"missing entry":         {entry(1, "as_planned", "")},
		"unknown item":          {entry(1, "as_planned", ""), entry(2, "kept", ""), entry(3, "as_planned", "")},
		"duplicate":             {entry(1, "as_planned", ""), entry(1, "as_planned", ""), entry(2, "kept", "")},
		"verdict mismatch":      {entry(1, "kept", ""), entry(2, "kept", "")},
		"discrepancy, no note":  {entry(1, "not_done", ""), entry(2, "kept", "")},
		"holds without earlier": {{ItemSeq: 1, Verdict: "not_done", Note: "n", AnswerHolds: true, Reason: "r"}, entry(2, "kept", "")},
	}
	for name, es := range cases {
		if _, err := h.reconcile(es...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	h.mustItems(PlanItemOp{Op: PlanOpWithdraw, Seq: 2})
	if _, err := h.reconcile(entry(1, "as_planned", ""), entry(2, "kept", "")); err == nil {
		t.Error("entry for a withdrawn item accepted")
	}
}

func TestReconcile_FilesKindTaggedQuestionsAndReusesEarlierAnswers(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"), add("deliverable", "b"), add("exclusion", "c"))
	h.baseline()
	h.mustItems(add("deliverable", "grown d"))

	p := h.mustReconcile(entry(1, "differently", "other route"), entry(2, "as_planned", ""),
		entry(3, "not_kept", "touched it"), entry(4, "as_planned", ""))
	if len(p.Questions) != 3 {
		t.Fatalf("questions = %+v, want 3", p.Questions)
	}
	kinds := map[string]bool{}
	for _, q := range p.Questions {
		kinds[q.Kind] = true
	}
	for _, k := range []string{planitem.QuestionDifferent, planitem.QuestionExcluded, planitem.QuestionMore} {
		if !kinds[k] {
			t.Errorf("no %s question in %+v", k, p.Questions)
		}
	}
	q1 := p.Entries[0].QuestionID

	// Still open: the same verdict carries the open question; nothing new.
	p2 := h.mustReconcile(entry(1, "differently", "other route"), entry(2, "as_planned", ""),
		entry(3, "not_kept", "touched it"), entry(4, "as_planned", ""))
	if len(p2.Questions) != 0 || p2.Entries[0].QuestionID != q1 {
		t.Fatalf("open question not carried: %+v", p2)
	}

	for _, q := range p.Questions {
		h.answer(q.ID)
	}
	// Answered: the same verdict must say holds or reask.
	if _, err := h.reconcile(entry(1, "differently", "other route"), entry(2, "as_planned", ""),
		entry(3, "not_kept", "x"), entry(4, "as_planned", "")); err == nil {
		t.Fatal("silent reuse of an earlier answer accepted")
	}
	holds := func(seq int64, verdict string) ReconciliationEntry {
		return ReconciliationEntry{ItemSeq: seq, Verdict: verdict, Note: "n", AnswerHolds: true, Reason: "nothing it covers changed"}
	}
	p3 := h.mustReconcile(holds(1, "differently"), entry(2, "not_done", "dropped"),
		ReconciliationEntry{ItemSeq: 3, Verdict: "not_kept", Note: "n", Reask: true, Reason: "touched more of it"},
		holds(4, "as_planned"))
	if p3.Entries[0].QuestionID != q1 || p3.Entries[0].HoldsReason == "" {
		t.Errorf("holds did not keep EQ-%d: %+v", q1, p3.Entries[0])
	}
	if len(p3.Questions) != 2 {
		t.Fatalf("want a new question for the changed verdict and the reask, got %+v", p3.Questions)
	}
	if !strings.Contains(p3.Questions[1].Question, "Earlier answer") {
		t.Errorf("reask question does not show the earlier answer: %q", p3.Questions[1].Question)
	}
}

func TestReconcile_ApprovedGrownItemIsNotADiscrepancy(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"))
	h.baseline()
	h.mustItems(add("deliverable", "grown"))
	h.baseline() // the user re-approves
	p := h.mustReconcile(entry(1, "as_planned", ""), entry(2, "as_planned", ""))
	if len(p.Questions) != 0 {
		t.Errorf("questions %+v, want none", p.Questions)
	}
}

// TestPlanItems_ReplayMatchesLive runs one sequence live, replays its events into
// a fresh database, and compares every table the kinds write.
func TestPlanItems_ReplayMatchesLive(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"), add("exclusion", "b"))
	h.baseline()
	h.mustItems(add("deliverable", "c"), PlanItemOp{Op: PlanOpMove, Seq: 2, SortOrder: 9},
		PlanItemOp{Op: PlanOpEdit, Seq: 1, Verify: str("go test ./...")})
	p := h.mustReconcile(entry(1, "differently", "n"), entry(2, "kept", ""), entry(3, "as_planned", ""))
	h.answer(p.Questions[0].ID)
	h.mustReconcile(ReconciliationEntry{ItemSeq: 1, Verdict: "differently", Note: "n", AnswerHolds: true, Reason: "r"},
		entry(2, "kept", ""), entry(3, "as_planned", ""))

	replay := newLandingTestDB(t)
	for _, evt := range h.events {
		if err := replayEvent(replay, evt, &ProjectResult{}); err != nil {
			t.Fatalf("replay %s: %v", evt.Kind, err)
		}
	}
	for _, q := range []string{
		`SELECT * FROM task_plan_items ORDER BY seq`,
		`SELECT * FROM task_reconciliations ORDER BY seq`,
		`SELECT * FROM task_reconciliation_entries ORDER BY reconciliation_id, item_seq`,
		`SELECT * FROM task_questions ORDER BY id`,
	} {
		if live, rep := dumpRows(t, h.db, q), dumpRows(t, replay, q); live != rep {
			t.Errorf("%s\nlive:\n%s\nreplay:\n%s", q, live, rep)
		}
	}
}

func dumpRows(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&b, vals...)
	}
	return b.String()
}

// A todo or bugfix reaches a status that reports its work only through the
// reconciliation gate, so it cannot be created at one; other types can.
func TestTaskCreated_RefusesAnItemizedTypeAtAReportedStatus(t *testing.T) {
	h := newPlanHarness(t)
	created := func(id int64, typ, status string) error {
		evt := h.event(KindTaskCreated, TaskCreatedPayload{Title: "Add a thing", Type: typ, Status: status, Phase: "now"})
		evt.Entity.ID = strconv.FormatInt(id, 10)
		_, err := dispatch(h.db, evt, nil)
		return err
	}
	for _, st := range planitem.ReportedStatuses {
		if err := created(9001, "todo", st); err == nil || !strings.Contains(err.Error(), "cannot be created") {
			t.Errorf("todo at %s: got %v, want a refusal", st, err)
		}
	}
	if err := created(9002, "bugfix", "unverified"); err == nil {
		t.Error("bugfix at unverified was created")
	}
	if err := created(9003, "todo", "underway"); err != nil {
		t.Errorf("todo at underway: %v", err)
	}
	if err := created(9004, "research", "completed"); err != nil {
		t.Errorf("research at completed: %v", err)
	}
}
