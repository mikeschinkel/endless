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

func manifestFor(id, format string) string {
	return "schema = 1\ntask = \"" + id + "\"\nrunner = \"go test ./...\"\nformat = \"" + format + "\"\n"
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
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest-json"))
	writeSuite(t, root, "E-5678", manifestFor("E-5678", "pytest-json"))

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
	if manifests["E-1234"] == nil || manifests["E-1234"].Format != verify.FormatGotestJSON {
		t.Errorf("E-1234 manifest wrong: %+v", manifests["E-1234"])
	}
	if manifests["E-5678"] == nil || manifests["E-5678"].Format != verify.FormatPytestJSON {
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
	writeSuite(t, root, "E-1234", manifestFor("E-9999", "tap"))

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
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\nformat = \"tap\"\n") // missing runner

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted an invalid manifest")
	}
	if !errors.Is(err, verify.ErrMissingField) {
		t.Errorf("error did not wrap ErrMissingField: %v", err)
	}
}

// A project-level verify.toml merges beneath each per-task manifest: project
// setup runs first, and a per-task manifest may omit a field the project
// supplies as a default (here, format).
func TestDiscover_MergesProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 1\nformat = \"gotest-json\"\nsetup = [\"just build\"]\n")
	// Per-task manifest omits format (inherits project default) and adds its
	// own setup step after the project's.
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\nrunner = \"go test ./...\"\nsetup = [\"task-setup\"]\n")

	manifests, err := verify.Discover(dt.DirPath(root))
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	m := manifests["E-1234"]
	if m == nil {
		t.Fatalf("Discover did not find E-1234 (keys: %v)", keysOf(manifests))
	}
	if m.Format != verify.FormatGotestJSON {
		t.Errorf("Format = %q, want inherited project default %q", m.Format, verify.FormatGotestJSON)
	}
	want := []string{"just build", "task-setup"}
	if len(m.Setup) != 2 || m.Setup[0] != want[0] || m.Setup[1] != want[1] {
		t.Errorf("Setup = %v, want %v (project first, then task)", m.Setup, want)
	}
}

// Without a project config, a per-task manifest must still be self-sufficient:
// an omitted format is not supplied by any layer and fails validation loudly.
func TestDiscover_NoProjectConfigRequiresCompleteManifest(t *testing.T) {
	root := t.TempDir()
	writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\nrunner = \"go test ./...\"\n") // no format

	_, err := verify.Discover(dt.DirPath(root))
	if err == nil {
		t.Fatal("Discover accepted a manifest missing format with no project default")
	}
	if !errors.Is(err, verify.ErrMissingField) {
		t.Errorf("error did not wrap ErrMissingField: %v", err)
	}
}

// An invalid project-level verify.toml fails discovery loudly.
func TestDiscover_InvalidProjectConfigFailsLoudly(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, "schema = 1\nformat = \"junit-xml\"\n") // bad default format
	writeSuite(t, root, "E-1234", manifestFor("E-1234", "tap"))

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
