package faults_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

// Project attribution (E-1960).
//
// One Endless database holds every project on the machine. Before E-1960 the
// `errors` table said nothing about which one a fault came from, so two
// unrelated projects hitting the same condition became ONE incident with a
// doubled count, `errors show` could not filter, and a producer that knew its
// project had to smuggle it through Source or Fields.

// seedProjects registers two projects and returns their ids. The rows are real
// because project_id is a foreign key: attributing a fault to a project that
// does not exist must fail, not silently store a dangling id.
func seedProjects(t *testing.T, db *sql.DB) (alpha, beta int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path) VALUES
		     (1, 'alpha', '~/Projects/alpha'), (2, 'beta', '~/Projects/beta')`,
	); err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	return 1, 2
}

// bindProject rebinds the store with a resolver that answers with one fixed
// project — the stand-in for "this process is running in project X", which in
// production is monitor.ProjectForCwd.
func bindProject(t *testing.T, db *sql.DB, logDir string, id int64, name string) {
	t.Helper()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		func(explicit int64) (int64, string) {
			if explicit != 0 {
				return explicit, ""
			}
			return id, name
		},
	)
}

// probe is the fault two projects raise identically: same code, same source,
// same fingerprint, same summary. Nothing but the project tells them apart.
func probe() faults.Fault {
	return faults.Fault{
		Code:        faults.ErrCodeWorktreeProbeFailed,
		Source:      "worktree:unsettled",
		Fingerprint: "e-1\x00git status",
		Summary:     "E-1: git status failed for its worktree",
	}
}

// TestRecord_SeparatesProjectsSharingAFingerprint is the bug E-1960 fixes,
// stated as a test: the partial unique index is qualified by project, so two
// projects hold their own open incident for the same condition.
func TestRecord_SeparatesProjectsSharingAFingerprint(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, beta := seedProjects(t, db)

	bindProject(t, db, logDir, alpha, "alpha")
	faults.Record(probe())
	bindProject(t, db, logDir, beta, "beta")
	faults.Record(probe())

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 2 {
		t.Fatalf("two projects raising one fingerprint opened %d incident(s), want 2", len(incidents))
	}
	got := map[string]int64{}
	for _, incident := range incidents {
		got[incident.Project] = incident.Occurrences
	}
	for _, name := range []string{"alpha", "beta"} {
		if got[name] != 1 {
			t.Errorf("project %q: occurrences = %d, want 1 (the projects collided)", name, got[name])
		}
	}
}

// TestRecord_StillDedupesWithinOneProject: qualifying the index must not cost
// the dedup it exists for. The same project raising the same fingerprint three
// times is still one incident.
func TestRecord_StillDedupesWithinOneProject(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, _ := seedProjects(t, db)
	bindProject(t, db, logDir, alpha, "alpha")

	for i := 0; i < 3; i++ {
		faults.Record(probe())
	}

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 || incidents[0].Occurrences != 3 {
		t.Fatalf("one project raising a fingerprint 3x gave %d incident(s) with %v occurrences, want 1 with 3",
			len(incidents), occurrencesOf(incidents))
	}
}

// TestRecord_DedupesUnattributedFaults is the reason the unique index is over
// COALESCE(project_id, 0) rather than over project_id.
//
// SQLite treats NULLs as DISTINCT in a unique index, so a bare column would stop
// deduplicating exactly the faults that repeat most: the ones no project owns.
// The tmux status bar re-execs per pane every two seconds, so the regression
// this guards is not "a few extra rows", it is thousands an hour.
func TestRecord_DedupesUnattributedFaults(t *testing.T) {
	newBoundStore(t) // no resolver bound: every fault is unattributed

	for i := 0; i < 50; i++ {
		faults.Record(probe())
	}

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("50 unattributed occurrences opened %d incidents, want 1 — "+
			"the open-incident index is not NULL-safe", len(incidents))
	}
	if incidents[0].Occurrences != 50 {
		t.Errorf("occurrences = %d, want 50", incidents[0].Occurrences)
	}
	if incidents[0].ProjectID != 0 || incidents[0].Project != "" {
		t.Errorf("unattributed incident reports project %d/%q, want 0/\"\"",
			incidents[0].ProjectID, incidents[0].Project)
	}
}

// TestRecord_ExplicitProjectIDBeatsTheAmbientOne: a producer told which project
// it is inspecting attributes the fault THERE, not to the directory the process
// happens to be running in. monitor.TaskWorktreeUnsettledDetail is the producer
// this exists for — it is called from machine-wide views that iterate other
// projects' tasks.
func TestRecord_ExplicitProjectIDBeatsTheAmbientOne(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, beta := seedProjects(t, db)
	bindProject(t, db, logDir, alpha, "alpha") // the process is "in" alpha

	f := probe()
	f.ProjectID = beta // but the fault is about beta
	faults.Record(f)

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("recorded %d incidents, want 1", len(incidents))
	}
	if incidents[0].ProjectID != beta {
		t.Errorf("fault filed under project %d, want %d (the ambient project won)",
			incidents[0].ProjectID, beta)
	}
}

// TestRecord_SurvivesAnAttributionThatCannotBeStored: a fault is never lost to
// its own attribution.
//
// errors.project_id is a foreign key, so an id naming a project that does not
// exist — a row deleted between the resolve and the write, a producer holding a
// stale id, a resolver with a bug — has the INSERT rejected by SQLite. Record
// swallows errors by contract, so without the unattributed retry the report
// would disappear silently, which is exactly the class of failure this package
// exists to prevent. Attribution is a refinement; the report is the payload.
func TestRecord_SurvivesAnAttributionThatCannotBeStored(t *testing.T) {
	newBoundStore(t) // no projects seeded: every id below is dangling

	f := probe()
	f.ProjectID = 404
	faults.Record(f)

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("a fault attributed to a missing project was DROPPED (%d recorded, want 1)",
			len(incidents))
	}
	if incidents[0].ProjectID != 0 {
		t.Errorf("stored a dangling project id %d, want 0", incidents[0].ProjectID)
	}
	if incidents[0].Summary != f.Summary {
		t.Errorf("summary = %q, want %q", incidents[0].Summary, f.Summary)
	}
}

// TestList_ProjectScopeIncludesUnattributed pins the rule that makes a scoped
// view safe: it returns that project's incidents PLUS the ones attributed to no
// project. A machine-level failure — the job runner unable to open the database
// — belongs to no project by construction, and a scoped view that dropped it
// would leave it reportable on no view at all.
func TestList_ProjectScopeIncludesUnattributed(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, beta := seedProjects(t, db)

	bindProject(t, db, logDir, alpha, "alpha")
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:a", Summary: "alpha's job failed"})
	bindProject(t, db, logDir, beta, "beta")
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:b", Summary: "beta's job failed"})
	faults.Bind(func() (*sql.DB, error) { return db, nil }, func() string { return logDir }, nil)
	faults.Record(faults.Fault{Code: faults.ErrCodeJobScheduling, Source: "jobs", Summary: "the runner could not open the database"})

	scoped, err := faults.List(faults.ProjectScope(alpha), false, 0)
	if err != nil {
		t.Fatalf("list scoped: %v", err)
	}
	if got := summariesOf(scoped); len(got) != 2 ||
		!contains(got, "alpha's job failed") ||
		!contains(got, "the runner could not open the database") {
		t.Errorf("alpha's scope returned %v, want alpha's own fault plus the unattributed one", got)
	}

	all, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("AllProjects returned %d incidents, want 3", len(all))
	}
}

// TestClear_ScopedClearAllLeavesOtherProjects: `errors clear` with no ids means
// "dismiss what you just showed me", so it must cover exactly the set a listing
// under the same scope produced — including the unattributed fault, and nothing
// belonging to another project.
func TestClear_ScopedClearAllLeavesOtherProjects(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, beta := seedProjects(t, db)

	bindProject(t, db, logDir, alpha, "alpha")
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:a", Summary: "alpha's job failed"})
	bindProject(t, db, logDir, beta, "beta")
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:b", Summary: "beta's job failed"})
	faults.Bind(func() (*sql.DB, error) { return db, nil }, func() string { return logDir }, nil)
	faults.Record(faults.Fault{Code: faults.ErrCodeJobScheduling, Source: "jobs", Summary: "the runner could not open the database"})

	cleared, err := faults.Clear(faults.ProjectScope(alpha), nil, "tester")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared != 2 {
		t.Errorf("scoped clear-all cleared %d, want 2 (alpha's plus the unattributed one)", cleared)
	}

	open, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(open) != 1 || open[0].Project != "beta" {
		t.Errorf("after alpha's clear the open set is %v, want only beta's", summariesOf(open))
	}
}

// TestClear_ByIDIgnoresScope: an id is an exact selector the user typed. Making
// the scope veto it would mean `errors clear 7` silently doing nothing right
// after a machine-wide listing showed row 7.
func TestClear_ByIDIgnoresScope(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, beta := seedProjects(t, db)

	bindProject(t, db, logDir, beta, "beta")
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:b", Summary: "beta's job failed"})

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil || len(incidents) != 1 {
		t.Fatalf("setup: %d incident(s), err %v", len(incidents), err)
	}

	cleared, err := faults.Clear(faults.ProjectScope(alpha), []int64{incidents[0].ID}, "tester")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared != 1 {
		t.Errorf("clearing beta's incident by id from alpha's scope cleared %d, want 1", cleared)
	}
}

// TestDetail_CarriesTheProjectName: the JSONL detail log is read WITHOUT a
// database — `errors show --detail` prints it, and so does anyone tailing the
// file — so the line carries the project's name, not its id. One shared log for
// every project on the machine is unreadable without it.
func TestDetail_CarriesTheProjectName(t *testing.T) {
	db, logDir := newBoundStore(t)
	alpha, _ := seedProjects(t, db)
	bindProject(t, db, logDir, alpha, "alpha")

	faults.Record(probe())

	line := firstDetailLine(t, logDir)
	var decoded faults.Detail
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("decode detail line: %v\n%s", err, line)
	}
	if decoded.Project != "alpha" {
		t.Errorf("detail line project = %q, want %q", decoded.Project, "alpha")
	}
	if !strings.Contains(line, `"project":"alpha"`) {
		t.Errorf("detail line does not carry a project key:\n%s", line)
	}
}

// TestDetail_OmitsAnUnattributedProject: a fault the resolver could not place
// says nothing rather than claiming a project. The key is absent, which is also
// how every line written before E-1960 reads.
func TestDetail_OmitsAnUnattributedProject(t *testing.T) {
	_, logDir := newBoundStore(t)

	faults.Record(probe())

	line := firstDetailLine(t, logDir)
	if strings.Contains(line, `"project"`) {
		t.Errorf("an unattributed fault claimed a project:\n%s", line)
	}
}

func firstDetailLine(t *testing.T, logDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, "errors.jsonl"))
	if err != nil {
		t.Fatalf("read detail log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("detail log is empty")
	}
	return lines[0]
}

func summariesOf(incidents []faults.Incident) []string {
	out := make([]string, 0, len(incidents))
	for _, incident := range incidents {
		out = append(out, incident.Summary)
	}
	return out
}

func occurrencesOf(incidents []faults.Incident) []int64 {
	out := make([]int64, 0, len(incidents))
	for _, incident := range incidents {
		out = append(out, incident.Occurrences)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
