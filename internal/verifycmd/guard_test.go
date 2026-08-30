package verifycmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"
)

// mainDB stands up a fixture at the ONE path the guard reads — the deployed
// installation's database under the (temp) home — and seeds the two facts the
// refusal is built from. It deliberately does not go through monitor.DB(): the
// guard opens the real database directly, and a test that mocked that away
// would prove nothing about the routing the guard exists to ignore.
func mainDB(t *testing.T, landed []int64, sessions map[int64]int64) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "endless.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE task_landings (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL);
		CREATE TABLE sessions (id INTEGER PRIMARY KEY, task_id INTEGER);`)
	if err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	for _, id := range landed {
		if _, err = db.Exec("INSERT INTO task_landings (task_id) VALUES (?)", id); err != nil {
			t.Fatalf("seed landing: %v", err)
		}
	}
	for sid, tid := range sessions {
		if _, err = db.Exec("INSERT INTO sessions (id, task_id) VALUES (?, ?)", sid, tid); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
}

// worktreeRoot builds a path shaped like a real task worktree, which is what
// makes the checkout itself name its task.
func worktreeRoot(t *testing.T, taskNum int) dt.DirPath {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".endless", "worktrees", fmt.Sprintf("e-%d", taskNum))
	if err := os.MkdirAll(filepath.Join(root, ".endless"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dt.DirPath(root)
}

// THE REGRESSION THIS MUST NEVER CAUSE. A task that landed, was reopened, and is
// being worked again is the claiming session's own task, so the refusal must not
// fire on it. A predicate keyed on landed-ness alone would break re-verifying
// and re-landing your own work, which is the documented path when shipped work
// turns out wrong.
func TestGuard_AllowsYourOwnLandedTaskFromItsWorktree(t *testing.T) {
	mainDB(t, []int64{2023}, nil)
	t.Setenv("ENDLESS_SESSION_ID", "")

	if err := guardOwnTaskOnly("E-2023", worktreeRoot(t, 2023)); err != nil {
		t.Fatalf("refused a session's own reopened task: %v", err)
	}
}

func TestGuard_RefusesAForeignLandedTask(t *testing.T) {
	mainDB(t, []int64{1603}, nil)
	t.Setenv("ENDLESS_SESSION_ID", "")

	err := guardOwnTaskOnly("E-1603", worktreeRoot(t, 2023))
	if err == nil {
		t.Fatal("a landed task's suite was allowed from another task's worktree")
	}
	if !errors.Is(err, ErrForeignLandedSuite) {
		t.Fatalf("error does not match the refusal sentinel: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"E-1603", "E-2023", "endless task verify E-2023", ".endless/tasks/CLAUDE.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// An UNLANDED foreign task is live work whose result still means something to
// whoever is looking at it. The refusal is about landed suites; widening it
// would block ordinary collaboration.
func TestGuard_AllowsAnUnlandedForeignTask(t *testing.T) {
	mainDB(t, nil, nil)
	t.Setenv("ENDLESS_SESSION_ID", "")

	if err := guardOwnTaskOnly("E-1603", worktreeRoot(t, 2023)); err != nil {
		t.Fatalf("refused an unlanded task: %v", err)
	}
}

// The session's own task is an owner even when the checkout is not a worktree —
// the `esu`-exported ENDLESS_SESSION_ID names it.
func TestGuard_SessionEnvNamesAnOwner(t *testing.T) {
	mainDB(t, []int64{1603}, map[int64]int64{42: 1603})
	t.Setenv("ENDLESS_SESSION_ID", "42")

	if err := guardOwnTaskOnly("E-1603", dt.DirPath(t.TempDir())); err != nil {
		t.Fatalf("refused the session's own task: %v", err)
	}
}

// Fail OPEN when the question cannot be answered. A fresh install, or a suite
// running under the runner's own isolated HOME, has no main database to read;
// refusing there would make a task unverifiable on the machine where its suite
// was written.
func TestGuard_FailsOpenWithNoMainDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ENDLESS_SESSION_ID", "")

	if err := guardOwnTaskOnly("E-1603", worktreeRoot(t, 2023)); err != nil {
		t.Fatalf("refused when the main database was unreadable: %v", err)
	}
}

// An id the guard cannot parse is not a refusal — it is a request it has no
// opinion about. Discovery reports the real problem a moment later.
func TestGuard_IgnoresANonTaskID(t *testing.T) {
	mainDB(t, []int64{1603}, nil)
	if err := guardOwnTaskOnly("E-PASS", worktreeRoot(t, 2023)); err != nil {
		t.Fatalf("refused a non-numeric suite id: %v", err)
	}
}

// The E-2071 shape: a loop over the suite directory executing what it finds.
// Every landed foreign suite must refuse, and the session's own must not — the
// loop cannot reach a single foreign suite, whatever order it walks in.
func TestGuard_RefusesEveryForeignSuiteInADirectorySweep(t *testing.T) {
	mainDB(t, []int64{1202, 1603, 2023}, nil)
	t.Setenv("ENDLESS_SESSION_ID", "")
	root := worktreeRoot(t, 2023)

	refused, allowed := 0, 0
	for _, id := range []string{"E-1202", "E-1603", "E-2023"} {
		if err := guardOwnTaskOnly(id, root); err != nil {
			refused++
			continue
		}
		allowed++
	}
	if refused != 2 || allowed != 1 {
		t.Errorf("sweep refused %d and allowed %d, want 2 refused and only E-2023 allowed", refused, allowed)
	}
}

func TestTaskNumber(t *testing.T) {
	cases := map[string]struct {
		want    int64
		wantErr bool
	}{
		"E-1603": {1603, false},
		"e-1603": {1603, false},
		"1603":   {1603, false},
		"E-PASS": {0, true},
		"":       {0, true},
		"E-0":    {0, true},
	}
	for in, tc := range cases {
		got, err := taskNumber(in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("taskNumber(%q) = %d, want an error", in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("taskNumber(%q) = %d, %v; want %d, nil", in, got, err, tc.want)
		}
	}
}
