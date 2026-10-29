package verify_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/verify"
)

// writeSuite writes a verify.toml for task id under <root>/.endless/tasks/<id>/.
func writeSuite(t *testing.T, root, id, content string) {
	t.Helper()
	dir := filepath.Join(root, ".endless", "tasks", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// manifestFor builds a minimal valid manifest for id with a single first-class
// check on runner.
func manifestFor(id, runner string) string {
	return "schema = 1\ntask = \"" + id + "\"\n[[check]]\nrunner = \"" + runner + "\"\ntests = [\"TestX\"]\n"
}

// writeProjectConfig writes a project-level <root>/.endless/verify.toml.
func writeProjectConfig(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// LoadSuite finds a task's suite by its directory, and returns ok=false for a
// task directory without a verify.toml; a stray file under tasks/ is ignored.
func TestLoadSuite_FindsSuite(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest"))
	writeSuite(t, root, "E-5678", manifestFor("E-5678", "pytest"))
	if err := os.MkdirAll(filepath.Join(root, ".endless", "tasks", "E-9999"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".endless", "tasks", "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for id, want := range map[string]verify.Format{"E-1234": verify.FormatGotestJSON, "E-5678": verify.FormatPytestJSON} {
		m, ok, err := verify.LoadSuite(dt.DirPath(root), id)
		if err != nil || !ok {
			t.Fatalf("LoadSuite(%s): ok=%v err=%v", id, ok, err)
		}
		if len(m.Checks) != 1 || m.Checks[0].ResolvedFormat() != want {
			t.Errorf("%s manifest wrong: %+v", id, m)
		}
	}
	for _, id := range []string{"E-9999", "README.md"} {
		if _, ok, err := verify.LoadSuite(dt.DirPath(root), id); ok || err != nil {
			t.Errorf("LoadSuite(%s): ok=%v err=%v, want false, nil", id, ok, err)
		}
	}
}

func TestLoadSuite_NoTasksDir(t *testing.T) {
	_, ok, err := verify.LoadSuite(dt.DirPath(t.TempDir()), "E-1234")
	if ok || err != nil {
		t.Errorf("LoadSuite with no suites: ok=%v err=%v, want false, nil", ok, err)
	}
}

func TestLoadSuite_InvalidManifestFailsLoudly(t *testing.T) {
	root := t.TempDir()
	// Missing required task field.
	writeSuite(t, root, "E-1234", "schema = 1\n[[check]]\nrunner = \"gotest\"\ntests = [\"TestX\"]\n")

	_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
	if !errors.Is(err, verify.ErrMissingField) {
		t.Errorf("error did not wrap ErrMissingField: %v", err)
	}
}

// LoadSuite validates the EFFECTIVE manifest, so a per-check rule violation
// (here a raw runner with no command) surfaces loudly.
func TestLoadSuite_ValidatesChecks(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\n[[check]]\nrunner = \"bats\"\n")

	_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
	if !errors.Is(err, verify.ErrRawCheckNeedsCommand) {
		t.Errorf("error did not wrap ErrRawCheckNeedsCommand: %v", err)
	}
}

// A project-level verify.toml merges beneath the per-task manifest: project
// setup runs first, then per-task setup appends.
func TestLoadSuite_MergesProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 1\nsetup = [\"just build\"]\n")
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\nsetup = [\"task-setup\"]\n[[check]]\nrunner = \"gotest\"\ntests = [\"TestX\"]\n")

	m, ok, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
	if err != nil || !ok {
		t.Fatalf("LoadSuite: ok=%v err=%v", ok, err)
	}
	want := []string{"just build", "task-setup"}
	if len(m.Setup) != 2 || m.Setup[0] != want[0] || m.Setup[1] != want[1] {
		t.Errorf("Setup = %v, want %v (project first, then task)", m.Setup, want)
	}
}

// An invalid project-level verify.toml fails loudly.
func TestLoadSuite_InvalidProjectConfigFailsLoudly(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 2\n") // unknown schema version
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest"))

	_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
	if !errors.Is(err, verify.ErrInvalidProjectConfig) {
		t.Errorf("error did not wrap ErrInvalidProjectConfig: %v", err)
	}
}

// TestLoadSuite_DirCasingIsIndependentOfTaskField pins the convention E-1927
// settled: the suite DIRECTORY is lowercase, matching every other Endless path
// (.endless/worktrees/e-1889/, .endless/tasks/e-1889/verify.sh), while the
// manifest's `task` field stays the canonical display form E-NNNN that CLI
// arguments and prose use. They are the same id written for two audiences, so
// loading normalizes rather than forcing one convention onto the other.
func TestLoadSuite_DirCasingIsIndependentOfTaskField(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		task string
	}{
		{"lowercase_dir_canonical_task", "e-1234", "E-1234"},
		{"uppercase_dir_still_works", "E-1234", "E-1234"},
		{"lowercase_both", "e-1234", "e-1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSuite(t, root, tc.dir, manifestFor(tc.task, "gotest"))

			// A caller passing `E-1234` (as the Python CLI always does) hits,
			// whatever the directory looks like.
			_, ok, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
			if err != nil || !ok {
				t.Fatalf("LoadSuite: ok=%v err=%v", ok, err)
			}
		})
	}
}

