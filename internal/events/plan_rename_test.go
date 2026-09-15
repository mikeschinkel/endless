// E-1000: tasks.text became tasks.plan, and the event payload key became
// `plan` with it. The db-ledger is immutable, so 1,257 historical events keep
// spelling it `text` forever — 1,128 task.fields_updated carrying a `text`
// field and 129 task.created carrying a `text` payload key.
//
// What this file pins is the property that makes that survivable: BOTH
// spellings land in the renamed column, on both the executor (live apply) and
// the projector (ledger replay). The failure it guards is silent — an
// unmapped key writes no column at all, and an absent field is
// indistinguishable from an empty one, so a rebuild would produce tasks with
// empty plans and no error anywhere.
//
// It also pins the plan-attach promotion, which reads the same payload key to
// decide whether a pre-judgment task moves to `submitted`. That branch is the
// one regression the rename could introduce quietly: it fires only when the
// update sets no explicit status, so a caller passing --status would never
// notice it had stopped working.
package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
)

// planOf reads the renamed column for a task, treating NULL as "".
func planOf(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var plan sql.NullString
	if err := db.QueryRow("SELECT plan FROM tasks WHERE id = ?", id).Scan(&plan); err != nil {
		t.Fatalf("read plan for E-%d: %v", id, err)
	}
	return plan.String
}

// statusOf reads a task's current status.
func statusOf(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatalf("read status for E-%d: %v", id, err)
	}
	return status
}

