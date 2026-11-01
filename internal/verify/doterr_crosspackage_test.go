package verify_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/verify"
)

// Every Endless package that uses doterr holds its own copy of doterr.go, and
// each copy stamps the errors it builds with its own id. WithErr marks an entry
// built by a different copy with ErrCrossPackageError instead of enriching it.
// This package is the only one in Endless that calls WithErr, so these cases
// drive each of its WithErr sites to an error and prove the error it enriched
// was one of its own: the expected sentinel is present and the cross-package
// marker is not. A new WithErr call that enriches another package's error
// surfaces here as ErrCrossPackageError.
func TestWithErr_NeverEnrichesAnotherPackagesError(t *testing.T) {
	cases := []struct {
		name     string
		run      func(t *testing.T, root string) error
		sentinel error
	}{
		{
			// discover.go: LoadSuite enriches eff.ValidateDirs's error.
			name: "LoadSuite: check dir missing",
			run: func(t *testing.T, root string) error {
				writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest")+"dir = \"missing\"\n")
				_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
				return err
			},
			sentinel: verify.ErrInvalidManifest,
		},
		{
			// discover.go: effectiveManifest enriches eff.Validate's error.
			name: "LoadSuite: manifest declares no checks",
			run: func(t *testing.T, root string) error {
				writeSuite(t, root, "E-1234", "schema = 1\ntask = \"E-1234\"\n")
				_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
				return err
			},
			sentinel: verify.ErrInvalidManifest,
		},
		{
			// discover.go: loadManifestForMerge enriches decodeManifest's error.
			name: "LoadSuite: manifest is not TOML",
			run: func(t *testing.T, root string) error {
				writeSuite(t, root, "E-1234", "schema = [\n")
				_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
				return err
			},
			sentinel: verify.ErrInvalidManifest,
		},
		{
			// project.go: LoadProjectConfig enriches ParseProjectConfig's error,
			// and LoadSuite enriches that again.
			name: "LoadSuite: project config lacks a schema",
			run: func(t *testing.T, root string) error {
				writeSuite(t, root, "E-1234", manifestFor("E-1234", "gotest"))
				writeProjectConfig(t, root, "setup = [\"true\"]\n")
				_, _, err := verify.LoadSuite(dt.DirPath(root), "E-1234")
				return err
			},
			sentinel: verify.ErrInvalidProjectConfig,
		},
		{
			name: "LoadProjectConfig: project config is not TOML",
			run: func(t *testing.T, root string) error {
				writeProjectConfig(t, root, "schema = [\n")
				_, err := verify.LoadProjectConfig(dt.DirPath(root))
				return err
			},
			sentinel: verify.ErrInvalidProjectConfig,
		},
		{
			// discover.go: DiscoverScripts enriches the ReadDir error.
			name: "DiscoverScripts: tasks directory unreadable",
			run: func(t *testing.T, root string) error {
				if os.Geteuid() == 0 {
					t.Skip("root reads any directory")
				}
				tasks := filepath.Join(root, ".endless", "tasks")
				if err := os.MkdirAll(tasks, 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.Chmod(tasks, 0o000); err != nil {
					t.Fatalf("Chmod: %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
				_, err := verify.DiscoverScripts(dt.DirPath(root))
				return err
			},
			sentinel: verify.ErrDiscoveringSuites,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t, t.TempDir())
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("error lacks %v: %v", tc.sentinel, err)
			}
			if errors.Is(err, verify.ErrCrossPackageError) {
				t.Errorf("WithErr enriched an error built by another package's doterr copy: %v", err)
			}
		})
	}
}
