// Binary-integration tests for the apply-first emit and `event void` (E-2578),
// through the real endless-go binary against a fresh DB and a real git repo.
package eventcmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
)

// personEnv is the test process's environment with every agent-harness
// variable removed, so the child runs as a person would. The tests themselves
// often run under Claude Code, whose CLAUDE_CODE_ENTRYPOINT=cli would
// otherwise make every child an agent.
func personEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "CLAUDECODE", name == "AI_AGENT", name == "__CFBundleIdentifier",
			strings.HasPrefix(name, "CLAUDE_"):
			continue
		}
		env = append(env, kv)
	}
	return env
}

// agentEnv is personEnv plus the variable that marks a Claude Code terminal.
func agentEnv() []string {
	return append(personEnv(), "CLAUDE_CODE_ENTRYPOINT=cli")
}

// runGo runs endless-go with args and env, returning combined output and the
// exit code.
func runGo(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(endlessGoBin(t), args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run endless-go %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), code
}

// emitArgs is the argv of one `event emit` against cfgDir and projectRoot.
func emitArgs(cfgDir, projectRoot, project, kind, entityID, payload string) []string {
	return []string{"--db-dir", cfgDir, "event", "emit",
		"--kind", kind, "--project", project,
		"--entity-type", string(events.EntityTask), "--entity-id", entityID,
		"--actor-kind", string(events.ActorCLI), "--actor-id", "test",
		"--node-id", "a7f3", "--project-root", projectRoot, "--payload", payload}
}

func gitLog(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "log", "--format=%s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "does not have any commits") {
			return nil
		}
		t.Fatalf("git log: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// The 2026-11-08 drift, closed: an emit whose apply fails — here a
// task.landed for a task that does not exist, refused at its INSERT — exits
// non-zero with the apply's error, writes no ledger line, makes no commit and
// changes nothing in the database.
func TestEmit_FailedApplyLeavesNothing(t *testing.T) {
	cfgDir, projectRoot := t.TempDir(), t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)
	initGitRepo(t, projectRoot)

	out, code := runGo(t, personEnv(), emitArgs(cfgDir, projectRoot, "proj-void", string(events.KindTaskLanded), "999",
		`{"base_branch":"main","merge_commit_sha":"abc123"}`)...)
	if code == 0 {
		t.Fatalf("emit of a landing for a task that does not exist exited 0:\n%s", out)
	}
	if !strings.Contains(out, "insert task_landing") {
		t.Errorf("output does not carry the apply's error:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".endless", events.LedgerDirName)); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(projectRoot, ".endless", events.LedgerDirName))
		if len(entries) != 0 {
			t.Errorf("ledger directory holds %d files after a failed apply; want none", len(entries))
		}
	}
	if log := gitLog(t, projectRoot); len(log) != 0 {
		t.Errorf("git log = %v after a failed apply; want no commit", log)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM task_landings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("task_landings = %d rows, want 0", n)
	}
}

// A successful emit writes the line and commits the segment, after the apply.
func TestEmit_SuccessWritesLineAndCommits(t *testing.T) {
	cfgDir, projectRoot := t.TempDir(), t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)
	initGitRepo(t, projectRoot)
	seedTaskRow(t, dbPath, "proj-void", 1, "one")

	out, code := runGo(t, personEnv(), emitArgs(cfgDir, projectRoot, "proj-void", string(events.KindTaskStatusChanged), "1",
		`{"old_status":"ready","new_status":"underway"}`)...)
	if code != 0 {
		t.Fatalf("emit exited %d:\n%s", code, out)
	}
	read, err := events.ReadAllEvents(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1 || read[0].Kind != events.KindTaskStatusChanged {
		t.Fatalf("ledger = %+v, want one task.status_changed", read)
	}
	if log := gitLog(t, projectRoot); len(log) != 1 || !strings.HasPrefix(log[0], "Endless: task.status_changed E-1") {
		t.Errorf("git log = %v", log)
	}
}

// emit refuses ledger.voided from the command line, as it refuses the
// correction kinds.
func TestEmit_RefusesLedgerVoided(t *testing.T) {
	cfgDir, projectRoot := t.TempDir(), t.TempDir()
	initSchemaDB(t, cfgDir)
	out, code := runGo(t, personEnv(), emitArgs(cfgDir, projectRoot, "proj-void", string(events.KindLedgerVoided), "1",
		`{"segment":"x","refs":[0]}`)...)
	if code == 0 || !strings.Contains(out, "never emitted directly") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".endless")); !os.IsNotExist(err) {
		t.Errorf("a refused emit wrote to the project")
	}
}

// voidArgs is the argv of one `event void`.
func voidArgs(cfgDir, projectRoot, segment, reason string, offsets ...int64) []string {
	args := []string{"--db-dir", cfgDir, "event", "void", segment}
	for _, off := range offsets {
		args = append(args, "--offset", fmt.Sprintf("%d", off))
	}
	return append(args, "--reason", reason, "--project", "proj-void",
		"--project-root", projectRoot, "--node-id", "a7f3", "--actor-id", "mike")
}