// planFieldsEvent builds a task.fields_updated event carrying exactly the
// given fields, so a test can spell the plan key either way.
func planFieldsEvent(t *testing.T, id int64, fields map[string]any) *events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskFieldsUpdatedPayload{Fields: fields})
	if err != nil {
		t.Fatalf("marshal fields payload: %v", err)
	}
	return &events.Event{
		V:       events.Version,
		TS:      "5WYM00001000",
		Kind:    events.KindTaskFieldsUpdated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

// TestExecute_PlanKeyWritesPlanColumn is the forward path: everything emitted
// from E-1000 on spells the field `plan`.
func TestExecute_PlanKeyWritesPlanColumn(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 900, "forward"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	const want = "# Plan\n\nthe forward key\n"
	if _, err := events.Execute(
		planFieldsEvent(t, 900, map[string]any{"plan": want}), nil,
	); err != nil {
		t.Fatalf("Execute plan update: %v", err)
	}

	if got := planOf(t, db, 900); got != want {
		t.Errorf("plan = %q, want %q", got, want)
	}
}

// TestExecute_LegacyTextKeyWritesPlanColumn is the same property for the
// historical spelling. The executor rejects an unknown field outright, so
// without the legacy map entry a replayed pre-rename event would not merely
// skip the plan — it would fail the whole apply.
func TestExecute_LegacyTextKeyWritesPlanColumn(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 901, "legacy"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	const want = "# Plan\n\nthe historical key\n"
	if _, err := events.Execute(
		planFieldsEvent(t, 901, map[string]any{"text": want}), nil,
	); err != nil {
		t.Fatalf("Execute legacy update: %v", err)
	}

	if got := planOf(t, db, 901); got != want {
		t.Errorf("plan = %q, want %q", got, want)
	}
}

// TestExecute_BothKeysPresentPrefersPlan pins the tie-break. Nothing emits
// both, but the loop that consumes them walks a map, so without an explicit
// winner the column would be written twice in randomised order.
func TestExecute_BothKeysPresentPrefersPlan(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 902, "both"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if _, err := events.Execute(planFieldsEvent(t, 902, map[string]any{
		"plan": "forward wins",
		"text": "legacy loses",
	}), nil); err != nil {
		t.Fatalf("Execute dual-key update: %v", err)
	}

	if got := planOf(t, db, 902); got != "forward wins" {
		t.Errorf("plan = %q, want the forward key's value", got)
	}
}

// TestExecute_PlanAttachPromotesPreJudgmentTask is the regression the rename
// could have introduced silently: the promotion keys off the payload's plan
// field, and it fires ONLY when the same update sets no status, so a caller
// passing --status would never see it stop working.
func TestExecute_PlanAttachPromotesPreJudgmentTask(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 903, "to-promote"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if got := statusOf(t, db, 903); got != "unplanned" {
		t.Fatalf("seeded status = %q, want unplanned", got)
	}

	if _, err := events.Execute(
		planFieldsEvent(t, 903, map[string]any{"plan": "# Plan\n\nreal body\n"}), nil,
	); err != nil {
		t.Fatalf("Execute plan attach: %v", err)
	}

	if got := statusOf(t, db, 903); got != "submitted" {
		t.Errorf("status after plan attach = %q, want submitted", got)
	}
}

// TestExecute_PlanAttachPromotionAcceptsLegacyKey keeps the promotion working
// for a re-applied historical event, so replay and live apply agree about the
// status a task ends on rather than diverging by payload vintage.
func TestExecute_PlanAttachPromotionAcceptsLegacyKey(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 904, "to-promote-legacy"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if _, err := events.Execute(
		planFieldsEvent(t, 904, map[string]any{"text": "# Plan\n\nreal body\n"}), nil,
	); err != nil {
		t.Fatalf("Execute legacy plan attach: %v", err)
	}

	if got := statusOf(t, db, 904); got != "submitted" {
		t.Errorf("status after legacy plan attach = %q, want submitted", got)
	}
}

// TestExecute_PlanAttachYieldsToExplicitStatus pins the caller-wins half of
// the same branch — the reason the promotion is safe to leave implicit.
func TestExecute_PlanAttachYieldsToExplicitStatus(t *testing.T) {
	db := withExecutorDB(t)
	if _, err := events.Execute(newTaskCreatedEvent(t, 905, "explicit"), nil); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if _, err := events.Execute(planFieldsEvent(t, 905, map[string]any{
		"plan":   "# Plan\n\nreal body\n",
		"status": "untriaged",
	}), nil); err != nil {
		t.Fatalf("Execute plan attach with status: %v", err)
	}

	if got := statusOf(t, db, 905); got != "untriaged" {
		t.Errorf("status = %q, want the explicitly requested untriaged", got)
	}
}

// TestExecute_TaskCreatedLegacyTextKeyStoresPlan covers the 129 historical
// task.created payloads. Their key is decoded by a struct tag, not by the
// field map the tests above exercise, so it is a separate path with the same
// silent failure mode.
func TestExecute_TaskCreatedLegacyTextKeyStoresPlan(t *testing.T) {
	db := withExecutorDB(t)

	// Hand-built JSON rather than the payload struct: the point is the WIRE
	// key a pre-rename emitter wrote, which no current struct field emits.
	payload := []byte(`{"title":"legacy created","phase":"now",` +
		`"status":"unplanned","type":"todo","text":"# Plan\n\nborn as text\n"}`)
	evt := &events.Event{
		V:       events.Version,
		TS:      "5WYM00001001",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(906)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute legacy created: %v", err)
	}

	if got := planOf(t, db, 906); got != "# Plan\n\nborn as text\n" {
		t.Errorf("plan = %q, want the legacy payload's body", got)
	}
	// The create-time promotion reads the same value, so it must see it too.
	if got := statusOf(t, db, 906); got != "submitted" {
		t.Errorf("status = %q, want submitted (plan attached at creation)", got)
	}
}

// TestTaskCreatedPayload_EmitsPlanNeverText pins the emit side: the legacy
// field is a read path only. If it ever marshalled, the ledger would start
// accumulating `text` keys again and the mapping above could never be retired.
func TestTaskCreatedPayload_EmitsPlanNeverText(t *testing.T) {
	out, err := json.Marshal(events.TaskCreatedPayload{
		Title: "emitting", Phase: "now", Status: "unplanned", Type: "todo",
		Plan: "# Plan\n",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := wire["text"]; present {
		t.Errorf("payload emitted a `text` key: %s", out)
	}
	if wire["plan"] != "# Plan\n" {
		t.Errorf("payload plan = %v, want the plan body", wire["plan"])
	}
}

// --- projector (ledger replay) ----------------------------------------------

// TestProjector_BothPlanKeysProjectIntoPlanColumn is the replay-side twin of
// the executor tests. The projector is the path a `db rebuild` takes, so it is
// where an unmapped historical key would rebuild 1,128 tasks with empty plans.
func TestProjector_BothPlanKeysProjectIntoPlanColumn(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		id   string
	}{
		{"forward key", "plan", "910"},
		{"legacy key", "text", "911"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := events.NewWriter(dir, "e100")
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			created, _ := json.Marshal(events.TaskCreatedPayload{
				Title: "replay target", Phase: "now", Status: "unplanned", Type: "todo",
			})
			appendEvent(t, w, events.Event{
				V:       events.Version,
				TS:      "5WYM00002001",
				Kind:    events.KindTaskCreated,
				Project: "proj-plan-rename",
				Entity:  events.EntityRef{Type: events.EntityTask, ID: tc.id},
				Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
				Payload: created,
			})

			const want = "# Plan\n\nreplayed body\n"
			updated, _ := json.Marshal(events.TaskFieldsUpdatedPayload{
				Fields: map[string]any{tc.key: want},
			})
			appendEvent(t, w, events.Event{
				V:       events.Version,
				TS:      "5WYM00002002",
				Kind:    events.KindTaskFieldsUpdated,
				Project: "proj-plan-rename",
				Entity:  events.EntityRef{Type: events.EntityTask, ID: tc.id},
				Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
				Payload: updated,
			})

			tempPath, result, err := events.ProjectToTempDB(dir)
			if err != nil {
				t.Fatalf("ProjectToTempDB: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(tempPath) })
			if len(result.Errors) != 0 {
				t.Fatalf("projection errors: %v", result.Errors)
			}

			db, err := sql.Open("sqlite", tempPath)
			if err != nil {
				t.Fatalf("open projected db: %v", err)
			}
			defer db.Close()

			var plan sql.NullString
			if err := db.QueryRow(
				"SELECT plan FROM tasks WHERE id = ?", tc.id,
			).Scan(&plan); err != nil {
				t.Fatalf("read projected plan: %v", err)
			}
			if plan.String != want {
				t.Errorf("projected plan = %q, want %q", plan.String, want)
			}
		})
	}
}

// TestProjector_TaskCreatedLegacyTextKeyProjectsPlan covers the created-event
// wire key on the replay path, the projector's twin of the executor test above.
func TestProjector_TaskCreatedLegacyTextKeyProjectsPlan(t *testing.T) {
	dir := t.TempDir()
	w, err := events.NewWriter(dir, "e101")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      "5WYM00003001",
		Kind:    events.KindTaskCreated,
		Project: "proj-plan-rename-created",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "912"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: []byte(`{"title":"legacy created","phase":"now",` +
			`"status":"unplanned","type":"todo","text":"# Plan\n\nborn as text\n"}`),
	})

	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tempPath) })
	if len(result.Errors) != 0 {
		t.Fatalf("projection errors: %v", result.Errors)
	}

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open projected db: %v", err)
	}
	defer db.Close()

	var plan sql.NullString
	if err := db.QueryRow("SELECT plan FROM tasks WHERE id = ?", 912).Scan(&plan); err != nil {
		t.Fatalf("read projected plan: %v", err)
	}
	if plan.String != "# Plan\n\nborn as text\n" {
		t.Errorf("projected plan = %q, want the legacy payload's body", plan.String)
	}
}
