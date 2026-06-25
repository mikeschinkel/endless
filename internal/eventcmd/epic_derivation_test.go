// Binary-integration tests for epic status auto-derivation (E-1541),
// exercising both sides through the real endless-go binary:
//
//   - the live emit path (makeDerivedEmitter): a task.status_changed on a child
//     writes an epic.status_derived ledger entry and updates the parent epic.
//     This is the path the --db sandbox cannot exercise (sandbox routing
//     bypasses the ledger writer + auto-commit), so it needs a real git repo.
//   - the projector replay path (replayEpicStatusDerived): a rebuild-db replays
//     a recorded epic.status_derived entry and reproduces the epic's status.
package eventcmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// initGitRepo turns dir into a minimal main-checkout git repo with a local
// identity, so CommitLedgerSegment's auto-commit succeeds.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// seedEpicWithChild inserts a project, an epic (epicID, unplanned), and one
// child task (childID, parent=epicID, childStatus) into the DB at dbPath.
func seedEpicWithChild(t *testing.T, dbPath, projectName string, epicID, childID int64, childStatus string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, ?, ?)",
		projectName, "/tmp/"+projectName,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id, sort_order)
		 VALUES (?, 1, 'epic', 'now', 'unplanned', ?, 10)`,
		epicID, int(tasktype.TaskTypeEpic),
	); err != nil {
		t.Fatalf("seed epic: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, parent_id, title, phase, status, type_id, sort_order)
		 VALUES (?, 1, ?, 'child', 'now', ?, ?, 20)`,
		childID, epicID, childStatus, int(tasktype.TaskTypeTask),
	); err != nil {
		t.Fatalf("seed child: %v", err)
	}
}

// TestEpicDerivation_EmitWritesLedgerAndUpdatesEpic drives the live path: a
// confirm of the epic's only child derives the epic to completed and records an
// epic.status_derived ledger entry with the system/epic-derivation actor.
func TestEpicDerivation_EmitWritesLedgerAndUpdatesEpic(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)
	initGitRepo(t, projectRoot)

	const projectName = "proj-epic-emit"
	const epicID int64 = 700
	const childID int64 = 701
	seedEpicWithChild(t, dbPath, projectName, epicID, childID, "ready")

	payload, err := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: "ready", NewStatus: "confirmed",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "emit",
		"--kind", string(events.KindTaskStatusChanged),
		"--project", projectName,
		"--entity-type", string(events.EntityTask),
		"--entity-id", fmt.Sprintf("%d", childID),
		"--actor-kind", string(events.ActorCLI),
		"--actor-id", "test",
		"--node-id", "a7f3",
		"--project-root", projectRoot,
		"--payload", string(payload),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emit status_changed failed: %v\n%s", err, out)
	}

	// The epic derived to completed in the DB.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	var epicStatus string
	var completedAt sql.NullString
	if err := db.QueryRow(
		"SELECT status, completed_at FROM tasks WHERE id = ?", epicID,
	).Scan(&epicStatus, &completedAt); err != nil {
		t.Fatalf("read epic: %v", err)
	}
	if epicStatus != "completed" {
		t.Errorf("epic status = %q, want completed", epicStatus)
	}
	if !completedAt.Valid {
		t.Errorf("completed epic has NULL completed_at")
	}

	// A well-formed epic.status_derived entry was recorded in the ledger.
	derived := readDerivedLedgerEvents(t, projectRoot)
	if len(derived) != 1 {
		t.Fatalf("epic.status_derived ledger entries = %d, want 1", len(derived))
	}
	got := derived[0]
	if got.Entity.ID != fmt.Sprintf("%d", epicID) {
		t.Errorf("derived entity id = %q, want %d", got.Entity.ID, epicID)
	}
	if got.Actor.Kind != events.ActorSystem || got.Actor.ID != "epic-derivation" {
		t.Errorf("derived actor = %+v, want system/epic-derivation", got.Actor)
	}
	if got.Actor.SessionID != "" {
		t.Errorf("derived actor has session_id %q, want empty", got.Actor.SessionID)
	}
	var p events.EpicStatusDerivedPayload
	if err := json.Unmarshal(got.Payload, &p); err != nil {
		t.Fatalf("unmarshal derived payload: %v", err)
	}
	if p.TaskID != epicID || p.OldStatus != "unplanned" || p.NewStatus != "completed" {
		t.Errorf("derived payload = %+v, want {%d unplanned completed}", p, epicID)
	}
}

