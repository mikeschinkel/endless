package verify

import (
	"os"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// SuitesDir is the product-controlled directory, relative to a project root,
// that holds per-task verification suites. It is identical across every project
// regardless of that project's own tests/ layout, so the discovery convention
// never has to be configured.
const SuitesDir = ".endless/tasks"

// ManifestFile is the manifest filename within each suite directory.
const ManifestFile = "verify.toml"

// LoadManifest reads, decodes, and validates a single verify.toml at fp.
func LoadManifest(fp dt.Filepath) (m *Manifest, err error) {
	var data []byte

	data, err = fp.ReadFile()
	if err != nil {
		err = doterr.NewErr(ErrInvalidManifest, ErrReadingManifest, err)
		goto end
	}

	m, err = ParseManifest(data)
	if err != nil {
		goto end
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "filepath", fp)
	}
	return m, err
}

// Discover walks <root>/.endless/tasks/*/verify.toml, parses and validates each
// manifest, and returns them keyed by task id (the suite directory name). A
// suite directory whose declared task does not match its directory name fails
// loudly, as does any malformed manifest.
//
// A missing .endless/tasks directory yields an empty map and no error: a
// project with no suites yet is not an error. Subdirectories without a
// verify.toml, and non-directory entries, are ignored.
func Discover(root dt.DirPath) (manifests map[string]*Manifest, err error) {
	var tasksDir dt.DirPath
	var entries []os.DirEntry
	var entry os.DirEntry
	var exists bool
	var hasManifest bool
	var id string
	var manifestPath dt.Filepath
	var m *Manifest

	manifests = make(map[string]*Manifest)
	tasksDir = root.Join(SuitesDir)

	exists, err = tasksDir.Exists()
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err)
		goto end
	}
	if !exists {
		goto end
	}

	entries, err = tasksDir.ReadDir()
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err)
		goto end
	}

	for _, entry = range entries {
		if !entry.IsDir() {
			continue
		}
		id = entry.Name()
		manifestPath = dt.FilepathJoin3(tasksDir, id, ManifestFile)

		hasManifest, err = manifestPath.Exists()
		if err != nil {
			err = doterr.NewErr(ErrDiscoveringSuites, err)
			goto end
		}
		if !hasManifest {
			continue
		}

		m, err = LoadManifest(manifestPath)
		if err != nil {
			goto end
		}
		if m.Task != id {
			err = doterr.NewErr(ErrInvalidManifest, ErrTaskIDMismatch,
				"dir", id, "task", m.Task, "filepath", manifestPath)
			goto end
		}
		manifests[id] = m
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "root", root)
	}
	return manifests, err
}
