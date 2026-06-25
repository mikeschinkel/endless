package monitor

import (
	"strings"
	"testing"
)

// TestTaskText_ReturnsText pins the happy path: a tasks row whose text
// column holds non-empty content is returned verbatim. This is the
// content endless-go session-query task-text writes to stdout for the
// Python claim flow (E-894, E-1445).
func TestTaskText_ReturnsText(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	want := "# Plan\n\nDo the thing.\n"
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, text) VALUES (?, ?, ?, ?, ?)",
		42, 1, "test task", "ready", want,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := TaskText(42)
	if err != nil {
		t.Fatalf("TaskText: %v", err)
	}
	if got != want {
		t.Errorf("TaskText = %q, want %q", got, want)
	}
}

// TestTaskText_EmptyTextReturnsEmpty pins the COALESCE branch: when text
// is NULL (no plan attached), the documented contract is to return ""
// with no error so the caller (create_task_worktree) treats it as
// "no plan file to materialize".
func TestTaskText_EmptyTextReturnsEmpty(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, text) VALUES (?, ?, ?, ?, NULL)",
		43, 1, "test task no text", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := TaskText(43)
	if err != nil {
		t.Fatalf("TaskText: %v", err)
	}
	if got != "" {
		t.Errorf("TaskText = %q, want \"\"", got)
	}
}

// TestTaskText_MissingRowReturnsEmpty pins the sql.ErrNoRows branch: an
// unknown task id returns "", nil so the Python caller can run the
// materialize step uniformly for present and absent rows.
func TestTaskText_MissingRowReturnsEmpty(t *testing.T) {
	withTestDB(t)
	got, err := TaskText(999999)
	if err != nil {
		t.Fatalf("TaskText: %v", err)
	}
	if got != "" {
		t.Errorf("TaskText on missing row = %q, want \"\"", got)
	}
}

// TestFormatTasks_EmptyListInstructsImport pins the no-tasks branch of
// the SessionStart task-context renderer: the message must name the
// project and tell the user how to import or check status.
func TestFormatTasks_EmptyListInstructsImport(t *testing.T) {
	got := FormatTasks("acme", nil)
	for _, want := range []string{"acme", "endless task import", "endless task show"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty FormatTasks output missing %q:\n%s", want, got)
		}
	}
}

// TestFormatTasks_GroupsInProgressAndAvailable pins the two-section
// layout that drives the SessionStart prompt: underway items appear
// under IN PROGRESS, the rest under NEXT UP.
func TestFormatTasks_GroupsInProgressAndAvailable(t *testing.T) {
	items := []Task{
		{ID: 11, Text: "active work", Status: "underway"},
		{ID: 22, Text: "queued work", Status: "ready"},
		{ID: 33, Text: "needs plan", Status: "unplanned"},
	}
	got := FormatTasks("acme", items)
	if !strings.Contains(got, "IN PROGRESS:") {
		t.Errorf("missing IN PROGRESS header:\n%s", got)
	}
	if !strings.Contains(got, "NEXT UP:") {
		t.Errorf("missing NEXT UP header:\n%s", got)
	}
	if !strings.Contains(got, "E-11 active work") {
		t.Errorf("missing underway item:\n%s", got)
	}
	if !strings.Contains(got, "E-22 queued work") {
		t.Errorf("missing next-up item:\n%s", got)
	}
	// Ordering: IN PROGRESS section must come before NEXT UP.
	if strings.Index(got, "IN PROGRESS:") > strings.Index(got, "NEXT UP:") {
		t.Errorf("section order wrong (IN PROGRESS after NEXT UP):\n%s", got)
	}
}

// TestFormatTasks_TruncatesAvailableAtFive pins the cap on NEXT UP — the
// SessionStart prompt is presented to a user, so the list must not blow
// past five entries; remaining count is summarised on a trailing line.
func TestFormatTasks_TruncatesAvailableAtFive(t *testing.T) {
	items := make([]Task, 0, 7)
	for i := int64(1); i <= 7; i++ {
		items = append(items, Task{ID: i, Text: "task", Status: "ready"})
	}
	got := FormatTasks("acme", items)
	// E-1..E-5 must appear; E-6 and E-7 must not — they're truncated.
	for i := int64(1); i <= 5; i++ {
		if !strings.Contains(got, "E-"+itoa(i)+" task") {
			t.Errorf("expected E-%d in truncated output:\n%s", i, got)
		}
	}
	if strings.Contains(got, "E-6 ") || strings.Contains(got, "E-7 ") {
		t.Errorf("entries past the cap should be omitted:\n%s", got)
	}
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("missing 'and N more' summary line:\n%s", got)
	}
}

// TestFormatTasks_FiveAvailableNoTruncationLine pins the boundary: when
// there are exactly five available items, all are listed and no "and N
// more" line appears.
func TestFormatTasks_FiveAvailableNoTruncationLine(t *testing.T) {
	items := make([]Task, 0, 5)
	for i := int64(1); i <= 5; i++ {
		items = append(items, Task{ID: i, Text: "task", Status: "ready"})
	}
	got := FormatTasks("acme", items)
	if strings.Contains(got, "more items") {
		t.Errorf("at boundary of 5 items, truncation line should not appear:\n%s", got)
	}
}

