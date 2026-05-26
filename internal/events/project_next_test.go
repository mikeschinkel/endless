package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

func newProjectNextTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("set fks: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-26T00:00:00', '2026-05-26T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Sessions referenced by project_next_revisions.session_id (NOT NULL FK).
	for _, id := range []int{41, 42} {
		if _, err := db.Exec(
			`INSERT INTO sessions (id, session_id, project_id, started_at)
			 VALUES (?, ?, 1, '2026-05-26T00:00:00')`,
			id, fmt.Sprintf("sess-%d", id),
		); err != nil {
			t.Fatalf("seed session %d: %v", id, err)
		}
	}
	return db
}

func reviseEvent(t *testing.T, sessionID string, p ProjectNextRevisedPayload) *Event {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-26T12:00:00",
		Kind:    KindProjectNextRevised,
		Project: "test",
		Entity:  EntityRef{Type: EntityProjectNext, ID: "test"},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host", SessionID: sessionID},
		Payload: payload,
	}
}

func lanes(specs ...[]string) []ProjectNextLanePayload {
	// each spec: [laneID, rationale, taskID1, taskID2, ...]
	out := make([]ProjectNextLanePayload, 0, len(specs))
	for i, s := range specs {
		lane := ProjectNextLanePayload{ID: s[0], Priority: i + 1, Rationale: s[1]}
		for _, taskID := range s[2:] {
			lane.Items = append(lane.Items, ProjectNextItemPayload{TaskID: taskID, Reason: "because"})
		}
		out = append(out, lane)
	}
	return out
}

func TestExecProjectNextRevised_FromScratch(t *testing.T) {
	db := newProjectNextTestDB(t)
	evt := reviseEvent(t, "42", ProjectNextRevisedPayload{
		Lanes: lanes(
			[]string{"resolver", "foundational", "E-1", "E-2", "E-3"},
			[]string{"observability", "after tier 1", "E-4", "E-5", "E-6"},
		),
	})

	res, err := execProjectNextRevised(db, evt)
	if err != nil {
		t.Fatalf("execProjectNextRevised: %v", err)
	}

	var laneCount, itemCount, revCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next_lanes").Scan(&laneCount)
	db.QueryRow("SELECT COUNT(*) FROM project_next_items").Scan(&itemCount)
	db.QueryRow("SELECT COUNT(*) FROM project_next_revisions WHERE change_kind='revise'").Scan(&revCount)
	if laneCount != 2 {
		t.Errorf("lanes: got %d, want 2", laneCount)
	}
	if itemCount != 6 {
		t.Errorf("items: got %d, want 6", itemCount)
	}
	if revCount != 1 {
		t.Errorf("revisions: got %d, want 1", revCount)
	}

	var sessID int64
	db.QueryRow("SELECT session_id FROM project_next_revisions LIMIT 1").Scan(&sessID)
	if sessID != 42 {
		t.Errorf("revision session_id: got %d, want 42", sessID)
	}

	if res.ProjectNext == nil {
		t.Fatalf("nil ProjectNext result")
	}
	if res.ProjectNext.PriorRevision != nil {
		t.Errorf("expected nil PriorRevision on first revise, got %+v", res.ProjectNext.PriorRevision)
	}
	if len(res.ProjectNext.State.Lanes) != 2 {
		t.Errorf("state lanes: got %d, want 2", len(res.ProjectNext.State.Lanes))
	}
	if res.ProjectNext.State.SessionID != 42 {
		t.Errorf("state session: got %d, want 42", res.ProjectNext.State.SessionID)
	}

	// positions are 0-based per lane
	var positions []int
	rows, _ := db.Query("SELECT position FROM project_next_items ORDER BY project_next_lane_id, position")
	for rows.Next() {
		var pos int
		rows.Scan(&pos)
		positions = append(positions, pos)
	}
	rows.Close()
	want := []int{0, 1, 2, 0, 1, 2}
	if fmt.Sprint(positions) != fmt.Sprint(want) {
		t.Errorf("positions: got %v, want %v", positions, want)
	}
}

func TestExecProjectNextRevised_ReplacesAndCapturesPrior(t *testing.T) {
	db := newProjectNextTestDB(t)

	first := reviseEvent(t, "41", ProjectNextRevisedPayload{
		Lanes: lanes([]string{"old", "old rationale", "E-1", "E-2"}),
	})
	if _, err := execProjectNextRevised(db, first); err != nil {
		t.Fatalf("first revise: %v", err)
	}

	second := reviseEvent(t, "42", ProjectNextRevisedPayload{
		Lanes: lanes([]string{"new", "new rationale", "E-9"}),
	})
	res, err := execProjectNextRevised(db, second)
	if err != nil {
		t.Fatalf("second revise: %v", err)
	}

	// Old lanes/items gone, new ones present.
	var laneID string
	if err := db.QueryRow("SELECT lane_id FROM project_next_lanes").Scan(&laneID); err != nil {
		t.Fatalf("query lane: %v", err)
	}
	if laneID != "new" {
		t.Errorf("lane_id: got %q, want \"new\"", laneID)
	}
	var itemCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next_items").Scan(&itemCount)
	if itemCount != 1 {
		t.Errorf("items after replace: got %d, want 1", itemCount)
	}

	// Two revision rows appended.
	var revCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next_revisions").Scan(&revCount)
	if revCount != 2 {
		t.Errorf("revisions: got %d, want 2", revCount)
	}

	// Prior revision captured = the first revise (session 41).
	if res.ProjectNext.PriorRevision == nil {
		t.Fatalf("expected non-nil PriorRevision on second revise")
	}
	if res.ProjectNext.PriorRevision.SessionID != 41 {
		t.Errorf("prior session: got %d, want 41", res.ProjectNext.PriorRevision.SessionID)
	}
}

