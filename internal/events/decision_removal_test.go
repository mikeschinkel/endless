package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
)

// decisionAllocatorFloor runs the exact query the decision-id allocator uses
// (events.PreAllocateDecisionID). It reads `decisions`, never `live_decisions` —
// that is the property under test.
func decisionAllocatorFloor(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var next int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM decisions").Scan(&next); err != nil {
		t.Fatalf("decision allocator floor query: %v", err)
	}
	return next
}

func appendDecisionCreated(t *testing.T, w *events.Writer, project, id, ts, title string) {
	t.Helper()
	payload, err := json.Marshal(events.DecisionCreatedPayload{Title: title})
	if err != nil {
		t.Fatalf("marshal decision.created payload: %v", err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindDecisionCreated,
		Project: project,
		Entity:  events.EntityRef{Type: events.EntityDecision, ID: id},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	})
}

// appendDecisionRemoval appends a removal under the given kind, so the same
// stream can be run under decision.removed and under the retired
// decision.deleted spelling.
func appendDecisionRemoval(t *testing.T, w *events.Writer, kind events.Kind, project, id, ts string) {
	t.Helper()
	payload, err := json.Marshal(events.DecisionRemovedPayload{Title: "Doomed"})
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind, err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    kind,
		Project: project,
		Entity:  events.EntityRef{Type: events.EntityDecision, ID: id},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	})
}

