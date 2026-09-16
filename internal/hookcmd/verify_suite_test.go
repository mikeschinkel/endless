package hookcmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The landed-suite gate (E-1916), tested at the two pure predicates and the
// message they feed. The block functions themselves call os.Exit, so what is
// unit-testable is the decision that precedes it — the same split as
// TestSqliteEndlessRe, TestPlanFileRe and TestWorktreeRemovalRes.
//
// These tests are the DURABLE half of this task. The rule they protect has been
// broken five times across two sessions, and every one of those sessions had
// read it in prose first; a predicate that quietly stops matching would restore
// exactly that state while the gate still looks installed.

// TestSuiteTaskFromPath is Arm 1's matcher: which task, if any, owns the file
// this Write/Edit is aimed at.
//
// The NOT-matched table is where this gate is most easily got wrong, and two
// entries in it are load-bearing in opposite directions. The project-level
// `.endless/verify.toml` shares a filename with a task manifest one level down
// and composes beneath every one of them; blocking it would make the shared
// setup uneditable. A path a manifest POINTS AT is the project's own durable
// test, and blocking those would invert the whole rule — "coverage that must
// survive belongs in the project's test suite" only works while that suite
// stays freely editable.
func TestSuiteTaskFromPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int64
	}{
		// Both suite forms, in every spelling a path arrives in.
		{"script, repo-relative", ".endless/tasks/e-1916/verify.sh", 1916},
		{"manifest, repo-relative", ".endless/tasks/e-1916/verify.toml", 1916},
		{"dot-slash prefix", "./.endless/tasks/e-101/verify.sh", 101},
		{"absolute", "/Users/x/proj/.endless/tasks/e-999/verify.toml", 999},
		// An uppercase suite directory still RESOLVES — verify.Discover
		// compares ids through NormalizeTaskID — so the gate has to see it too,
		// or it goes blind on exactly the directories that predate the rename.
		{"uppercase suite dir", ".endless/tasks/E-1889/verify.sh", 1889},
		{"mixed case", ".endless/Tasks/E-1889/verify.sh", 1889},
		// The suite of one task, sitting inside another task's checkout. This
		// is the shape of the incident: the path names E-101 whichever worktree
		// it is read from.
		{"foreign suite inside a worktree", "/p/.endless/worktrees/e-102/.endless/tasks/e-101/verify.sh", 101},
		// Everything in a suite directory is the record, not just the two
		// filenames. A fixture retrofitted is a record rewritten.
		{"a fixture beside the suite", ".endless/tasks/e-1568/fixtures/expected.json", 1568},
		{"a nested helper", ".endless/tasks/e-1568/lib/helpers.sh", 1568},

		// NOT a per-task suite.
		{"project-level verify config", ".endless/verify.toml", 0},
		{"project-level setup script", ".endless/verify/setup.sh", 0},
		{"the shared harness", ".endless/tasks/_harness.sh", 0},
		{"the shared guard", ".endless/tasks/_guard.sh", 0},
		{"the directory's own rules", ".endless/tasks/CLAUDE.md", 0},
		{"the suite directory itself", ".endless/tasks/e-1916", 0},
		// What a manifest's [[check]] entries name: the project's durable
		// tests. These must stay editable forever.
		{"a gotest a manifest points at", "internal/monitor/verify_ownership_test.go", 0},
		{"a pytest a manifest points at", "tests/test_suite_guard.py", 0},
		// Neighbours and near-misses.
		{"plan mirror", ".endless/plans/E-1916.md", 0},
		{"a worktree path, not a suite", ".endless/worktrees/e-1916/main.go", 0},
		{"not a path component", "my.endless/tasks/e-1/verify.sh", 0},
		{"wrong dir name", ".endless/taskss/e-1/verify.sh", 0},
		{"no digits", ".endless/tasks/e-/verify.sh", 0},
		{"not an e- prefix", ".endless/tasks/x-1/verify.sh", 0},
		{"the retired script layout", "tests/tasks/e-1568-verify.sh", 0},
		{"ordinary source", "internal/hookcmd/claude.go", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := suiteTaskFromPath(tc.path); got != tc.want {
				t.Errorf("suiteTaskFromPath(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

// TestSuiteTaskFromCommand is Arm 2's matcher: the four direct-execution shapes
// the plan names, and the far longer list of commands that merely MENTION a
// suite path and are ordinary work.
//
// The allowed table is what keeps the gate usable. Reading a landed suite is
// explicitly permitted — the rule is against running and editing — so `cat`,
// `grep`, `git add` and `wc` over one of these paths must all pass. And the
// rule's own documentation quotes these invocations in order to forbid them, so
// a session writing or grepping that documentation must not be blocked either.
func TestSuiteTaskFromCommand(t *testing.T) {
	blocked := map[string]int64{
		// The four shapes: ./x, a bare relative path (bash runs anything
		// containing a slash), an interpreter, and an absolute path.
		"./.endless/tasks/e-101/verify.sh":                  101,
		".endless/tasks/e-101/verify.sh":                    101,
		"bash .endless/tasks/e-101/verify.sh":               101,
		"sh .endless/tasks/e-101/verify.sh":                 101,
		"/Users/x/proj/.endless/tasks/e-101/verify.sh":      101,
		"/bin/bash /Users/x/p/.endless/tasks/e-7/verify.sh": 7,
		"zsh .endless/tasks/e-7/verify.sh":                  7,
		"sh -x .endless/tasks/e-101/verify.sh":              101,
		"bash -e -u .endless/tasks/e-101/verify.sh":         101,
		"../.endless/tasks/e-101/verify.sh":                 101,
		// Arguments and redirection do not change what is being run.
		"./.endless/tasks/e-101/verify.sh --verbose":      101,
		"./.endless/tasks/e-101/verify.sh 2>&1 | tail -5": 101,
		// After a separator, which is how a run usually arrives.
		"cd /tmp && ./.endless/tasks/e-101/verify.sh":        101,
		"cd /tmp; bash .endless/tasks/e-101/verify.sh":       101,
		"git status | cat\n./.endless/tasks/e-101/verify.sh": 101,
		"   ./.endless/tasks/e-101/verify.sh":                101,
		// Uppercase directory, same as Arm 1.
		"./.endless/tasks/E-101/verify.sh": 101,
		// A helper split out beside the suite is still the suite.
		"bash .endless/tasks/e-101/lib/setup.sh": 101,
	}
	for cmd, want := range blocked {
		if got := suiteTaskFromCommand(cmd); got != want {
			t.Errorf("expected BLOCK E-%d, got %d: %q", want, got, cmd)
		}
	}

	allowed := []string{
		// Reading a landed suite is allowed. Only running it is not.
		"cat .endless/tasks/e-101/verify.sh",
		"head -40 .endless/tasks/e-101/verify.sh",
		"grep -n summary .endless/tasks/e-101/verify.sh",
		"wc -l .endless/tasks/e-101/verify.sh",
		"git add .endless/tasks/e-101/verify.sh",
		"git log --oneline .endless/tasks/e-101/verify.sh",
		"ls -la .endless/tasks/e-101/",
		"chmod +x .endless/tasks/e-101/verify.sh",
		// MENTIONING a run is not performing one, and this gate's own rule is
		// documented by quoting exactly these strings.
		"echo 'never run ./.endless/tasks/e-101/verify.sh'",
		`echo "do not run bash .endless/tasks/e-101/verify.sh"`,
		"grep -rn './.endless/tasks/e-101/verify.sh' docs/",
		`git commit -m "block ./.endless/tasks/e-101/verify.sh at the tool layer"`,
		// The runner is the front door and stays open. E-2023 refuses a foreign
		// landed suite inside it, where the id is an argument rather than a
		// guess made from a command string; duplicating that here is the one
		// clause this plan deliberately dropped.
		"endless task verify E-101",
		"endless task verify",
		"just verify E-101",
		// Not a per-task suite, so not this gate's business.
		"bash .endless/tasks/_harness.sh",
		"bash .endless/verify/setup.sh",
		"./scripts/build.sh",
		"bash tests/tasks/e-101-verify.sh",
		// Not a path component.
		"./my.endless/tasks/e-101/verify.sh",
		// Not a shell script.
		"cat .endless/tasks/e-101/verify.toml",
	}
	for _, cmd := range allowed {
		if got := suiteTaskFromCommand(cmd); got != 0 {
			t.Errorf("expected ALLOW, got block E-%d: %q", got, cmd)
		}
	}
}

// TestSuiteCallerHolds pins the carve-out that makes the gate survivable: the
// conjunction is landed AND foreign, so a session's own task is never refused
// its own suite — including a task that landed and was then REOPENED, which is
// once again the claiming session's task and whose suite has to be runnable and
// authorable again.
func TestSuiteCallerHolds(t *testing.T) {
	var c suiteCaller
	if c.holds(1916) {
		t.Error("an empty caller holds nothing")
	}

	c.add(1916, "this session's claimed task")
	c.add(1916, "the worktree at /p/.endless/worktrees/e-1916")
	if len(c.tasks) != 1 {
		t.Errorf("the same task from two sources is one entry, got %v", c.tasks)
	}
	if !c.holds(1916) {
		t.Error("a session holds its own claimed task")
	}
	if c.holds(101) {
		t.Error("it does not hold a task nobody named")
	}

	// A second, different source is a second entry — the sources disagree only
	// when a stale export meets a live checkout, and BOTH are then allowed.
	c.add(101, "session ES-9 (ENDLESS_SESSION_ID)")
	if !c.holds(101) || !c.holds(1916) {
		t.Errorf("both sources must be honoured, got %v", c.tasks)
	}
	c.add(0, "nothing")
	c.add(-3, "nonsense")
	if len(c.tasks) != 2 {
		t.Errorf("a non-task is not a task, got %v", c.tasks)
	}
}

// TestLandedSuiteRefusal pins the block-response shape. The two refusals are
// deliberately DISTINCT: a run wants a result and an edit wants an assertion
// changed, and the reason each is refused differs with the motive. One message
// serving both is how a refusal reads as boilerplate.
func TestLandedSuiteRefusal(t *testing.T) {
	var mine suiteCaller
	mine.add(1916, "this session's claimed task")

	edit := landedSuiteRefusal(landedSuiteEdit, 101, mine)
	run := landedSuiteRefusal(landedSuiteRun, 101, mine)

	for name, msg := range map[string]string{"edit": edit, "run": run} {
		t.Run(name, func(t *testing.T) {
			// Claude Code shows stderr from an exit-2 hook to the agent; the
			// BLOCKED prefix is how every other gate in this file announces
			// itself, and the agent reads the first line.
			if !strings.HasPrefix(msg, "BLOCKED: refusing to "+name+" E-101's verification suite") {
				t.Errorf("first line does not name the act and the task:\n%s", msg)
			}
			// Both halves of the conjunction, so the agent can tell this from a
			// generic "not your worktree" refusal.
			if !strings.Contains(msg, "it has landed, and it is not yours") {
				t.Error("the refusal does not state its own predicate")
			}
			// Show the work: whose task this is, and whose it is not.
			if !strings.Contains(msg, "E-1916 (this session's claimed task)") {
				t.Error("the refusal does not name what IS the session's")
			}
			if !strings.Contains(msg, "E-101 (landed)") {
				t.Error("the refusal does not name the requested task as landed")
			}
			// Name a command that works from the state that produced the
			// refusal, and the alternative that preserves the coverage.
			if !strings.Contains(msg, "endless task verify E-1916") {
				t.Error("the refusal does not name a command that works")
			}
			if !strings.Contains(msg, "project's own test") {
				t.Error("the refusal does not name where durable coverage goes")
			}
			if !strings.Contains(msg, ".endless/tasks/CLAUDE.md") {
				t.Error("the refusal does not point at the rules in full")
			}
			// No bypass is offered, here or anywhere: a session that can talk
			// itself into a violation can talk itself into the exception.
			for _, bypass := range []string{"--force", "--no-verify", "override"} {
				if strings.Contains(msg, bypass) {
					t.Errorf("the refusal offers a bypass: %q", bypass)
				}
			}
		})
	}

	if strings.Contains(edit, "pass OR fail") {
		t.Error("the edit refusal reuses the run refusal's reason")
	}
	if !strings.Contains(edit, "rewrites that record") {
		t.Error("the edit refusal does not say what an edit costs")
	}
	if !strings.Contains(run, "says nothing about your work") {
		t.Error("the run refusal does not say what a landed result is worth")
	}

	// A session that holds nothing still gets a usable message rather than a
	// dangling "yours: ".
	none := landedSuiteRefusal(landedSuiteRun, 101, suiteCaller{})
	if !strings.Contains(none, "yours:     nothing") {
		t.Errorf("no-task refusal does not say so:\n%s", none)
	}
	if strings.Contains(none, "endless task verify E-0") {
		t.Error("no-task refusal names a nonexistent task")
	}
}

// seedLandings points HOME at a scratch directory holding a main database with
// just the one table the gate reads, and returns nothing: the point is the
// environment. The same shape monitor's own guard tests use — a real SQLite
// file rather than a fake, because what is being tested is that the gate asks
// the main database and believes the answer.
func seedLandings(t *testing.T, landed ...int64) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "endless.db"))
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE task_landings (id INTEGER PRIMARY KEY, task_id INTEGER)"); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	for _, id := range landed {
		if _, err = db.Exec("INSERT INTO task_landings (task_id) VALUES (?)", id); err != nil {
			t.Fatalf("seed landing E-%d: %v", id, err)
		}
	}
}

