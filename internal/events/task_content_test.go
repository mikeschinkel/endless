package events_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// contentRows renders every task_content row as "task name=content", ordered,
// so two databases' content can be compared as one string.
func contentRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT task_id, name, content FROM task_content ORDER BY task_id, name`)
	if err != nil {
		t.Fatalf("read task_content: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		var name, content string
		if err = rows.Scan(&id, &name, &content); err != nil {
			t.Fatalf("scan task_content: %v", err)
		}
		out = append(out, fmt.Sprintf("%d %s=%s", id, name, content))
	}
	return strings.Join(out, "\n")
}

func taskEvent(t *testing.T, kind events.Kind, id int64, ts string, payload any) events.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind, err)
	}
	return events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    kind,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: raw,
	}
}

// TestTaskContent_LiveAndRebuildAgree is the parity assertion §6 of E-1531's
// plan asked for. The executor and the projector used to keep separate field
// maps, and the projector's omitted `notes`: written live, silently dropped on
// rebuild. Here the same ledger is applied both ways, and the content each path
// produces must be identical — for every content name the enum declares, the
// legacy `text` spelling, a clear, and the outcome/reason split.
func TestTaskContent_LiveAndRebuildAgree(t *testing.T) {
	var ledger []events.Event
	ts := 0
	next := func() string { ts++; return fmt.Sprintf("5WYM%08d", ts) }
	add := func(kind events.Kind, id int64, payload any) {
		ledger = append(ledger, taskEvent(t, kind, id, next(), payload))
	}

	created := func(id int64, p events.TaskCreatedPayload) {
		p.Title, p.Phase, p.Status = fmt.Sprintf("task %d", id), "now", "unplanned"
		if p.Type == "" {
			p.Type = "todo"
		}
		add(events.KindTaskCreated, id, p)
	}
	fields := func(id int64, f map[string]any) {
		add(events.KindTaskFieldsUpdated, id, events.TaskFieldsUpdatedPayload{Fields: f})
	}

	// Filed with every content task.created carries — notes included, which
	// the projector's INSERT once omitted.
	created(700, events.TaskCreatedPayload{
		Type: "research", Plan: "the plan", Analysis: "the analysis", Notes: "## Justification\n\nwhy",
	})
	fields(700, map[string]any{"notes": "revised notes"})
	fields(700, map[string]any{"text": "a legacy-spelled plan"})
	fields(700, map[string]any{"analysis": ""})

	// A decline whose event spells its reason `outcome`, as every event did
	// before E-1531.
	created(701, events.TaskCreatedPayload{})
	add(events.KindTaskStatusChanged, 701, events.TaskStatusChangedPayload{
		OldStatus: "unplanned", NewStatus: "declined", Outcome: "a legacy-spelled reason",
	})

	// The forward spellings.
	created(702, events.TaskCreatedPayload{})
	add(events.KindTaskStatusChanged, 702, events.TaskStatusChangedPayload{
		OldStatus: "unplanned", NewStatus: "declined", Reason: "an explicit reason",
	})

	// Every name the enum declares, through fields_updated: the one list.
	created(703, events.TaskCreatedPayload{})
	all := map[string]any{}
	for _, n := range taskcontent.All() {
		all[n.Slug()] = "content for " + n.Slug()
	}
	fields(703, all)

	// Live.
	liveDB := withExecutorDB(t)
	for i := range ledger {
		if _, err := events.Execute(&ledger[i], nil); err != nil {
			t.Fatalf("Execute %s on E-%s: %v", ledger[i].Kind, ledger[i].Entity.ID, err)
		}
	}

	// Rebuilt.
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

	live, rebuilt := contentRows(t, liveDB), contentRows(t, rebuiltDB)
	if live != rebuilt {
		t.Fatalf("live and rebuilt content differ.\nlive:\n%s\nrebuilt:\n%s", live, rebuilt)
	}

	want := []string{
		"700 notes=revised notes",
		"700 plan=a legacy-spelled plan",
		"701 reason=a legacy-spelled reason",
		"702 reason=an explicit reason",
	}
	for _, n := range taskcontent.All() {
		want = append(want, fmt.Sprintf("703 %s=content for %s", n.Slug(), n.Slug()))
	}
	for _, line := range want {
		if !strings.Contains(live+"\n", line+"\n") {
			t.Errorf("missing %q from content:\n%s", line, live)
		}
	}
	if strings.Contains(live, "700 analysis=") {
		t.Errorf("an empty analysis must delete the row, not store it:\n%s", live)
	}
}

// TestTaskContent_UnknownFieldStillRefused pins that moving content out of the
// column map did not widen what the executor accepts: a key that is neither a
// column nor a content name is still a hard error.
func TestTaskContent_UnknownFieldStillRefused(t *testing.T) {
	withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 710, "seed"), nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	evt := taskEvent(t, events.KindTaskFieldsUpdated, 710, "5WYM00000900",
		events.TaskFieldsUpdatedPayload{Fields: map[string]any{"justification": "x"}})
	if _, err := events.Execute(&evt, nil); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

// TestTaskContent_OneEditOneNotice pins the merge the task_content triggers
// exist for: one update that moves a watched tasks column AND a content field
// is one edit, and must reach a holding session as ONE notice naming both.
func TestTaskContent_OneEditOneNotice(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 720, "held"), nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO sessions (id, project_id, state) VALUES (77, 1, 'working')`,
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (77, 720, '2026-01-01', '2026-01-01')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	evt := taskEvent(t, events.KindTaskFieldsUpdated, 720, "5WYM00000901",
		events.TaskFieldsUpdatedPayload{Fields: map[string]any{"complexity": "low", "plan": "a plan"}})
	if _, err := events.Execute(&evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rows, err := db.Query(`SELECT changes FROM session_notices WHERE session_id = 77`)
	if err != nil {
		t.Fatalf("read notices: %v", err)
	}
	defer rows.Close()
	var notices []map[string]any
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var changes map[string]any
		if err = json.Unmarshal([]byte(raw), &changes); err != nil {
			t.Fatalf("changes not JSON: %s", raw)
		}
		notices = append(notices, changes)
	}
	if len(notices) != 1 {
		t.Fatalf("one edit wrote %d notices, want 1: %v", len(notices), notices)
	}
	for _, field := range []string{"complexity", "plan"} {
		if _, ok := notices[0][field]; !ok {
			t.Errorf("the notice does not name %s: %v", field, notices[0])
		}
	}
	plan, _ := notices[0]["plan"].(map[string]any)
	if plan["before"] != nil || plan["after"] != "…" {
		t.Errorf("plan change = %v, want added with the content elided", plan)
	}

	// A content-only edit still notifies, on its own line.
	evt = taskEvent(t, events.KindTaskFieldsUpdated, 720, "5WYM00000902",
		events.TaskFieldsUpdatedPayload{Fields: map[string]any{"notes": "n"}})
	if _, err := events.Execute(&evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var n int
	if err = db.QueryRow(
		`SELECT count(*) FROM session_notices
		  WHERE session_id = 77 AND json_extract(changes, '$.notes.after') = '…'`,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("a notes-only edit reached the holder %d times, want 1", n)
	}
}
