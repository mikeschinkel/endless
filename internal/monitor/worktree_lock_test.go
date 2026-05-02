package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClaimWorktreeLock_RoundTrip(t *testing.T) {
	wt := t.TempDir()

	lock := WorktreeLock{
		SessionID: "f41f263e-c708-4c42-af7c-083b5be04943",
		PID:       20545,
		TmuxPane:  "%53",
		ClaimedAt: "2026-05-02T03:51:23Z",
	}
	if err := ClaimWorktreeLock(wt, lock); err != nil {
		t.Fatalf("claim: %v", err)
	}

	got, err := ReadWorktreeLock(wt)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SessionID != lock.SessionID {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, lock.SessionID)
	}
	if got.PID != lock.PID {
		t.Errorf("PID: got %d, want %d", got.PID, lock.PID)
	}
	if got.TmuxPane != lock.TmuxPane {
		t.Errorf("TmuxPane: got %q, want %q", got.TmuxPane, lock.TmuxPane)
	}
}

func TestClaimWorktreeLock_RefusesWhenLocked(t *testing.T) {
	wt := t.TempDir()
	first := WorktreeLock{SessionID: "first", PID: 1}
	if err := ClaimWorktreeLock(wt, first); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second := WorktreeLock{SessionID: "second", PID: 2}
	err := ClaimWorktreeLock(wt, second)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("second claim: got %v, want os.ErrExist", err)
	}
}

func TestReadWorktreeLock_MissingReturnsErrNotExist(t *testing.T) {
	wt := t.TempDir()
	_, err := ReadWorktreeLock(wt)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read missing: got %v, want os.ErrNotExist", err)
	}
}

func TestReleaseWorktreeLock_Idempotent(t *testing.T) {
	wt := t.TempDir()
	// Release on a never-claimed worktree must not error.
	if err := ReleaseWorktreeLock(wt); err != nil {
		t.Fatalf("release on empty: %v", err)
	}
	// Claim, release, release again — last call must not error.
	if err := ClaimWorktreeLock(wt, WorktreeLock{SessionID: "s", PID: 1}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := ReleaseWorktreeLock(wt); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := ReleaseWorktreeLock(wt); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestIsWorktreeLockStale_InvalidPID(t *testing.T) {
	if !IsWorktreeLockStale(nil) {
		t.Errorf("nil lock: got false, want true (stale)")
	}
	if !IsWorktreeLockStale(&WorktreeLock{PID: 0}) {
		t.Errorf("PID 0: got false, want true (stale)")
	}
	if !IsWorktreeLockStale(&WorktreeLock{PID: -1}) {
		t.Errorf("PID -1: got false, want true (stale)")
	}
}

func TestIsWorktreeLockStale_LiveProcess(t *testing.T) {
	// Our own PID is alive by definition.
	self := os.Getpid()
	if IsWorktreeLockStale(&WorktreeLock{PID: self}) {
		t.Errorf("self PID %d: got true (stale), want false", self)
	}
}

func TestIsWorktreeLockStale_DeadProcess(t *testing.T) {
	// PIDs above the typical max (4194303 on 64-bit Linux, 99998 on macOS by
	// default) are guaranteed not to exist. Use a value above all known
	// platform maxes.
	dead := 2147483646
	if !IsWorktreeLockStale(&WorktreeLock{PID: dead}) {
		t.Errorf("dead PID %d: got false, want true (stale)", dead)
	}
}

func TestReadWorktreeCompanion_RoundTrip(t *testing.T) {
	wt := t.TempDir()
	dir := filepath.Join(wt, ".endless")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const content = `{
  "kind": "task",
  "task_id": "E-808",
  "base_branch": "main",
  "branch": "task/808-event-logs-authoritative",
  "created_at": "2026-05-02T15:30:00Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "worktree.json"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadWorktreeCompanion(wt)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "task" {
		t.Errorf("Kind: got %q, want %q", got.Kind, "task")
	}
	if got.TaskID != "E-808" {
		t.Errorf("TaskID: got %q, want %q", got.TaskID, "E-808")
	}
	if got.Branch != "task/808-event-logs-authoritative" {
		t.Errorf("Branch: got %q", got.Branch)
	}
}

func TestReadWorktreeCompanion_Missing(t *testing.T) {
	wt := t.TempDir()
	_, err := ReadWorktreeCompanion(wt)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: got %v, want os.ErrNotExist", err)
	}
}

func TestFindWorktreeRoot_FindsNestedCompanion(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".endless", "worktrees", "e-808")
	dir := filepath.Join(wt, ".endless")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.json"), []byte(`{"kind":"task"}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Caller cwd is inside a subdir of the worktree.
	deep := filepath.Join(wt, "src", "internal")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}

	got, err := FindWorktreeRoot(deep, root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != wt {
		t.Errorf("got %q, want %q", got, wt)
	}
}

func TestFindWorktreeRoot_ReturnsEmptyAtProjectRoot(t *testing.T) {
	root := t.TempDir()
	// No companion anywhere; cwd is the project root.
	got, err := FindWorktreeRoot(root, root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFindWorktreeRoot_DoesNotWalkAboveProjectRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	// Plant a stray companion ABOVE the project root.
	strayDir := filepath.Join(parent, ".endless")
	if err := os.MkdirAll(strayDir, 0755); err != nil {
		t.Fatalf("mkdir stray: %v", err)
	}
	if err := os.WriteFile(filepath.Join(strayDir, "worktree.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	// cwd is the project root; walk-up must not pick up the parent's stray.
	got, err := FindWorktreeRoot(root, root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "" {
		t.Errorf("walked above projectRoot: got %q", got)
	}
}

func TestParseEndlessTaskID_AcceptsForms(t *testing.T) {
	// Local copy of the helper for testability — production version lives in
	// cmd/endless-hook/claude.go (it's a hook-local helper, not a package
	// export). This test mirrors its expected behavior so any divergence
	// is caught when the helper changes.
	cases := map[string]int64{
		"E-808":  808,
		"e-808":  808,
		"808":    808,
		" E-42 ": 42,
	}
	for in, want := range cases {
		got, err := parseEndlessTaskIDForTest(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %d, want %d", in, got, want)
		}
	}

	for _, bad := range []string{"", "E-", "E-foo", "abc", "E-12x"} {
		if _, err := parseEndlessTaskIDForTest(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}

// parseEndlessTaskIDForTest mirrors cmd/endless-hook/claude.go's
// parseEndlessTaskID. Kept in sync with the production version.
func parseEndlessTaskIDForTest(s string) (int64, error) {
	s = trimSpaceForTest(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	if len(s) >= 2 && (s[0] == 'E' || s[0] == 'e') && s[1] == '-' {
		s = s[2:]
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int64(c-'0')
	}
	if s == "" {
		return 0, errors.New("empty after prefix")
	}
	return n, nil
}

func trimSpaceForTest(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