// Relaxing CASE must not relax IDENTITY: a manifest declaring a different task
// than its directory is a loud failure.
func TestLoadSuite_RejectsTaskIDMismatch(t *testing.T) {
	for _, dir := range []string{"E-1234", "e-1234"} {
		root := t.TempDir()
		writeSuite(t, root, dir, manifestFor("E-9999", "gotest"))

		_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
		if !errors.Is(err, verify.ErrTaskIDMismatch) {
			t.Errorf("dir %s: want ErrTaskIDMismatch, got %v", dir, err)
		}
	}
}

// A check's dir is refused when its suite is loaded to run if it is absolute,
// leaves the repo root (lexically or through a symlink), or names no existing
// directory, and the refusal names the check's index and its dir.
func TestLoadSuite_RefusesBadCheckDir(t *testing.T) {
	outside := t.TempDir()
	tests := []struct {
		name    string
		dir     string
		wantErr error
	}{
		{name: "absolute", dir: outside, wantErr: verify.ErrCheckDirAbsolute},
		{name: "dot-dot", dir: "../elsewhere", wantErr: verify.ErrCheckDirOutsideRoot},
		{name: "symlink out", dir: "escape", wantErr: verify.ErrCheckDirOutsideRoot},
		{name: "missing", dir: "nope", wantErr: verify.ErrCheckDirMissing},
		{name: "a file", dir: "afile", wantErr: verify.ErrCheckDirMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
				t.Fatalf("Symlink: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "afile"), nil, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\n"+
				"[[check]]\nsh = \"true\"\n"+
				"[[check]]\nsh = \"true\"\ndir = \""+tt.dir+"\"\n")

			_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LoadSuite error = %v, want %v", err, tt.wantErr)
			}
			if !errors.Is(err, verify.ErrInvalidManifest) {
				t.Errorf("error did not wrap ErrInvalidManifest: %v", err)
			}
			for _, want := range []string{"index=1", "dir=" + tt.dir} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name %q: %v", want, err)
				}
			}
		})
	}
}

// A dir inside the root, including one reached through a symlink that stays
// inside it, is accepted.
func TestLoadSuite_AcceptsCheckDirInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods", "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "mods"), filepath.Join(root, "link")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\n"+
		"[[check]]\nsh = \"true\"\ndir = \"mods/sub\"\n"+
		"[[check]]\nsh = \"true\"\ndir = \"link/sub\"\n")

	if _, ok, err := verify.LoadSuite(dt.DirPath(root), "E-1234"); err != nil || !ok {
		t.Fatalf("LoadSuite: ok=%v err=%v", ok, err)
	}
}

// LoadSuite reads only the named task's suite: another task's, rotted after it
// landed — a dir since removed, a key this build does not know — does not
// refuse it.
func TestLoadSuite_IgnoresOtherSuites(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "e-1", "schema = 1\ntask = \"E-1\"\n[[check]]\nsh = \"true\"\ndir = \"gone\"\n")
	writeSuite(t, root, "e-2", "schema = 1\ntask = \"E-2\"\nretired_key = 1\n[[check]]\nsh = \"true\"\n")
	writeSuite(t, root, "e-3", manifestFor("E-3", "gotest"))

	eff, ok, err := verify.LoadSuite(dt.DirPath(root), "E-3")
	if err != nil || !ok {
		t.Fatalf("LoadSuite(E-3): ok=%v err=%v", ok, err)
	}
	if eff.Task != "E-3" {
		t.Errorf("task = %q, want E-3", eff.Task)
	}

	if _, ok, err := verify.LoadSuite(dt.DirPath(root), "E-4"); ok || err != nil {
		t.Errorf("LoadSuite(E-4) with no suite: ok=%v err=%v, want false, nil", ok, err)
	}
}
