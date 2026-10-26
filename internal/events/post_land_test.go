package events

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/reconcilegate"
)

// Post-land items and their verdicts (E-2444).

func yes() *bool { t := true; return &t }

func (h *planHarness) postLand(entries ...ReconciliationEntry) (TaskPostLandReconciledPayload, error) {
	p := TaskPostLandReconciledPayload{Fingerprint: reconcilegate.EmptyFingerprint, Entries: entries}
	if err := PreparePostLandReconciled(h.db, questionTask, &p); err != nil {
		return p, err
	}
	h.run(h.event(KindTaskPostLandReconciled, p))
	return p, nil
}

func (h *planHarness) mustPostLand(entries ...ReconciliationEntry) TaskPostLandReconciledPayload {
	h.t.Helper()
	p, err := h.postLand(entries...)
	if err != nil {
		h.t.Fatalf("post-land reconcile: %v", err)
	}
	return p
}

// landedWithDeferred builds a task with P1 done and P2 post-land, reconciled
// with P2 deferred, and moves it to `landed`.
func landedWithDeferred(t *testing.T) *planHarness {
	h := newPlanHarness(t)
	a := add("deliverable", "the code")
	b := add("deliverable", "a row written after install")
	b.PostLand = yes()
	h.mustItems(a, b)
	h.baseline()
	h.mustReconcile(entry(1, "as_planned", ""), entry(2, "deferred", ""))
	h.setStatus("landed")
	return h
}

func TestPostLand_MarkFitsOnlyDeliverables(t *testing.T) {
	h := newPlanHarness(t)
	ex := add("exclusion", "does not touch the ledger")
	ex.PostLand = yes()
	if _, err := h.items(ex); err == nil || !strings.Contains(err.Error(), "only a deliverable") {
		t.Fatalf("post-land exclusion: err = %v", err)
	}
	h.mustItems(add("exclusion", "x"))
	if _, err := h.items(PlanItemOp{Op: PlanOpEdit, Seq: 1, PostLand: yes()}); err == nil {
		t.Fatal("marking an exclusion post-land was accepted")
	}
}

func TestPostLand_DeferredOnlyOnPostLandItems(t *testing.T) {
	h := newPlanHarness(t)
	h.mustItems(add("deliverable", "a"))
	h.baseline()
	if _, err := h.reconcile(entry(1, "deferred", "")); err == nil || !strings.Contains(err.Error(), "post-land") {
		t.Fatalf("deferred on an unmarked item: err = %v", err)
	}
	// Marking changes the item, so it differs from what was approved until the
	// mark is approved with it.
	h.mustItems(PlanItemOp{Op: PlanOpEdit, Seq: 1, PostLand: yes(), ApprovePostLand: true})
	p := h.mustReconcile(entry(1, "deferred", ""))
	if len(p.Questions) != 0 {
		t.Errorf("deferred filed questions %+v; it is not a discrepancy", p.Questions)
	}
	var mark, approved int
	if err := h.db.QueryRow(`SELECT post_land, approved_post_land FROM task_plan_items WHERE seq = 1`).Scan(&mark, &approved); err != nil {
		t.Fatal(err)
	}
	if mark != 1 || approved != 1 {
		t.Errorf("post_land = %d, approved_post_land = %d; want 1, 1", mark, approved)
	}
}

func TestPostLand_RefusesWhenNotLandedOrBranchHasCode(t *testing.T) {
	h := newPlanHarness(t)
	b := add("deliverable", "after install")
	b.PostLand = yes()
	h.mustItems(b)
	h.baseline()
	h.mustReconcile(entry(1, "deferred", ""))
	if _, err := h.postLand(entry(1, "as_planned", "")); err == nil || !strings.Contains(err.Error(), "not landed") {
		t.Fatalf("post-land reconcile on an underway task: err = %v", err)
	}
	h.setStatus("landed")
	p := TaskPostLandReconciledPayload{Fingerprint: "abc123", Entries: []ReconciliationEntry{entry(1, "as_planned", "")}}
	if err := PreparePostLandReconciled(h.db, questionTask, &p); err == nil || !strings.Contains(err.Error(), "--cleans-up") {
		t.Fatalf("post-land reconcile with code on the branch: err = %v", err)
	}
}

func TestPostLand_TakesPostLandItemsOnly(t *testing.T) {
	h := landedWithDeferred(t)
	if _, err := h.postLand(entry(1, "as_planned", "")); err == nil || !strings.Contains(err.Error(), "not a post-land item") {
		t.Fatalf("entry for an ordinary item: err = %v", err)
	}
	if _, err := h.postLand(); err == nil {
		t.Fatal("an empty post-land reconcile was accepted")
	}
}