// TestGetActiveTasks_ReturnsInProgressNeedsPlanReady pins the WHERE
// clause: only rows whose status is one of underway / unplanned /
// ready are returned. confirmed and blocked rows are filtered out.
func TestGetActiveTasks_ReturnsInProgressNeedsPlanReady(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	rows := []struct {
		id     int64
		status string
	}{
		{101, "underway"},
		{102, "unplanned"},
		{103, "ready"},
		{104, "confirmed"},
		{105, "blocked"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			"INSERT INTO tasks (id, project_id, title, description, status) VALUES (?, ?, ?, ?, ?)",
			r.id, 1, "task", "task description", r.status,
		); err != nil {
			t.Fatalf("seed %d: %v", r.id, err)
		}
	}

	got, err := GetActiveTasks(1)
	if err != nil {
		t.Fatalf("GetActiveTasks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d tasks, want 3 (filtered): %#v", len(got), got)
	}
	seen := map[int64]string{}
	for _, item := range got {
		seen[item.ID] = item.Status
	}
	for _, want := range []int64{101, 102, 103} {
		if _, ok := seen[want]; !ok {
			t.Errorf("missing task id %d in results: %#v", want, got)
		}
	}
	for _, dropped := range []int64{104, 105} {
		if _, ok := seen[dropped]; ok {
			t.Errorf("task id %d should have been filtered out: %#v", dropped, got)
		}
	}
}

// TestGetActiveTasks_InProgressOrderedFirst pins the ORDER BY: rows
// with status underway sort before the others (CASE 0 vs CASE 1),
// then secondary ordering by sort_order. This is what FormatTasks
// downstream uses to build the IN PROGRESS / NEXT UP sections.
func TestGetActiveTasks_InProgressOrderedFirst(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	// Insert ready first, then underway — the SQL must still surface
	// underway at position 0.
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
		201, 1, "ready a", "ready a desc", "ready", 1,
	); err != nil {
		t.Fatalf("seed 201: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
		202, 1, "in progress", "ip desc", "underway", 5,
	); err != nil {
		t.Fatalf("seed 202: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
		203, 1, "ready b", "ready b desc", "ready", 2,
	); err != nil {
		t.Fatalf("seed 203: %v", err)
	}

	got, err := GetActiveTasks(1)
	if err != nil {
		t.Fatalf("GetActiveTasks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d tasks, want 3", len(got))
	}
	if got[0].ID != 202 {
		t.Errorf("first task = %d, want 202 (underway should sort first)", got[0].ID)
	}
	// Sort order: 201 (sort 1) before 203 (sort 2) among the ready set.
	if got[1].ID != 201 || got[2].ID != 203 {
		t.Errorf("ready ordering wrong: got ids %d,%d,%d", got[0].ID, got[1].ID, got[2].ID)
	}
}

// TestGetActiveTasks_ScopedToProject confirms only rows whose
// project_id matches the argument are returned; rows from sibling
// projects are excluded.
func TestGetActiveTasks_ScopedToProject(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedProject(t, db, 2, "proj-test-2", "/tmp/proj-test-2")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status) VALUES (?, ?, ?, ?, ?)",
		301, 1, "ours", "ours desc", "ready",
	); err != nil {
		t.Fatalf("seed 301: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status) VALUES (?, ?, ?, ?, ?)",
		302, 2, "theirs", "theirs desc", "ready",
	); err != nil {
		t.Fatalf("seed 302: %v", err)
	}

	got, err := GetActiveTasks(1)
	if err != nil {
		t.Fatalf("GetActiveTasks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tasks for project 1, want 1: %#v", len(got), got)
	}
	if got[0].ID != 301 {
		t.Errorf("got task id %d, want 301 (cross-project leak)", got[0].ID)
	}
}

// TestHasInjectedContext_FalseWhenNoRow pins the empty-table branch:
// no activity rows means no injection has happened.
func TestHasInjectedContext_FalseWhenNoRow(t *testing.T) {
	withTestDB(t)
	if HasInjectedContext("sess-none") {
		t.Errorf("HasInjectedContext on empty table = true, want false")
	}
}

// TestMarkContextInjected_FlipsHasInjectedContext pins the round-trip
// between the writer (MarkContextInjected → RecordActivity) and the
// reader (HasInjectedContext checks the activity row by the marker
// shape "session_id":"<sid>" AND "injected_tasks":"true").
func TestMarkContextInjected_FlipsHasInjectedContext(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	if HasInjectedContext("sess-A") {
		t.Fatal("HasInjectedContext was true before marking; baseline broken")
	}
	MarkContextInjected(1, "sess-A", "/tmp/wd")
	if !HasInjectedContext("sess-A") {
		t.Errorf("HasInjectedContext after MarkContextInjected = false, want true")
	}
}

// TestHasInjectedContext_ScopedToSessionID confirms the LIKE pattern
// is keyed on session_id: a different session's injection marker must
// not register as an injection for this session.
func TestHasInjectedContext_ScopedToSessionID(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	MarkContextInjected(1, "sess-A", "/tmp/wd")
	if HasInjectedContext("sess-B") {
		t.Errorf("HasInjectedContext for sess-B = true; sess-A's marker leaked")
	}
}

// TestGetProjectName_ReturnsName pins the happy path: GetProjectName
// returns the seeded name when the project exists.
func TestGetProjectName_ReturnsName(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	got, err := GetProjectName(1)
	if err != nil {
		t.Fatalf("GetProjectName: %v", err)
	}
	if got != "acme" {
		t.Errorf("GetProjectName = %q, want acme", got)
	}
}

// TestGetProjectName_MissingRowReturnsError confirms an unknown id
// surfaces an error (sql.ErrNoRows) so the caller can distinguish
// "registered with empty name" from "no row".
func TestGetProjectName_MissingRowReturnsError(t *testing.T) {
	withTestDB(t)
	got, err := GetProjectName(999999)
	if err == nil {
		t.Errorf("GetProjectName on missing row = (%q, nil), want error", got)
	}
}

// itoa is a tiny inline helper to avoid pulling strconv just for tests.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