// stubSessionTask replaces the session half of the ownership question, so the
// conjunction can be driven without standing up a sessions table and the whole
// schema behind it.
func stubSessionTask(t *testing.T, taskID int64) {
	t.Helper()
	prev := sessionClaimedTask
	sessionClaimedTask = func(string) (int64, bool) { return taskID, taskID > 0 }
	t.Cleanup(func() { sessionClaimedTask = prev })
}

// TestForeignLandedSuite drives the conjunction against a real main database.
// These four rows ARE the gate; the regexes above only decide which task to ask
// about.
func TestForeignLandedSuite(t *testing.T) {
	// ENDLESS_SESSION_ID is inherited from whatever shell ran `go test`, and on
	// this project that shell has usually run `esu`. Cleared so the table below
	// measures what it says it measures.
	t.Setenv("ENDLESS_SESSION_ID", "")

	t.Run("landed and not mine blocks", func(t *testing.T) {
		seedLandings(t, 101)
		stubSessionTask(t, 1916)
		mine, foreign := foreignLandedSuite(claudePayload{SessionID: "s"}, 101)
		if !foreign {
			t.Fatal("a landed, foreign suite was allowed")
		}
		if !mine.holds(1916) {
			t.Errorf("the refusal cannot name what IS mine: %v", mine.tasks)
		}
	})

	t.Run("landed and mine allows", func(t *testing.T) {
		// The reopened-task case: E-101 landed, was set back to `revisit`, and
		// is once again this session's claimed task. Re-verifying and
		// re-landing your own reopened work is the documented path when shipped
		// work turns out wrong, so landed-ness alone must not refuse it.
		seedLandings(t, 101)
		stubSessionTask(t, 101)
		if _, foreign := foreignLandedSuite(claudePayload{SessionID: "s"}, 101); foreign {
			t.Fatal("a session was refused its own reopened task's suite")
		}
	})

	t.Run("unlanded allows", func(t *testing.T) {
		// Live work. Its suite is being written right now, by somebody, and its
		// result means something to whoever is looking at it.
		seedLandings(t)
		stubSessionTask(t, 1916)
		if _, foreign := foreignLandedSuite(claudePayload{SessionID: "s"}, 101); foreign {
			t.Fatal("an unlanded suite was refused")
		}
	})

	t.Run("no main database fails open", func(t *testing.T) {
		// A fresh install, or a suite running under the verify runner's own
		// isolated HOME. The gate stops a mistake somebody is making; it is not
		// a precondition for working, and a machine it cannot question is not a
		// machine it may refuse.
		t.Setenv("HOME", t.TempDir())
		stubSessionTask(t, 1916)
		if _, foreign := foreignLandedSuite(claudePayload{SessionID: "s"}, 101); foreign {
			t.Fatal("refused on a question it could not ask")
		}
	})

	t.Run("the worktree names an owner even with no session row", func(t *testing.T) {
		// An agent's shell carries no ENDLESS_SESSION_ID, and a session row can
		// fail to resolve. The checkout still says whose work this is, and that
		// is the source the incident behind E-2023 could not fake.
		seedLandings(t, 101)
		stubSessionTask(t, 0)
		payload := claudePayload{
			SessionID: "s",
			CWD:       filepath.Join(t.TempDir(), ".endless", "worktrees", "e-101", "internal"),
		}
		if _, foreign := foreignLandedSuite(payload, 101); foreign {
			t.Fatal("a session standing in E-101's worktree was refused E-101's suite")
		}
	})
}