// TestEpicDerivation_RebuildReplaysDerivedEvent drives the projector replay path:
// a ledger holding an epic + child create plus a recorded epic.status_derived
// rebuilds an epic whose status is the derived value, with no recompute.
func TestEpicDerivation_RebuildReplaysDerivedEvent(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-epic-rebuild"
	const epicID int64 = 800
	const childID int64 = 801

	// Seed only the project row so rebuild-db's DELETE-by-name has a target.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, ?, ?)",
		projectName, "/tmp/"+projectName,
	); err != nil {
		db.Close()
		t.Fatalf("seed project: %v", err)
	}
	db.Close()

	clock := kairos.NewClock(0xa7f3)
	writeLedgerEvent(t, projectRoot, makeEpicCreatedEvent(t, clock, projectName, epicID, nil))
	writeLedgerEvent(t, projectRoot, makeTaskCreatedChildEvent(t, clock, projectName, childID, epicID))
	writeLedgerEvent(t, projectRoot, makeEpicStatusDerivedEvent(t, clock, projectName, epicID, "unplanned", "underway"))

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
		"--confirm",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rebuild-db failed: %v\n%s", err, out)
	}

	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db2.Close()
	var epicStatus string
	if err := db2.QueryRow("SELECT status FROM tasks WHERE id = ?", epicID).Scan(&epicStatus); err != nil {
		t.Fatalf("read rebuilt epic: %v", err)
	}
	if epicStatus != "underway" {
		t.Errorf("rebuilt epic status = %q, want underway (from replayed derived event)", epicStatus)
	}
}

// readDerivedLedgerEvents scans the project's ledger for epic.status_derived
// events, parsed from the segment files.
func readDerivedLedgerEvents(t *testing.T, projectRoot string) []events.Event {
	t.Helper()
	evts, err := events.ReadAllEvents(projectRoot)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var derived []events.Event
	for _, e := range evts {
		if e.Kind == events.KindEpicStatusDerived {
			derived = append(derived, e)
		}
	}
	return derived
}

func makeEpicCreatedEvent(t *testing.T, clock *kairos.Clock, projectName string, epicID int64, parentID *int64) events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title: "epic", Phase: "now", Status: "unplanned", Type: "epic",
		SortOrder: 10, ParentID: parentID,
	})
	if err != nil {
		t.Fatalf("marshal epic payload: %v", err)
	}
	return events.Event{
		V: events.Version, TS: clock.Now().String(), Kind: events.KindTaskCreated,
		Project: projectName,
		Entity:  events.EntityRef{Type: events.EntityTask, ID: fmt.Sprintf("%d", epicID)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "test"},
		Payload: payload,
	}
}

func makeTaskCreatedChildEvent(t *testing.T, clock *kairos.Clock, projectName string, childID, parentID int64) events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title: "child", Phase: "now", Status: "unplanned", Type: "task",
		SortOrder: 20, ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("marshal child payload: %v", err)
	}
	return events.Event{
		V: events.Version, TS: clock.Now().String(), Kind: events.KindTaskCreated,
		Project: projectName,
		Entity:  events.EntityRef{Type: events.EntityTask, ID: fmt.Sprintf("%d", childID)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "test"},
		Payload: payload,
	}
}

func makeEpicStatusDerivedEvent(t *testing.T, clock *kairos.Clock, projectName string, epicID int64, oldS, newS string) events.Event {
	t.Helper()
	payload, err := json.Marshal(events.EpicStatusDerivedPayload{
		TaskID: epicID, OldStatus: oldS, NewStatus: newS,
	})
	if err != nil {
		t.Fatalf("marshal derived payload: %v", err)
	}
	return events.Event{
		V: events.Version, TS: clock.Now().String(), Kind: events.KindEpicStatusDerived,
		Project: projectName,
		Entity:  events.EntityRef{Type: events.EntityTask, ID: fmt.Sprintf("%d", epicID)},
		Actor:   events.Actor{Kind: events.ActorSystem, ID: "epic-derivation"},
		Payload: payload,
	}
}
