package verify

import (
	"os"
	"strings"

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

// ScriptFile is the shell-script suite filename within each suite directory —
// the second of the two forms a task's verification can take. A task carries a
// manifest, a script, or (during a conversion) both; the manifest is the
// documented form and wins when both are present.
//
// It is a product-controlled constant beside ManifestFile rather than a literal
// at the runner's call site, so a project that lays its suites out differently
// is a config change here rather than a fork of the runner.
const ScriptFile = "verify.sh"

// HarnessFile is the shared shell harness a script suite sources, at the ROOT of
// SuitesDir (.endless/tasks/_harness.sh) rather than inside any task's
// directory: it belongs to no task. Discovery iterates directory entries looking
// for per-task directories, so a plain file sitting beside them is skipped and
// the harness is never mistaken for a task of its own.
const HarnessFile = "_harness.sh"

// NormalizeTaskID folds a task id to a case-insensitive comparison form.
//
// The same id is written two ways, for two audiences, and both are right where
// they appear. On disk it is lowercase — `.endless/tasks/e-1758/` — matching
// every other Endless path (`.endless/worktrees/e-1889/`,
// `.endless/tasks/e-1889/verify.sh`). In the manifest's `task` field, in CLI
// arguments, and in prose it is the canonical display form `E-1758`, which is
// how a task id is written everywhere else in the product.
//
// So nothing internal compares these raw. Forcing one convention onto the other
// would let a string-equality detail dictate what users type and see, which is
// backwards: normalize at the comparison instead and let each surface read the
// way it should.
func NormalizeTaskID(id string) string {
	return strings.ToUpper(id)
}

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

// LoadSuite loads the one task's effective manifest a run executes: its
// verify.toml merged beneath the project-level config, validated, and its
// checks' dirs resolved against root (see Manifest.ValidateDirs). It reads no
// other task's suite, so a landed suite that has rotted — a key a newer build
// dropped, a dir since renamed — cannot refuse another task's run. A task with
// no suite directory, or no verify.toml in it, returns ok=false and no error.
func LoadSuite(root dt.DirPath, id string) (eff *Manifest, ok bool, err error) {
	var dir dt.DirPath
	var manifestPath dt.Filepath
	var project *ProjectConfig

	dir, ok, err = SuiteDir(root, id)
	if err != nil || !ok {
		goto end
	}
	manifestPath = dt.FilepathJoin(dir, ManifestFile)
	ok, err = manifestPath.Exists()
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err)
		goto end
	}
	if !ok {
		goto end
	}

	project, err = LoadProjectConfig(root)
	if err != nil {
		goto end
	}
	eff, err = effectiveManifest(project, manifestPath, string(dir.Base()))
	if err != nil {
		goto end
	}
	err = eff.ValidateDirs(root)
	if err != nil {
		err = doterr.WithErr(err, "filepath", manifestPath)
		goto end
	}
end:
	if err != nil {
		ok = false
		err = doterr.WithErr(err, "root", root)
	}
	return eff, ok, err
}

// effectiveManifest decodes the per-task manifest at manifestPath, merges it
// beneath project, validates the result, and checks its task names the suite
// directory dirName.
func effectiveManifest(project *ProjectConfig, manifestPath dt.Filepath, dirName string) (eff *Manifest, err error) {
	var m *Manifest

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
	if NormalizeTaskID(eff.Task) != NormalizeTaskID(dirName) {
		err = doterr.NewErr(ErrInvalidManifest, ErrTaskIDMismatch,
			"dir", dirName, "task", eff.Task, "filepath", manifestPath)
		goto end
	}
end:
	return eff, err
}

// SuiteDir resolves a task's suite directory: the entry under
// <root>/.endless/tasks whose name normalizes to id. It scans rather than
// joining strings.ToLower(id) because the directory name is data on disk, and
// discovery has always compared these NORMALIZED — the directory is lowercase
// by path convention while the id is written canonically everywhere else.
// Deriving the path by re-lowercasing would be a second, quieter casing rule
// that disagrees with the first on a case-sensitive filesystem.
//
// A missing suites directory, or no matching entry, returns ok=false and no
// error: not every task has a suite.
func SuiteDir(root dt.DirPath, id string) (dir dt.DirPath, ok bool, err error) {
	var tasksDir dt.DirPath
	var entries []os.DirEntry
	var entry os.DirEntry
	var exists bool

	tasksDir = root.Join(SuitesDir)

	exists, err = tasksDir.Exists()
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err, "root", root)
		goto end
	}
	if !exists {
		goto end
	}

	entries, err = tasksDir.ReadDir()
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err, "root", root)
		goto end
	}

	for _, entry = range entries {
		if !entry.IsDir() {
			continue
		}
		if NormalizeTaskID(entry.Name()) != NormalizeTaskID(id) {
			continue
		}
		dir = tasksDir.Join(entry.Name())
		ok = true
		goto end
	}
end:
	return dir, ok, err
}

// ManifestIDs returns the normalized ids of the suite directories under
// <root>/.endless/tasks that hold a verify.toml, by the file's presence alone:
// no manifest is read. A missing .endless/tasks directory yields an empty set.
func ManifestIDs(root dt.DirPath) (ids map[string]bool, err error) {
	var tasksDir dt.DirPath
	var entries []os.DirEntry
	var exists bool

	ids = make(map[string]bool)
	tasksDir = root.Join(SuitesDir)

	exists, err = tasksDir.Exists()
	if err != nil || !exists {
		goto end
	}
	entries, err = tasksDir.ReadDir()
	if err != nil {
		goto end
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		has, ferr := dt.FilepathJoin3(tasksDir, entry.Name(), ManifestFile).Exists()
		if ferr == nil && has {
			ids[NormalizeTaskID(entry.Name())] = true
		}
	}
end:
	if err != nil {
		err = doterr.NewErr(ErrDiscoveringSuites, err, "root", root)
	}
	return ids, err
}

// DiscoverScripts walks <root>/.endless/tasks/*/verify.sh and returns the
// script suites keyed by task id (normalized — see NormalizeTaskID). It is the
// script-form counterpart of ManifestIDs and deliberately mirrors its rules: only
// directory entries are considered, a directory without the file is skipped, and
// a missing .endless/tasks directory yields an empty map and no error.
//
// The key is the DIRECTORY name, because a script carries no `task` field to
// declare its own id. That is the whole reason the two forms cannot share one
// keying rule, and the reason discovery normalizes rather than lowercasing: the
// directory is lowercase by path convention while callers name the task in the
// canonical E-NNNN form.
//
// Non-directory entries — HarnessFile, a directory-level CLAUDE.md — are skipped
// by the same IsDir() test that skips them in ManifestIDs, so a shared file living
// beside the per-task directories is never discoverable as a task.
func DiscoverScripts(root dt.DirPath) (scripts map[string]dt.Filepath, err error) {
	var tasksDir dt.DirPath
	var entries []os.DirEntry
	var entry os.DirEntry
	var exists bool
	var hasScript bool
	var scriptPath dt.Filepath

	scripts = make(map[string]dt.Filepath)

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
		scriptPath = dt.FilepathJoin3(tasksDir, entry.Name(), ScriptFile)

		hasScript, err = scriptPath.Exists()
		if err != nil {
			err = doterr.NewErr(ErrDiscoveringSuites, err)
			goto end
		}
		if !hasScript {
			continue
		}
		scripts[NormalizeTaskID(entry.Name())] = scriptPath
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "root", root)
	}
	return scripts, err
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