func TestExecProjectNextRevised_EmptyClears(t *testing.T) {
	db := newProjectNextTestDB(t)

	if _, err := execProjectNextRevised(db, reviseEvent(t, "41", ProjectNextRevisedPayload{
		Lanes: lanes([]string{"l1", "r", "E-1", "E-2"}),
	})); err != nil {
		t.Fatalf("seed revise: %v", err)
	}

	// Revise with an empty list clears all lanes/items but records a revision.
	if _, err := execProjectNextRevised(db, reviseEvent(t, "42", ProjectNextRevisedPayload{
		Lanes: []ProjectNextLanePayload{},
	})); err != nil {
		t.Fatalf("empty revise: %v", err)
	}

	var laneCount, itemCount, revCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next_lanes").Scan(&laneCount)
	db.QueryRow("SELECT COUNT(*) FROM project_next_items").Scan(&itemCount)
	db.QueryRow("SELECT COUNT(*) FROM project_next_revisions").Scan(&revCount)
	if laneCount != 0 || itemCount != 0 {
		t.Errorf("expected empty list, got %d lanes / %d items", laneCount, itemCount)
	}
	if revCount != 2 {
		t.Errorf("revisions: got %d, want 2", revCount)
	}
}

func TestValidateProjectNextRevise(t *testing.T) {
	mkItems := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"task_id":"E-%d","reason":"r"}`, i)
		}
		return b.String()
	}
	oneLane := func(items string) string {
		return `{"lanes":[{"id":"l","priority":1,"rationale":"why","items":[` + items + `]}]}`
	}

	tests := []struct {
		name        string
		payload     string
		wantErr     bool
		wantWarning bool
	}{
		{"empty list ok", `{"lanes":[]}`, false, false},
		{"normal", oneLane(mkItems(5)), false, false},
		{"soft cap boundary ok", oneLane(mkItems(10)), false, false},
		{"soft cap warns", oneLane(mkItems(11)), false, true},
		{"hard cap boundary ok", oneLane(mkItems(25)), false, true},
		{"hard cap refused", oneLane(mkItems(26)), true, false},
		{"missing lane id", `{"lanes":[{"id":"","priority":1,"rationale":"r","items":[]}]}`, true, false},
		{"missing rationale", `{"lanes":[{"id":"l","priority":1,"rationale":"","items":[]}]}`, true, false},
		{"missing task_id", `{"lanes":[{"id":"l","priority":1,"rationale":"r","items":[{"task_id":"","reason":"r"}]}]}`, true, false},
		{"missing reason", `{"lanes":[{"id":"l","priority":1,"rationale":"r","items":[{"task_id":"E-1","reason":""}]}]}`, true, false},
		{"non-integer priority", `{"lanes":[{"id":"l","priority":"high","rationale":"r","items":[]}]}`, true, false},
		{"malformed json", `{not json`, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warning, err := ValidateProjectNextRevise([]byte(tc.payload))
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil (warning=%q)", warning)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.wantWarning && warning == "" {
				t.Errorf("expected a warning, got none")
			}
			if !tc.wantWarning && warning != "" {
				t.Errorf("unexpected warning: %q", warning)
			}
		})
	}
}

// TestProjectNextRevise_ConcurrentBeginImmediateConflicts verifies the SQLite
// guarantee the revise design relies on: while one connection holds a
// BEGIN IMMEDIATE write lock, a second connection's BEGIN IMMEDIATE fails with
// a busy error rather than racing. (BeginImmediate uses monitor.DB(); here we
// drive two raw connections to the same file to assert the lock semantics.)
func TestProjectNextRevise_ConcurrentBeginImmediateConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		db.SetMaxOpenConns(1)
		db.Exec("PRAGMA journal_mode=WAL")
		db.Exec("PRAGMA busy_timeout=200") // keep the test fast
		return db
	}

	a := open()
	if _, err := a.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	b := open()

	if _, err := a.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("A begin immediate: %v", err)
	}
	if _, err := b.Exec("BEGIN IMMEDIATE"); err == nil {
		t.Fatalf("B begin immediate succeeded while A held the lock; expected busy error")
	} else if !strings.Contains(strings.ToLower(err.Error()), "busy") &&
		!strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("B begin immediate: got %v, expected a busy/locked error", err)
	}

	// Once A releases, B can take the lock.
	if _, err := a.Exec("COMMIT"); err != nil {
		t.Fatalf("A commit: %v", err)
	}
	if _, err := b.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("B begin immediate after A released: %v", err)
	}
	b.Exec("ROLLBACK")
}

func TestProjectNextRevisedKind_IsRegistered(t *testing.T) {
	if !ValidKinds[KindProjectNextRevised] {
		t.Errorf("KindProjectNextRevised missing from ValidKinds")
	}
	if !validEntityTypes[EntityProjectNext] {
		t.Errorf("EntityProjectNext missing from validEntityTypes")
	}
}