func TestPostLand_WritesOnlyItsOwnRowsAndComparesWithThePreviousOne(t *testing.T) {
	h := landedWithDeferred(t)
	before := dumpRows(t, h.db, `SELECT * FROM task_reconciliation_entries ORDER BY item_seq`)

	// A failed attempt files a question.
	first := h.mustPostLand(entry(2, "not_done", "the migration had not run"))
	if first.Attempt != 1 || len(first.Questions) != 1 || first.Questions[0].Kind != planitem.QuestionLess {
		t.Fatalf("first attempt = %+v", first)
	}
	// The same verdict while the question is open carries it over.
	again := h.mustPostLand(entry(2, "not_done", "still not run"))
	if len(again.Questions) != 0 || again.Entries[0].QuestionID != first.Questions[0].ID {
		t.Fatalf("second attempt did not carry EQ-%d over: %+v", first.Questions[0].ID, again)
	}
	// Once answered, the same verdict must say whether the answer holds.
	h.answer(first.Questions[0].ID)
	if _, err := h.postLand(entry(2, "not_done", "n")); err == nil || !strings.Contains(err.Error(), "answer_holds") {
		t.Fatalf("settled question without answer_holds/reask: err = %v", err)
	}
	h.mustPostLand(entry(2, "as_planned", ""))

	if after := dumpRows(t, h.db, `SELECT * FROM task_reconciliation_entries ORDER BY item_seq`); after != before {
		t.Errorf("the reconciliation in force at the land changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	var rows, items int
	if err := h.db.QueryRow(`SELECT count(*), count(DISTINCT item_seq) FROM task_post_land_entries`).Scan(&rows, &items); err != nil {
		t.Fatal(err)
	}
	if rows != 3 || items != 1 {
		t.Errorf("post-land rows = %d over %d items; want 3 rows, all for P2", rows, items)
	}
}

func TestPostLand_ReplayMatchesLive(t *testing.T) {
	h := landedWithDeferred(t)
	p := h.mustPostLand(entry(2, "blocked", "install pending"))
	h.answer(p.Questions[0].ID)
	h.mustPostLand(ReconciliationEntry{ItemSeq: 2, Verdict: "as_planned"})

	replay := newLandingTestDB(t)
	for _, evt := range h.events {
		if err := replayEvent(replay, evt, &ProjectResult{}); err != nil {
			t.Fatalf("replay %s: %v", evt.Kind, err)
		}
	}
	for _, q := range []string{
		`SELECT * FROM task_plan_items ORDER BY seq`,
		`SELECT * FROM task_reconciliation_entries ORDER BY reconciliation_id, item_seq`,
		`SELECT * FROM task_post_land_entries ORDER BY id`,
		`SELECT * FROM task_questions ORDER BY id`,
	} {
		if live, rep := dumpRows(t, h.db, q), dumpRows(t, replay, q); live != rep {
			t.Errorf("%s\nlive:\n%s\nreplay:\n%s", q, live, rep)
		}
	}
}

func TestPostLand_OnlyTheUserWithdrawsAPostLandItem(t *testing.T) {
	h := landedWithDeferred(t)
	withdraw := func(seq int64) TaskPlanItemsChangedPayload {
		return TaskPlanItemsChangedPayload{Ops: []PlanItemOp{{Op: PlanOpWithdraw, Seq: seq}}}
	}
	p := withdraw(2)
	if err := PreparePlanItemsChangedBy(h.db, questionTask, &p, true); err == nil || !strings.Contains(err.Error(), "user's") {
		t.Fatalf("agent withdrawal: err = %v", err)
	}
	p = withdraw(1)
	if err := PreparePlanItemsChangedBy(h.db, questionTask, &p, false); err == nil || !strings.Contains(err.Error(), "not a post-land item") {
		t.Fatalf("withdrawing an ordinary item of a landed task: err = %v", err)
	}
	edit := TaskPlanItemsChangedPayload{Ops: []PlanItemOp{{Op: PlanOpEdit, Seq: 2, Statement: str("x")}}}
	if err := PreparePlanItemsChangedBy(h.db, questionTask, &edit, false); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("editing an item of a landed task: err = %v", err)
	}
	p = withdraw(2)
	if err := PreparePlanItemsChangedBy(h.db, questionTask, &p, false); err != nil {
		t.Fatalf("user withdrawal: %v", err)
	}
}

func TestPostLand_LandedSettlesOnlyByItself(t *testing.T) {
	if err := ValidateLandedSettle("landed", "assumed", 7); err == nil {
		t.Error("task update landed → assumed was accepted; only the post-land settle takes it")
	}
	if err := ValidateLandedSettle("unlanded", "assumed", 7); err != nil {
		t.Errorf("unlanded → assumed refused: %v", err)
	}
	db := newLandingTestDB(t)
	err := ValidateStatusActor(db, questionTask, "landed", Actor{Kind: ActorCLI, Harness: "claude-code"})
	if err == nil || !strings.Contains(err.Error(), "worktree land") {
		t.Errorf("an agent setting landed: err = %v", err)
	}
}