// TestProjectToTempDB_DecisionRemovalRetainsRowAndIDFloor is the
// executor/projector parity check for ED-1547 as it applies to decisions
// (E-2010) — the decisions half of the failure E-1929 pinned for tasks.
//
// The executor marks a removed decision `removed = 1` (see removeDecision). If
// the projector still replayed the removal as a real DELETE, rebuilding the DB
// from the ledger would drop the retained row, MAX(id) would fall back, and the
// next decision allocated would REUSE the removed id. That id is an ED-NNNN
// written into task descriptions, plans, commit subjects and CLAUDE.md files, so
// reuse silently repoints every one of those references at an unrelated
// decision — and nothing in normal use would surface it.
//
// E-1929 flagged the decision allocator as carrying this exact hazard and left
// it alone because decisions had no `removed` column yet. This is the test that
// says they do now.
//
// Run under BOTH event spellings. decision.deleted is in RetiredKinds, whose
// default handling in the projector is a declared no-op — so a refactor that
// dropped the explicit case for it would make replay silently stop removing
// anything, resurrecting every decision ever removed. The subtest is what
// notices.
//
// Deliberately NOT verified by rebuilding a real DB, for the reasons given on
// TestProjectToTempDB_TaskDeletedRetainsRowAndIDFloor.
func TestProjectToTempDB_DecisionRemovalRetainsRowAndIDFloor(t *testing.T) {
	for _, kind := range []events.Kind{
		events.KindDecisionRemoved,
		events.KindDecisionDeleted, // retired spelling, still in the ledger
	} {
		t.Run(string(kind), func(t *testing.T) {
			dir := t.TempDir()

			w, err := events.NewWriter(dir, "dead")
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			appendDecisionCreated(t, w, "proj-decision-removal", "700", "5WYM00000001", "Survivor")
			appendDecisionCreated(t, w, "proj-decision-removal", "701", "5WYM00000002", "Doomed")

			// A relation sourced by the doomed decision. Its FK was ON DELETE
			// CASCADE and an UPDATE fires no FK action, so the removal path has
			// to drop it by hand — if it does not, the relation survives
			// pointing at a decision nothing shows.
			relPayload, err := json.Marshal(events.DecisionRelationCreatedPayload{
				SourceDecisionID: 701,
				TargetKind:       "decision",
				TargetID:         700,
				RelationType:     "supersedes",
			})
			if err != nil {
				t.Fatalf("marshal decision_relation.created payload: %v", err)
			}
			appendEvent(t, w, events.Event{
				V:       events.Version,
				TS:      "5WYM00000003",
				Kind:    events.KindDecisionRelationCreated,
				Project: "proj-decision-removal",
				Entity:  events.EntityRef{Type: events.EntityDecisionRelation, ID: "701"},
				Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
				Payload: relPayload,
			})

			appendDecisionRemoval(t, w, kind, "proj-decision-removal", "701", "5WYM00000004")

			tempPath, _, err := events.ProjectToTempDB(dir)
			if err != nil {
				t.Fatalf("ProjectToTempDB: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(tempPath) })

			db, err := sql.Open("sqlite", tempPath)
			if err != nil {
				t.Fatalf("open temp db: %v", err)
			}
			defer db.Close()

			var removed int
			if err := db.QueryRow("SELECT removed FROM decisions WHERE id = 701").Scan(&removed); err != nil {
				t.Fatalf("replayed removal deleted the row instead of marking it: %v", err)
			}
			if removed != 1 {
				t.Errorf("decisions.removed = %d for replayed removal, want 1 "+
					"— a retired kind is a declared no-op unless the projector "+
					"keeps an explicit case for it", removed)
			}

			var live int
			if err := db.QueryRow("SELECT count(*) FROM live_decisions WHERE id = 701").Scan(&live); err != nil {
				t.Fatalf("count live_decisions: %v", err)
			}
			if live != 0 {
				t.Errorf("removed decision visible through live_decisions (%d row(s))", live)
			}

			// The survivor is untouched — removal is not a bulk operation.
			if err := db.QueryRow("SELECT count(*) FROM live_decisions WHERE id = 700").Scan(&live); err != nil {
				t.Fatalf("count live_decisions for survivor: %v", err)
			}
			if live != 1 {
				t.Errorf("survivor decision 700 live rows = %d, want 1", live)
			}

			// The cascade that no longer fires, performed by hand instead.
			var rels int
			if err := db.QueryRow(
				"SELECT count(*) FROM decision_relations WHERE source_decision_id = 701",
			).Scan(&rels); err != nil {
				t.Fatalf("count decision_relations: %v", err)
			}
			if rels != 0 {
				t.Errorf("relations of removed decision = %d, want 0 — the ON DELETE "+
					"CASCADE stopped firing when removal became an UPDATE, so "+
					"removeDecision must delete them explicitly", rels)
			}

			// The regression this whole change exists to prevent: the next id
			// allocated against the rebuilt DB must be ABOVE the removed one.
			if got := decisionAllocatorFloor(t, db); got != 702 {
				t.Errorf("decision allocator floor after replaying removal of the "+
					"highest id = %d, want 702 — a re-freed id repoints every "+
					"ED-%d reference already written down", got, 701)
			}
		})
	}
}

// TestKinds_DecisionDeletedIsRetiredButNotEmittable pins the two halves of the
// rename (E-2010): the old spelling may no longer be WRITTEN, and the new one
// may.
//
// ValidKinds is what the executor and the `event emit` front door gate on, so
// membership there is exactly "a live path may emit this". RetiredKinds is what
// replay consults to get past a kind nothing writes any more. decision.deleted
// has to be in the second and out of the first, or either the rename is
// cosmetic or the events already in the ledger stop replaying.
func TestKinds_DecisionDeletedIsRetiredButNotEmittable(t *testing.T) {
	if !events.ValidKinds[events.KindDecisionRemoved] {
		t.Error("decision.removed is not in ValidKinds, so nothing can emit it")
	}
	if events.ValidKinds[events.KindDecisionDeleted] {
		t.Error("decision.deleted is still in ValidKinds, so the old spelling " +
			"can still be emitted and the rename bought nothing")
	}
	if !events.RetiredKinds[events.KindDecisionDeleted] {
		t.Error("decision.deleted is not in RetiredKinds, so a ledger holding " +
			"one no longer replays")
	}
	if !events.KnownKind(events.KindDecisionDeleted) {
		t.Error("decision.deleted is not a known kind, so events already in the " +
			"ledger are malformed")
	}
}
