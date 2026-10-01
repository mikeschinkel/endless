package monitor

import (
	"context"
	"testing"
)

// verdictStub decides each task's unsettled verdict by id, standing in for the
// worktree probe so an epic's roll-up can be tested without building git
// worktrees. A task absent from the map has no worktree: known and settled.
type verdictStub struct {
	byID  map[int64]UnsettledDetail
	calls map[int64]int
}

func installVerdictStub(t *testing.T, byID map[int64]UnsettledDetail) *verdictStub {
	t.Helper()
	s := &verdictStub{byID: byID, calls: map[int64]int{}}
	prev := taskUnsettledVerdict
	taskUnsettledVerdict = func(_ context.Context, _, taskID int64) UnsettledDetail {
		s.calls[taskID]++
		if d, ok := s.byID[taskID]; ok {
			return d
		}
		return UnsettledDetail{UnlandedKnown: true}
	}
	t.Cleanup(func() { taskUnsettledVerdict = prev })
	return s
}

// Verdict fixtures. A dirty worktree is known unsettled without the cache; a
// clean worktree with no cached verdict is the not-yet-determined state.
var (
	verdictUnsettled = UnsettledDetail{HasWorktree: true, Modified: []string{"a.go"}}
	verdictUnknown   = UnsettledDetail{HasWorktree: true}
	verdictSettled   = UnsettledDetail{HasWorktree: true, UnlandedKnown: true}
)

func annotateOne(t *testing.T, r SessionStatusRow) SessionStatusRow {
	t.Helper()
	rows := []SessionStatusRow{r}
	AnnotateSessionStatusUnsettled(context.Background(), rows)
	return rows[0]
}

func epicRow(id int64, status string) SessionStatusRow {
	return SessionStatusRow{ID: id, ProjectID: 1, Status: status, TypeSlug: "epic"}
}

// TestEpicWorkProductRollsUpFromChildren is E-2198's reproduction. E-1991 had
// five children with landed code, three of them terminal, and still wore
// ⊙ not started, because the mark was decided by the epic's own status — which
// never reaches Shipped by the epic's own work.
func TestEpicWorkProductRollsUpFromChildren(t *testing.T) {
	cases := []struct {
		name          string
		children      map[int64]string
		verdicts      map[int64]UnsettledDetail
		wantShipped   bool
		wantUnsettled bool
		wantKnown     bool
	}{
		{"a shipped child and nothing outstanding",
			map[int64]string{11: "assumed", 12: "unplanned"}, nil, true, false, true},
		{"every child still unshipped",
			map[int64]string{11: "unplanned", 12: "ready"}, nil, false, false, true},
		{"an abandoned child is not shipped work",
			map[int64]string{11: "declined", 12: "obsolete"}, nil, false, false, true},
		{"an unsettled child",
			map[int64]string{11: "confirmed", 12: "underway"},
			map[int64]UnsettledDetail{12: verdictUnsettled}, true, true, true},
		{"a child whose verdict is not yet known",
			map[int64]string{11: "confirmed", 12: "underway"},
			map[int64]UnsettledDetail{11: verdictSettled, 12: verdictUnknown}, true, false, false},
		{"a known unsettled child outranks an unknown sibling",
			map[int64]string{11: "underway", 12: "underway"},
			map[int64]UnsettledDetail{11: verdictUnsettled, 12: verdictUnknown}, false, true, true},
		{"the epic's own unsettled worktree still counts",
			map[int64]string{11: "confirmed"},
			map[int64]UnsettledDetail{10: verdictUnsettled}, true, true, true},
		{"the epic's own unknown verdict still counts",
			map[int64]string{11: "confirmed"},
			map[int64]UnsettledDetail{10: verdictUnknown}, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := withTestDB(t)
			seedProject(t, db, 1, "p1", "/p1")
			insertTaskFull(t, db, 10, 1, 0, typeEpic, "submitted")
			for id, status := range c.children {
				insertTaskFull(t, db, id, 1, 10, typeTask, status)
			}
			installVerdictStub(t, c.verdicts)

			got := annotateOne(t, epicRow(10, "submitted"))
			if got.DescendantShipped != c.wantShipped || got.Unsettled != c.wantUnsettled || got.UnsettledKnown != c.wantKnown {
				t.Errorf("shipped=%v unsettled=%v known=%v, want shipped=%v unsettled=%v known=%v",
					got.DescendantShipped, got.Unsettled, got.UnsettledKnown,
					c.wantShipped, c.wantUnsettled, c.wantKnown)
			}
			if got.HasWorkProduct() != c.wantShipped {
				t.Errorf("HasWorkProduct() = %v, want %v", got.HasWorkProduct(), c.wantShipped)
			}
		})
	}
}

