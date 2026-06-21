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

// ScriptsDir is the standard home, relative to a project root, for
// project-shared setup/seed scripts referenced from a verify config (e.g.
// .endless/verify/setup.sh). Short steps may inline directly in the TOML;
// non-trivial setup belongs in a real script file here so it stays
// editor- and linter-friendly. Per-task scripts instead live beside their
// manifest under .endless/tasks/<id>/. A setup or seed entry is either an
// inline command or a path (conventionally under this directory) the runner
// executes; this package defines the convention, the runner (downstream)
// resolves and runs the entries.
const ScriptsDir = ".endless/verify"

// LoadManifest reads, decodes, and validates a single, self-sufficient
// verify.toml at fp. It enforces every required field; use it for a standalone
// manifest (the bare-clone case). The layered discovery path (Discover) instead
// decodes per-task files leniently and validates the merged effective manifest,
// so a field the project config supplies need not appear in every task file.
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

// Discover walks <root>/.endless/tasks/*/verify.toml, merges each per-task
// manifest beneath the optional project-level <root>/.endless/verify.toml, and
// returns the effective manifests keyed by task id (the suite directory name).
// Each returned manifest is the merged result a runner executes (see Merge) and
// is fully validated. A suite directory whose declared task does not match its
// directory name fails loudly, as does any malformed manifest or project config.
//
// A missing .endless/tasks directory yields an empty map and no error: a
// project with no suites yet is not an error. A missing project-level
// verify.toml simply contributes no shared layer. Subdirectories without a
// verify.toml, and non-directory entries, are ignored.
func Discover(root dt.DirPath) (manifests map[string]*Manifest, err error) {
	var tasksDir dt.DirPath
	var entries []os.DirEntry
	var entry os.DirEntry
	var exists bool
	var hasManifest bool
	var id string
	var manifestPath dt.Filepath
	var project *ProjectConfig
	var m *Manifest
	var eff *Manifest

	manifests = make(map[string]*Manifest)

	project, err = LoadProjectConfig(root)
	if err != nil {
		goto end
	}

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

		m, err = loadManifestForMerge(manifestPath)
		if err != nil {
			goto end
		}

		eff = Merge(project, m)

		err = eff.Validate()
		if err != nil {
			err = doterr.WithErr(err, "filepath", manifestPath)
			goto end
		}
		if eff.Task != id {
			err = doterr.NewErr(ErrInvalidManifest, ErrTaskIDMismatch,
				"dir", id, "task", eff.Task, "filepath", manifestPath)
			goto end
		}
		manifests[id] = eff
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "root", root)
	}
	return manifests, err
}

// loadManifestForMerge reads and leniently decodes a per-task verify.toml: it
// rejects unknown keys but does NOT enforce required fields, leaving that to
// validation of the effective manifest after the project layer is merged in.
func loadManifestForMerge(fp dt.Filepath) (m *Manifest, err error) {
	var data []byte

	data, err = fp.ReadFile()
	if err != nil {
		err = doterr.NewErr(ErrInvalidManifest, ErrReadingManifest, err)
		goto end
	}

	m, err = decodeManifest(data)
	if err != nil {
		goto end
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "filepath", fp)
	}
	return m, err
}