// seededLedger emits a status change for a seeded task and returns the segment
// and offset of its line, for voiding.
func seededLedger(t *testing.T) (cfgDir, projectRoot, dbPath, segment string, offset int64) {
	t.Helper()
	cfgDir, projectRoot = t.TempDir(), t.TempDir()
	dbPath = initSchemaDB(t, cfgDir)
	initGitRepo(t, projectRoot)
	seedTaskRow(t, dbPath, "proj-void", 1, "one")
	out, code := runGo(t, personEnv(), emitArgs(cfgDir, projectRoot, "proj-void", string(events.KindTaskStatusChanged), "1",
		`{"old_status":"ready","new_status":"underway"}`)...)
	if code != 0 {
		t.Fatalf("seed emit exited %d:\n%s", code, out)
	}
	read, err := events.ReadAllEvents(projectRoot)
	if err != nil || len(read) != 1 {
		t.Fatalf("ledger after seed: %v, %d events", err, len(read))
	}
	return cfgDir, projectRoot, dbPath, read[0].Segment, read[0].Offset
}

// `event void` is the user's: an agent is refused before anything is read or
// written.
func TestVoid_RefusesAgent(t *testing.T) {
	cfgDir, projectRoot, _, segment, offset := seededLedger(t)
	out, code := runGo(t, agentEnv(), voidArgs(cfgDir, projectRoot, segment, "test", offset)...)
	if code == 0 || !strings.Contains(out, "the user's command") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	read, err := events.ReadAllEvents(projectRoot)
	if err != nil || len(read) != 1 {
		t.Errorf("ledger after an agent's refused void: %v, %d events (want 1)", err, len(read))
	}
}

// A person's `event void` appends a ledger.voided line naming the segment and
// offsets, commits it under a subject that names the voiding, and the
// projection then skips the voided line.
func TestVoid_AppendsVoidingAndProjectionSkipsIt(t *testing.T) {
	cfgDir, projectRoot, _, segment, offset := seededLedger(t)

	out, code := runGo(t, personEnv(), voidArgs(cfgDir, projectRoot, segment, "E-2578 test: never applied", offset)...)
	if code != 0 {
		t.Fatalf("void exited %d:\n%s", code, out)
	}
	var result struct {
		Kind          string  `json:"kind"`
		Segment       string  `json:"segment"`
		VoidedSegment string  `json:"voided_segment"`
		Refs          []int64 `json:"refs"`
	}
	line := out[strings.Index(out, "{"):]
	line = line[:strings.LastIndex(line, "}")+1]
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if result.Kind != string(events.KindLedgerVoided) || result.VoidedSegment != segment || len(result.Refs) != 1 || result.Refs[0] != offset {
		t.Errorf("result = %+v", result)
	}

	read, err := events.ReadAllEvents(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 || read[1].Kind != events.KindLedgerVoided {
		t.Fatalf("ledger = %+v, want the change then its voiding", read)
	}
	var p events.VoidedPayload
	if err := json.Unmarshal(read[1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Segment != segment || len(p.Refs) != 1 || p.Refs[0] != offset || p.Reason != "E-2578 test: never applied" {
		t.Errorf("void payload = %+v", p)
	}
	if read[1].Actor.ID != "mike" || read[1].Actor.Harness != "" {
		t.Errorf("void actor = %+v, want the person, no harness", read[1].Actor)
	}
	log := gitLog(t, projectRoot)
	if len(log) != 2 || !strings.HasPrefix(log[0], "Endless: ledger.voided E-1 E-2578 test") {
		t.Errorf("git log = %v; want the voiding's own commit, unfolded, leading with the voiding", log)
	}

	tempPath, res, err := events.ProjectToTempDB(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempPath)
	if res.EventsSkipped != 1 {
		t.Errorf("projection skipped %d, want 1", res.EventsSkipped)
	}
	out, code = runGo(t, personEnv(), "--db-dir", cfgDir, "event", "validate-db", "--project-root", projectRoot)
	if !strings.Contains(out, "1 skipped as voided") {
		t.Errorf("validate-db does not report the skip (exit %d):\n%s", code, out)
	}
}

// An offset that does not start an event line is refused, with nothing written.
func TestVoid_RefusesBadOffset(t *testing.T) {
	cfgDir, projectRoot, _, segment, offset := seededLedger(t)
	out, code := runGo(t, personEnv(), voidArgs(cfgDir, projectRoot, segment, "test", offset+3)...)
	if code == 0 || !strings.Contains(out, "does not start a line") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	out, code = runGo(t, personEnv(), voidArgs(cfgDir, projectRoot, segment, "", offset)...)
	if code == 0 || !strings.Contains(out, "--reason is required") {
		t.Errorf("no reason: exit %d, output:\n%s", code, out)
	}
	if read, _ := events.ReadAllEvents(projectRoot); len(read) != 1 {
		t.Errorf("a refused void wrote %d lines", len(read)-1)
	}
}