// TestEpicWorkProductReadsDescendants pins the walk below one level. A child
// epic's work product is its own children's, so a parent of epics must see its
// grandchildren — and a sub-epic's derived `completed` (here from declined
// children only) must not count as shipped work by itself.
func TestEpicWorkProductReadsDescendants(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "underway")
	insertTaskFull(t, db, 11, 1, 10, typeEpic, "completed")
	insertTaskFull(t, db, 12, 1, 11, typeTask, "declined")
	installVerdictStub(t, nil)

	if got := annotateOne(t, epicRow(10, "underway")); got.DescendantShipped {
		t.Error("a sub-epic's derived completed status counted as shipped work")
	}

	insertTaskFull(t, db, 13, 1, 11, typeTask, "assumed")
	if got := annotateOne(t, epicRow(10, "underway")); !got.DescendantShipped {
		t.Error("a shipped grandchild under a sub-epic did not reach the top epic")
	}

	installVerdictStub(t, map[int64]UnsettledDetail{13: verdictUnsettled})
	if got := annotateOne(t, epicRow(10, "underway")); !got.Unsettled {
		t.Error("an unsettled grandchild did not mark the top epic")
	}
}

// TestEpicWorkProductFollowsEffectiveParent: a child whose own parent was
// removed renders under the epic above it (E-2161), so it must count there too —
// the same edge epic status derivation reads.
func TestEpicWorkProductFollowsEffectiveParent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "submitted")
	insertTaskFull(t, db, 11, 1, 10, typeTask, "ready")
	insertTaskFull(t, db, 12, 1, 11, typeTask, "confirmed")
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = 11"); err != nil {
		t.Fatalf("remove intermediate: %v", err)
	}
	installVerdictStub(t, nil)

	if got := annotateOne(t, epicRow(10, "submitted")); !got.DescendantShipped {
		t.Error("a shipped child behind a removed intermediate did not count")
	}
}

// TestEpicWorkProductLeavesOrdinaryRowsAlone is the E-2107 constraint: the
// descendant read joins the hot path for epic rows only. A todo with a shipped
// child keeps its own status's answer, and nothing is probed on its behalf.
func TestEpicWorkProductLeavesOrdinaryRowsAlone(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeTask, "underway")
	insertTaskFull(t, db, 11, 1, 10, typeTask, "confirmed")
	stub := installVerdictStub(t, map[int64]UnsettledDetail{11: verdictUnsettled})

	got := annotateOne(t, SessionStatusRow{ID: 10, ProjectID: 1, Status: "underway", TypeSlug: "todo"})
	if got.DescendantShipped || got.Unsettled || got.HasWorkProduct() {
		t.Errorf("a todo row rolled up its child: %+v", got)
	}
	if stub.calls[11] != 0 {
		t.Errorf("probed a todo's child %d time(s); only epic rows read descendants", stub.calls[11])
	}
}

// TestEpicWorkProductReusesRenderedVerdicts: a descendant that is itself a row
// in the frame already has a verdict from the first pass, and the epic reads
// that one rather than probing its worktree a second time on the same tick.
func TestEpicWorkProductReusesRenderedVerdicts(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 10, 1, 0, typeEpic, "underway")
	insertTaskFull(t, db, 11, 1, 10, typeTask, "underway")
	insertTaskFull(t, db, 12, 1, 10, typeTask, "confirmed")
	stub := installVerdictStub(t, map[int64]UnsettledDetail{11: verdictUnsettled, 12: verdictSettled})

	rows := []SessionStatusRow{
		epicRow(10, "underway"),
		{ID: 11, ProjectID: 1, Status: "underway", TypeSlug: "todo"},
	}
	AnnotateSessionStatusUnsettled(context.Background(), rows)
	if !rows[0].Unsettled {
		t.Error("the epic missed its rendered child's unsettled verdict")
	}
	if stub.calls[11] != 1 {
		t.Errorf("the rendered child was probed %d times, want 1", stub.calls[11])
	}
	if stub.calls[12] != 1 {
		t.Errorf("the unrendered child was probed %d times, want 1", stub.calls[12])
	}
}
