package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

// writeScriptSuite creates <root>/.endless/tasks/<dir>/verify.sh.
func writeScriptSuite(t *testing.T, root, dir string) {
	t.Helper()
	d := filepath.Join(root, SuitesDir, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, ScriptFile), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDiscoverScripts_KeysByNormalizedDirectoryName(t *testing.T) {
	root := t.TempDir()
	writeScriptSuite(t, root, "e-1234")

	scripts, err := DiscoverScripts(dt.DirPath(root))
	if err != nil {
		t.Fatalf("DiscoverScripts: %v", err)
	}
	fp, ok := scripts["E-1234"]
	if !ok {
		t.Fatalf("lowercase directory e-1234 did not resolve under the canonical id E-1234; got %v", scripts)
	}
	if filepath.Base(string(fp)) != ScriptFile {
		t.Errorf("script path = %q, want a path ending in %s", fp, ScriptFile)
	}
}

// A per-task directory holding no verify.sh contributes nothing — the same rule
// Discover applies to a directory holding no verify.toml.
func TestDiscoverScripts_SkipsDirectoriesWithoutTheFile(t *testing.T) {
	root := t.TempDir()
	writeScriptSuite(t, root, "e-1")
	if err := os.MkdirAll(filepath.Join(root, SuitesDir, "e-2"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	scripts, err := DiscoverScripts(dt.DirPath(root))
	if err != nil {
		t.Fatalf("DiscoverScripts: %v", err)
	}
	if len(scripts) != 1 {
		t.Errorf("discovered %d suites, want 1: %v", len(scripts), scripts)
	}
}

// The shared harness and the directory-level rules file sit BESIDE the per-task
// directories. Neither may be discoverable as a task of its own, and neither may
// disturb discovery of the real ones. This is pinned rather than relied upon: it
// falls out of the IsDir() test today, and a later loosening of that test would
// otherwise silently invent tasks named "_harness.sh" and "CLAUDE.md".
func TestDiscoverScripts_IgnoresSharedFilesBesideTheTaskDirs(t *testing.T) {
	root := t.TempDir()
	writeScriptSuite(t, root, "e-1")
	for _, name := range []string{HarnessFile, "CLAUDE.md"} {
		fp := filepath.Join(root, SuitesDir, name)
		if err := os.WriteFile(fp, []byte("shared\n"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	scripts, err := DiscoverScripts(dt.DirPath(root))
	if err != nil {
		t.Fatalf("DiscoverScripts: %v", err)
	}
	if len(scripts) != 1 {
		t.Fatalf("discovered %d suites, want only the one task dir: %v", len(scripts), scripts)
	}
	for _, name := range []string{HarnessFile, "CLAUDE.md", NormalizeTaskID(HarnessFile)} {
		if _, ok := scripts[name]; ok {
			t.Errorf("%q was discovered as a task", name)
		}
	}

	// The same files must not become manifest suites either.
	manifests, err := Discover(dt.DirPath(root))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("Discover found %d manifests beside shared files, want 0: %v", len(manifests), manifests)
	}
}

// A project with no .endless/tasks directory has no suites; that is not an
// error, exactly as it is not one for Discover.
func TestDiscoverScripts_MissingDirectoryIsEmpty(t *testing.T) {
	scripts, err := DiscoverScripts(dt.DirPath(t.TempDir()))
	if err != nil {
		t.Fatalf("DiscoverScripts: %v", err)
	}
	if len(scripts) != 0 {
		t.Errorf("discovered %d suites in an empty project, want 0", len(scripts))
	}
}

// A task may hold both forms during a conversion. Discovery reports both; which
// one WINS is the runner's decision (the manifest), not discovery's.
func TestDiscoverScripts_CoexistsWithAManifest(t *testing.T) {
	root := t.TempDir()
	writeScriptSuite(t, root, "e-7")
	body := "schema = 1\ntask = \"E-7\"\n[[check]]\nrunner = \"sh\"\ncommand = \"true\"\nformat = \"tap\"\n"
	if err := os.WriteFile(filepath.Join(root, SuitesDir, "e-7", ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	scripts, err := DiscoverScripts(dt.DirPath(root))
	if err != nil {
		t.Fatalf("DiscoverScripts: %v", err)
	}
	manifests, err := Discover(dt.DirPath(root))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if _, ok := scripts["E-7"]; !ok {
		t.Error("script suite not discovered when a manifest sits beside it")
	}
	if _, ok := manifests["E-7"]; !ok {
		t.Error("manifest not discovered when a script sits beside it")
	}
}
