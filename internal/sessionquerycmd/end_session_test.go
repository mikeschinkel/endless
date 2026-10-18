package sessionquerycmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestEndSession_EndsTheRowAndReleasesItsLock pins the write behind `endless
// session end` (E-2394): the named row is ended, its worktree lock is released
// as the SessionEnd hook releases it, and no other session is touched.
func TestEndSession_EndsTheRowAndReleasesItsLock(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, []sessionSeed{
		{"ghost-uuid", "working", "%91"},
		{"other-uuid", "working", "%92"},
	})
	ghostID, _, _, _ := readSessionsRow(t, cfgDir, "ghost-uuid")

	lockDir := filepath.Join(projectPath, ".endless", "worktrees", "e-1", ".endless")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(lockDir, "worktree.lock")
	if err := os.WriteFile(lock, []byte(`{"session_id":"ghost-uuid","pid":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := endlessGoBin(t)
	out, err := exec.Command(bin, "--db-dir", cfgDir,
		"session-query", "end-session", "--id", strconv.FormatInt(ghostID, 10)).CombinedOutput()
	if err != nil {
		t.Fatalf("end-session failed: %v\n%s", err, out)
	}
	if _, state, _, _ := readSessionsRow(t, cfgDir, "ghost-uuid"); state != "ended" {
		t.Errorf("ghost state = %q, want ended", state)
	}
	if _, state, _, _ := readSessionsRow(t, cfgDir, "other-uuid"); state != "working" {
		t.Errorf("other session state = %q, want it untouched (working)", state)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("worktree lock still present after end (stat err %v)", err)
	}
}

// TestEndSession_UnknownIDRefuses: an id with no row is the caller's to fix.
func TestEndSession_UnknownIDRefuses(t *testing.T) {
	cfgDir := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, t.TempDir(), nil)
	out, err := exec.Command(endlessGoBin(t), "--db-dir", cfgDir,
		"session-query", "end-session", "--id", "999").CombinedOutput()
	if err == nil {
		t.Fatalf("expected a refusal for an unknown id, got success\n%s", out)
	}
}

// TestResumeTarget_CarriesLastActivity: `session end` reads last_activity from
// the resume target to refuse a recently active session (E-2394).
func TestResumeTarget_CarriesLastActivity(t *testing.T) {
	cfgDir := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, t.TempDir(), []sessionSeed{{"ra-uuid", "idle", "%93"}})
	id, _, _, _ := readSessionsRow(t, cfgDir, "ra-uuid")
	out, err := exec.Command(endlessGoBin(t), "--db-dir", cfgDir,
		"session-query", "resume-target", "--ref", "ES-"+strconv.FormatInt(id, 10)).Output()
	if err != nil {
		t.Fatalf("resume-target failed: %v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got["last_activity"] != "2026-05-20T00:00:00" {
		t.Errorf("last_activity = %v, want the seeded 2026-05-20T00:00:00", got["last_activity"])
	}
}
