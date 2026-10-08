package autospawnjob

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/schema"
)

// init wires cfgstore's package-global logger, which it panics without; the
// binary sets it in main(). Same pattern as projectstatuscmd's tests.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestDB is an in-memory database carrying the real, migrated schema — so
// task_types.auto_spawnable comes from seeds.sql exactly as in production.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// fixture ids. Projects live under real temp dirs so path resolution works.
const (
	projA = 1
	projB = 2
)

// realDir is a temp dir with symlinks resolved: the selector reads projects
// through monitor.ResolvedProjectPath, and macOS's /var is /private/var.
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// seedProjects registers two projects and returns their directories.
func seedProjects(t *testing.T, db *sql.DB) (dirA, dirB string) {
	t.Helper()
	dirA, dirB = realDir(t), realDir(t)
	mustExec(t, db, `INSERT INTO projects (id, name, path) VALUES (?, 'a', ?), (?, 'b', ?)`,
		projA, dirA, projB, dirB)
	return dirA, dirB
}

// taskSpec is one task, eligible by default; each test case breaks one thing.
type taskSpec struct {
	id         int64
	project    int64
	status     string
	phase      string
	typeID     int
	complexity any // rating id, or nil for unrated
	risk       any
	created    string
	plan       string
}

func eligible(id int64) taskSpec {
	return taskSpec{
		id: id, project: projA, status: "ready", phase: "now", typeID: 1,
		complexity: 1, risk: 1, created: "2026-09-01T00:00:00", plan: "# Plan\nDo it.",
	}
}

