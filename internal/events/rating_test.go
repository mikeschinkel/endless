package events_test

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// ratingRows renders every task's rating columns as "id complexity/risk", with
// NULL as "-", so two databases can be compared as one string.
func ratingRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id, complexity_id, risk_id FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("read ratings: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		var c, r sql.NullInt64
		if err = rows.Scan(&id, &c, &r); err != nil {
			t.Fatalf("scan ratings: %v", err)
		}
		out = append(out, fmt.Sprintf("%d %s/%s", id, nullInt(c), nullInt(r)))
	}
	return strings.Join(out, "\n")
}

func nullInt(v sql.NullInt64) string {
	if !v.Valid {
		return "-"
	}
	return fmt.Sprintf("%d", v.Int64)
}

func strPtr(s string) *string { return &s }

// TestRatings_LiveAndRebuildAgree applies one ledger both ways — the executor
// and the projector — and requires the same ratings from each. It covers a
// rating set at creation, set and cleared by fields_updated, and a historical
// `tier` key, which the projector must skip (E-1813 dropped tier, it did not
// map it).
func TestRatings_LiveAndRebuildAgree(t *testing.T) {
	var ledger []events.Event
	ts := 0
	next := func() string { ts++; return fmt.Sprintf("5WYN%08d", ts) }
	created := func(id int64, p events.TaskCreatedPayload) {
		p.Title, p.Phase, p.Status, p.Type = fmt.Sprintf("task %d", id), "now", "unplanned", "todo"
		ledger = append(ledger, taskEvent(t, events.KindTaskCreated, id, next(), p))
	}
	fields := func(id int64, f map[string]any) {
		ledger = append(ledger, taskEvent(t, events.KindTaskFieldsUpdated, id, next(),
			events.TaskFieldsUpdatedPayload{Fields: f}))
	}

	created(800, events.TaskCreatedPayload{Complexity: strPtr("low"), Risk: strPtr("high")})
	created(801, events.TaskCreatedPayload{})
	fields(801, map[string]any{"complexity": "medium", "risk": "low"})
	fields(801, map[string]any{"risk": nil})
	created(802, events.TaskCreatedPayload{Complexity: strPtr("high")})
	fields(802, map[string]any{"complexity": "none"})

	// Live: every event but the historical tier one, which the executor refuses.
	liveDB := withExecutorDB(t)
	for i := range ledger {
		if _, err := events.Execute(&ledger[i], nil); err != nil {
			t.Fatalf("Execute %s on E-%s: %v", ledger[i].Kind, ledger[i].Entity.ID, err)
		}
	}

	// Rebuilt: the same ledger plus a historical tier edit, which must not move
	// anything.
	fields(800, map[string]any{"tier": float64(1)})
	dir := t.TempDir()
	w, err := events.NewWriter(dir, "c0de")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for _, evt := range ledger {
		appendEvent(t, w, evt)
	}
	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })
	if len(result.Errors) > 0 {
		t.Fatalf("projection reported errors: %v", result.Errors)
	}
	rebuiltDB, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open projected db: %v", err)
	}
	defer rebuiltDB.Close()

	live, rebuilt := ratingRows(t, liveDB), ratingRows(t, rebuiltDB)
	want := "800 1/5\n801 3/-\n802 -/-"
	if live != want {
		t.Errorf("live ratings:\n%s\nwant:\n%s", live, want)
	}
	if rebuilt != live {
		t.Errorf("live and rebuilt ratings differ.\nlive:\n%s\nrebuilt:\n%s", live, rebuilt)
	}
}

// TestRatings_ExecutorRefuses pins the write-side guards: an unknown slug and a
// `tier` field are both hard errors live, so neither reaches the database.
func TestRatings_ExecutorRefuses(t *testing.T) {
	withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 810, "seed"), nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i, f := range []map[string]any{
		{"complexity": "extreme"},
		{"risk": float64(3)},
		{"tier": float64(1)},
	} {
		evt := taskEvent(t, events.KindTaskFieldsUpdated, 810, fmt.Sprintf("5WYN0000090%d", i),
			events.TaskFieldsUpdatedPayload{Fields: f})
		if _, err := events.Execute(&evt, nil); err == nil {
			t.Errorf("fields %v were accepted", f)
		}
	}

	bad := taskEvent(t, events.KindTaskCreated, 811, "5WYN00000910", events.TaskCreatedPayload{
		Title: "t", Phase: "now", Status: "unplanned", Type: "todo", Risk: strPtr("severe"),
	})
	if _, err := events.Execute(&bad, nil); err == nil {
		t.Error("task.created with an unknown risk slug was accepted")
	}
}

// TestRatings_StatusChangeLeavesThemAlone pins that ratings are not cleared on
// settling, as tier was: a rating is a record of the judgment, not a queue key.
func TestRatings_StatusChangeLeavesThemAlone(t *testing.T) {
	db := withExecutorDB(t)
	evt := taskEvent(t, events.KindTaskCreated, 820, "5WYN00000920", events.TaskCreatedPayload{
		Title: "t", Phase: "now", Status: "underway", Type: "todo",
		Complexity: strPtr("low"), Risk: strPtr("medium"),
	})
	if _, err := events.Execute(&evt, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	st := taskEvent(t, events.KindTaskStatusChanged, 820, "5WYN00000921", events.TaskStatusChangedPayload{
		OldStatus: "underway", NewStatus: "declined", Reason: "not needed",
	})
	if _, err := events.Execute(&st, nil); err != nil {
		t.Fatalf("status change: %v", err)
	}
	if got := ratingRows(t, db); got != "820 1/3" {
		t.Errorf("ratings after declining = %q, want 820 1/3", got)
	}
}
