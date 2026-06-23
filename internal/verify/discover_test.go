package verify_test

import (
	"errors"
	"os"
	"path/filepath"
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

func TestDiscover_FindsSuites(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest"))
	writeSuite(t, root, "E-5678", manifestFor("E-5678", "pytest"))

	// A subdirectory with no verify.toml is not a suite and must be ignored.
	if err := os.MkdirAll(filepath.Join(root, ".endless", "tasks", "E-9999"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A stray file directly under tasks/ is not a directory and must be ignored.
	if err := os.WriteFile(filepath.Join(root, ".endless", "tasks", "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	manifests, err := verify.Discover(dt.DirPath(root))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("Discover found %d manifests, want 2 (keys: %v)", len(manifests), keysOf(manifests))
	}
	if m := manifests["E-1234"]; m == nil || len(m.Checks) != 1 || m.Checks[0].ResolvedFormat() != verify.FormatGotestJSON {
		t.Errorf("E-1234 manifest wrong: %+v", manifests["E-1234"])
	}
	if m := manifests["E-5678"]; m == nil || len(m.Checks) != 1 || m.Checks[0].ResolvedFormat() != verify.FormatPytestJSON {
		t.Errorf("E-5678 manifest wrong: %+v", manifests["E-5678"])
	}
}

func TestDiscover_NoTasksDir(t *testing.T) {
	manifests, err := verify.Discover(dt.DirPath(t.TempDir()))
	if err != nil {
		t.Fatalf("Discover returned error for project with no suites: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("Discover found %d manifests, want 0", len(manifests))
	}
}

func TestDiscover_TaskIDMismatchFailsLoudly(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", manifestFor("E-9999", "gotest"))

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted a manifest whose task id mismatched its directory")
	}
	if !errors.Is(err, verify.ErrTaskIDMismatch) {
		t.Errorf("error did not wrap ErrTaskIDMismatch: %v", err)
	}
}

func TestDiscover_InvalidManifestFailsLoudly(t *testing.T) {
	root := t.TempDir()
	// Missing required task field.
	writeSuite(t, root, "E-1234", "schema = 1\n[[check]]\nrunner = \"gotest\"\ntests = [\"TestX\"]\n")

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted an invalid manifest")
	}
	if !errors.Is(err, verify.ErrMissingField) {
		t.Errorf("error did not wrap ErrMissingField: %v", err)
	}
}

// Discover validates the EFFECTIVE manifest, so a per-check rule violation (here
// a raw runner with no command) surfaces loudly through discovery.
func TestDiscover_ValidatesChecks(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\n[[check]]\nrunner = \"bats\"\n")

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted a raw check with no command")
	}
	if !errors.Is(err, verify.ErrRawCheckNeedsCommand) {
		t.Errorf("error did not wrap ErrRawCheckNeedsCommand: %v", err)
	}
}

// A project-level verify.toml merges beneath each per-task manifest: project
// setup runs first, then per-task setup appends.
func TestDiscover_MergesProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 1\nsetup = [\"just build\"]\n")
	// Per-task manifest adds its own setup step after the project's.
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\nsetup = [\"task-setup\"]\n[[check]]\nrunner = \"gotest\"\ntests = [\"TestX\"]\n")

	manifests, err := verify.Discover(dt.DirPath(root))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	m := manifests["E-1234"]
	if m == nil {
		t.Fatalf("Discover did not find E-1234 (keys: %v)", keysOf(manifests))
	}
	want := []string{"just build", "task-setup"}
	if len(m.Setup) != 2 || m.Setup[0] != want[0] || m.Setup[1] != want[1] {
		t.Errorf("Setup = %v, want %v (project first, then task)", m.Setup, want)
	}
}

// An invalid project-level verify.toml fails discovery loudly.
func TestDiscover_InvalidProjectConfigFailsLoudly(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 2\n") // unknown schema version
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest"))

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted an invalid project config")
	}
	if !errors.Is(err, verify.ErrInvalidProjectConfig) {
		t.Errorf("error did not wrap ErrInvalidProjectConfig: %v", err)
	}
}

func keysOf(m map[string]*verify.Manifest) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