func seedTask(t *testing.T, db *sql.DB, s taskSpec) {
	t.Helper()
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title, status, phase, type_id,
	                                complexity_id, risk_id, created_at)
	             VALUES (?, ?, 'T', ?, ?, ?, ?, ?, ?)`,
		s.id, s.project, s.status, s.phase, s.typeID, s.complexity, s.risk, s.created)
	if s.plan != "" {
		mustExec(t, db, `INSERT INTO task_content (task_id, name, content) VALUES (?, 'plan', ?)`, s.id, s.plan)
	}
}

// policies builds a policyFunc from a directory → policy map; a directory not
// in the map has not opted in.
func policies(m map[string]projectPolicy) policyFunc {
	return func(path string) (projectPolicy, error) {
		return m[path], nil
	}
}

// TestSelect_EachConditionExcludes is §1 of the plan, one case per condition:
// the same eligible task, with exactly one thing wrong, must not be picked.
func TestSelect_EachConditionExcludes(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(*taskSpec)
		after func(t *testing.T, db *sql.DB)
	}{
		{"1. status is not ready (submitted)", func(s *taskSpec) { s.status = "submitted" }, nil},
		{"1. status is not ready (underway)", func(s *taskSpec) { s.status = "underway" }, nil},
		{"2. complexity is medium", func(s *taskSpec) { s.complexity = 3 }, nil},
		{"2. risk is high", func(s *taskSpec) { s.risk = 5 }, nil},
		{"2. unrated", func(s *taskSpec) { s.complexity, s.risk = nil, nil }, nil},
		{"3. phase is next", func(s *taskSpec) { s.phase = "next" }, nil},
		{"3. phase is later", func(s *taskSpec) { s.phase = "later" }, nil},
		{"4. type is research", func(s *taskSpec) { s.typeID = 3 }, nil},
		{"4. type is brainstorm", func(s *taskSpec) { s.typeID = 5 }, nil},
		{"4. type is epic", func(s *taskSpec) { s.typeID = 4 }, nil},
		{"5. project not opted in", func(s *taskSpec) { s.project = projB }, nil},
		{"6. blocked by a non-terminal task", nil, func(t *testing.T, db *sql.DB) {
			seedTask(t, db, taskSpec{id: 900, project: projA, status: "unverified", phase: "later", typeID: 1, created: "2026-01-01T00:00:00"})
			mustExec(t, db, `INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
			             VALUES ('task', 900, 'task', 100, 'blocks')`)
		}},
		{"6. preceded by a non-terminal task (E-2270)", nil, func(t *testing.T, db *sql.DB) {
			seedTask(t, db, taskSpec{id: 900, project: projA, status: "unverified", phase: "later", typeID: 1, created: "2026-01-01T00:00:00"})
			mustExec(t, db, `INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
			             VALUES ('task', 900, 'task', 100, 'precedes')`)
		}},
		{"7. no plan", func(s *taskSpec) { s.plan = "" }, nil},
		{"7. whitespace-only plan", func(s *taskSpec) { s.plan = " \n\t\n" }, nil},
		{"7. an open question", nil, func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO task_questions (task_id, series, question, status) VALUES (100, 1, 'which?', 'open')`)
		}},
		{"8. a session once claimed it", nil, func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO sessions (session_id, state, task_id) VALUES ('old', 'ended', 100)`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			dirA, _ := seedProjects(t, db)
			s := eligible(100)
			if tc.tweak != nil {
				tc.tweak(&s)
			}
			seedTask(t, db, s)
			if tc.after != nil {
				tc.after(t, db)
			}
			pick, skip, err := selectCandidate(db, policies(map[string]projectPolicy{
				dirA: {enabled: true, cap: 3},
			}))
			if err != nil {
				t.Fatalf("selectCandidate: %v", err)
			}
			if pick.taskID != 0 {
				t.Fatalf("picked E-%d, want nothing", pick.taskID)
			}
			if skip != "nothing eligible" {
				t.Errorf("skip = %q, want %q", skip, "nothing eligible")
			}
		})
	}
}

// TestSelect_EligiblePickedWithProjectDir is the positive control for the
// table above, and pins that the pick carries the directory spawn runs from.
func TestSelect_EligiblePickedWithProjectDir(t *testing.T) {
	for _, typeID := range []int{1, 2} { // todo, bugfix
		db := newTestDB(t)
		dirA, _ := seedProjects(t, db)
		s := eligible(100)
		s.typeID = typeID
		seedTask(t, db, s)
		// A blocker that has SETTLED does not block (the task-next rule).
		seedTask(t, db, taskSpec{id: 900, project: projA, status: "confirmed", phase: "later", typeID: 1, created: "2026-01-01T00:00:00"})
		mustExec(t, db, `INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		             VALUES ('task', 900, 'task', 100, 'blocks')`)
		// Nor does a predecessor that has settled (E-2270).
		seedTask(t, db, taskSpec{id: 901, project: projA, status: "superseded", phase: "later", typeID: 1, created: "2026-01-01T00:00:00"})
		mustExec(t, db, `INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		             VALUES ('task', 901, 'task', 100, 'precedes')`)
		// A question that was answered does not park it.
		mustExec(t, db, `INSERT INTO task_questions (task_id, series, question, status) VALUES (100, 1, 'q', 'answered')`)

		pick, skip, err := selectCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 3}}))
		if err != nil || skip != "" {
			t.Fatalf("type %d: skip=%q err=%v, want a pick", typeID, skip, err)
		}
		if pick.taskID != 100 || pick.projectPath != dirA {
			t.Errorf("type %d: pick = %+v, want E-100 in %s", typeID, pick, dirA)
		}
	}
}

// TestSelect_Ordering: urgent before now, then oldest first, across projects.
func TestSelect_Ordering(t *testing.T) {
	db := newTestDB(t)
	dirA, dirB := seedProjects(t, db)
	newNow := eligible(101)
	newNow.created = "2026-09-20T00:00:00"
	oldNow := eligible(102)
	oldNow.created = "2026-09-02T00:00:00"
	oldNow.project = projB
	newUrgent := eligible(103)
	newUrgent.phase = "urgent"
	newUrgent.created = "2026-09-25T00:00:00"
	for _, s := range []taskSpec{newNow, oldNow, newUrgent} {
		seedTask(t, db, s)
	}
	pol := policies(map[string]projectPolicy{dirA: {enabled: true, cap: 3}, dirB: {enabled: true, cap: 3}})

	var order []int64
	for range 3 {
		pick, skip, err := selectCandidate(db, pol)
		if err != nil || skip != "" {
			t.Fatalf("skip=%q err=%v", skip, err)
		}
		order = append(order, pick.taskID)
		// Simulate the spawn's pre-claim: the task leaves `ready`.
		mustExec(t, db, `UPDATE tasks SET status = 'underway' WHERE id = ?`, pick.taskID)
	}
	if want := []int64{103, 102, 101}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v (urgent first, then oldest)", order, want)
	}
}

// TestSelect_Cap: auto-spawned tasks underway or unverified count against the
// cap; settled ones, and ones a person spawned, do not.
func TestSelect_Cap(t *testing.T) {
	db := newTestDB(t)
	dirA, dirB := seedProjects(t, db)
	seedTask(t, db, eligible(100))

	claim := func(id int64, status string, auto int) {
		s := eligible(id)
		s.status = status
		seedTask(t, db, s)
		mustExec(t, db, `INSERT INTO sessions (session_id, state, task_id, auto_spawned) VALUES (?, 'idle', ?, ?)`,
			"s"+string(rune('a'+id%26)), id, auto)
	}
	claim(200, "underway", 1)
	claim(201, "unverified", 1)
	claim(202, "confirmed", 1)  // settled: does not count
	claim(203, "revisit", 1)    // sent back: does not count
	claim(204, "unverified", 0) // a person's spawn: does not count

	if n, err := outstandingAutoSpawned(db, projA); err != nil || n != 2 {
		t.Fatalf("outstanding = %d (err %v), want 2", n, err)
	}

	pick, skip, err := selectCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 2}}))
	if err != nil {
		t.Fatal(err)
	}
	if pick.taskID != 0 || !strings.Contains(skip, "at its cap") {
		t.Errorf("at cap 2: pick=%d skip=%q, want a cap skip", pick.taskID, skip)
	}

	pick, _, err = selectCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 3}}))
	if err != nil || pick.taskID != 100 {
		t.Errorf("under cap 3: pick=%d err=%v, want E-100", pick.taskID, err)
	}

	// The cap is per project: a second opted-in project under its cap is still
	// served while the first is full.
	b := eligible(300)
	b.project = projB
	seedTask(t, db, b)
	pick, _, err = selectCandidate(db, policies(map[string]projectPolicy{
		dirA: {enabled: true, cap: 2}, dirB: {enabled: true, cap: 2},
	}))
	if err != nil || pick.taskID != 300 {
		t.Errorf("A full, B open: pick=%d err=%v, want E-300", pick.taskID, err)
	}
}

// TestSelect_SkipReasons pins the notes a person reads in `jobs list`.
func TestSelect_SkipReasons(t *testing.T) {
	db := newTestDB(t)
	dirA, _ := seedProjects(t, db)
	seedTask(t, db, eligible(100))

	_, skip, err := selectCandidate(db, policies(nil))
	if err != nil || !strings.HasPrefix(skip, "no project has opted in") {
		t.Errorf("nothing opted in: skip=%q err=%v", skip, err)
	}

	boom := errors.New("bad json")
	_, _, err = selectCandidate(db, func(string) (projectPolicy, error) { return projectPolicy{}, boom })
	if !errors.Is(err, boom) {
		t.Errorf("unreadable config: err=%v, want it surfaced as a failure", err)
	}

	mustExec(t, db, `UPDATE tasks SET phase = 'maybe'`)
	_, skip, err = selectCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 3}}))
	if err != nil || skip != "nothing eligible" {
		t.Errorf("nothing eligible: skip=%q err=%v", skip, err)
	}
}

// TestLoadProjectPolicy_DefaultsAndOptIn reads real config files.
func TestLoadProjectPolicy_DefaultsAndOptIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	dir := t.TempDir()
	p, err := loadProjectPolicy(dir)
	if err != nil || p.enabled {
		t.Fatalf("no config file: %+v err=%v, want off", p, err)
	}
	writeFile(t, filepath.Join(dir, ".endless", "config.json"), `{"auto_spawn":{"enabled":true}}`)
	p, err = loadProjectPolicy(dir)
	if err != nil || !p.enabled || p.cap != 3 {
		t.Errorf("enabled, no cap: %+v err=%v, want enabled with the default cap 3", p, err)
	}
}

// TestResolveTarget covers the three target outcomes and the misconfiguration.
func TestResolveTarget(t *testing.T) {
	prev := tmuxOutput
	t.Cleanup(func() { tmuxOutput = prev })

	tmuxOutput = func(args ...string) (string, error) {
		return "1700000100 $2\n1700000900 $5\n1700000500 $2", nil
	}
	target, skip, err := resolveTarget("active")
	if err != nil || skip != "" || target != "$5" {
		t.Errorf("active: target=%q skip=%q err=%v, want $5 (latest activity)", target, skip, err)
	}
	if target, _, _ = resolveTarget(""); target != "$5" {
		t.Errorf("unset target: %q, want the active default", target)
	}

	tmuxOutput = func(args ...string) (string, error) { return "", nil }
	_, skip, err = resolveTarget("active")
	if err != nil || !strings.HasPrefix(skip, "no tmux client is attached") {
		t.Errorf("no client: skip=%q err=%v", skip, err)
	}
	tmuxOutput = func(args ...string) (string, error) { return "", errors.New("no server running") }
	_, skip, _ = resolveTarget("active")
	if !strings.HasPrefix(skip, "no tmux client is attached") {
		t.Errorf("no server: skip=%q, want the no-client skip, not a failure", skip)
	}

	t.Setenv("TMUX_PANE", "%9")
	target, skip, err = resolveTarget("monitor")
	if err != nil || skip != "" || target != "" {
		t.Errorf("monitor in a pane: target=%q skip=%q err=%v, want spawn-window to resolve it", target, skip, err)
	}
	t.Setenv("TMUX_PANE", "")
	_, skip, _ = resolveTarget("monitor")
	if !strings.Contains(skip, "not in a tmux pane") {
		t.Errorf("monitor outside a pane: skip=%q", skip)
	}

	if _, _, err = resolveTarget("elsewhere"); err == nil {
		t.Error("an unknown target must be a failure, not a silent skip")
	}
}

func TestSpawnArgs(t *testing.T) {
	if got, want := spawnArgs(42, "$5", "last"), []string{"--no-session", "task", "spawn", "E-42", "--auto", "--to-last", "--target-session", "$5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("with target: %q, want %q", got, want)
	}
	if got, want := spawnArgs(42, "", "right"), []string{"--no-session", "task", "spawn", "E-42", "--auto", "--to-right"}; !reflect.DeepEqual(got, want) {
		t.Errorf("monitor target: %q, want %q", got, want)
	}
}

// TestRegisteredAndScheduled pins the wiring and the cadence read.
func TestRegisteredAndScheduled(t *testing.T) {
	if _, ok := jobs.Lookup(JobName); !ok {
		t.Fatalf("%q is not registered", JobName)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if got := (job{}).Schedule().Interval.String(); got != "5m0s" {
		t.Errorf("default interval = %s, want 5m0s", got)
	}
	writeFile(t, filepath.Join(home, ".config", "endless", "config.json"),
		`{"auto_spawn":{"interval":"90s"}}`)
	if got := (job{}).Schedule().Interval.String(); got != "1m30s" {
		t.Errorf("configured interval = %s, want 1m30s", got)
	}
	writeFile(t, filepath.Join(home, ".config", "endless", "config.json"),
		`{"auto_spawn":{"interval":"soon"}}`)
	if got := (job{}).Schedule().Interval.String(); got != "5m0s" {
		t.Errorf("unparsable interval = %s, want the 5m0s default", got)
	}
}

// TestLoadSettings_Placement: an auto-spawned window lands last unless the
// user's config says otherwise (E-2234).
func TestLoadSettings_Placement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if got := loadSettings().placement; got != "last" {
		t.Errorf("default placement = %q, want last", got)
	}
	writeFile(t, filepath.Join(home, ".config", "endless", "config.json"),
		`{"auto_spawn":{"placement":"left"}}`)
	if got := loadSettings().placement; got != "left" {
		t.Errorf("configured placement = %q, want left", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