// TestLandedSuiteDecisions drives each arm from the payload the harness
// actually delivers, so the plumbing between the tool input and the predicate
// is covered too — which field of which tool carries the target, and what a
// payload that will not parse means.
func TestLandedSuiteDecisions(t *testing.T) {
	t.Setenv("ENDLESS_SESSION_ID", "")

	write := func(tool, path string) claudePayload {
		field := "file_path"
		if tool == "NotebookEdit" {
			field = "notebook_path"
		}
		return claudePayload{
			SessionID: "s",
			ToolName:  tool,
			ToolInput: []byte(`{"` + field + `":"` + path + `"}`),
		}
	}
	bash := func(cmd string) claudePayload {
		return claudePayload{
			SessionID: "s",
			ToolName:  "Bash",
			ToolInput: []byte(`{"command":"` + cmd + `"}`),
		}
	}

	t.Run("arm 1 refuses each write tool", func(t *testing.T) {
		seedLandings(t, 101)
		stubSessionTask(t, 1916)
		for _, tool := range []string{"Write", "Edit", "NotebookEdit"} {
			msg, block := landedSuiteEditDecision(write(tool, ".endless/tasks/e-101/verify.sh"))
			if !block {
				t.Errorf("%s of a landed foreign suite was allowed", tool)
				continue
			}
			if !strings.Contains(msg, "refusing to edit E-101") {
				t.Errorf("%s: wrong refusal:\n%s", tool, msg)
			}
		}
	})

	t.Run("arm 1 allows the neighbours", func(t *testing.T) {
		seedLandings(t, 101)
		stubSessionTask(t, 1916)
		for _, path := range []string{
			".endless/verify.toml",                 // the shared project layer
			".endless/tasks/_harness.sh",           // belongs to no task
			".endless/tasks/e-1916/verify.sh",      // this session's own
			"internal/monitor/verify_ownership.go", // a durable test's subject
		} {
			if _, block := landedSuiteEditDecision(write("Edit", path)); block {
				t.Errorf("an edit of %s was refused", path)
			}
		}
	})

	t.Run("arm 2 refuses a direct run and allows a read", func(t *testing.T) {
		seedLandings(t, 101)
		stubSessionTask(t, 1916)
		msg, block := landedSuiteRunDecision(bash("./.endless/tasks/e-101/verify.sh"))
		if !block {
			t.Fatal("a direct run of a landed foreign suite was allowed")
		}
		if !strings.Contains(msg, "refusing to run E-101") {
			t.Errorf("wrong refusal:\n%s", msg)
		}
		if _, block = landedSuiteRunDecision(bash("cat .endless/tasks/e-101/verify.sh")); block {
			t.Error("reading a landed suite was refused; only running it is forbidden")
		}
		// The runner is E-2023's to refuse, where the id is an argument rather
		// than a guess. This arm must not double it.
		if _, block = landedSuiteRunDecision(bash("endless task verify E-101")); block {
			t.Error("the hook duplicated the runner's own refusal")
		}
	})

	t.Run("an unreadable tool input is not a refusal", func(t *testing.T) {
		seedLandings(t, 101)
		stubSessionTask(t, 1916)
		bad := claudePayload{SessionID: "s", ToolName: "Bash", ToolInput: []byte(`not json`)}
		if _, block := landedSuiteRunDecision(bad); block {
			t.Error("refused a payload it could not read")
		}
		if _, block := landedSuiteEditDecision(bad); block {
			t.Error("refused a payload it could not read")
		}
	})
}
