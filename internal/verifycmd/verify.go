// Package verifycmd implements the `endless-go verify` subcommand: the Tier-0
// verification runner (E-1603). It reads a task's discovered verify.toml
// (E-1602/E-1611/E-1618), runs the suite under generic, app-agnostic isolation
// — a fresh per-run temp directory plus a temp HOME and temp XDG_CONFIG_HOME so
// the suite cannot read or pollute the developer's real home/config — invokes
// each [[check]] instrumented to emit its native result stream, normalizes and
// merges the streams to one CTRF report (E-1604), prints a pass/fail summary,
// and exits 0 on all-pass / non-zero otherwise.
//
// The runner is deliberately app-agnostic: it knows nothing about any specific
// application's state (no task DB, no SQLite). Because Endless's own DB lives
// under XDG_CONFIG_HOME, isolating XDG happens to give an Endless-as-SUT suite a
// fresh DB for free — that is incidental to the isolation, not runner logic.
//
// Tier-0 boundary: a suite that declares needs (substrate escalation, Stage 3+)
// or seed (E-1606) fails loudly rather than run something weaker than asked.
package verifycmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// checkResult pairs a check's position and runner with its normalized report so
// the summary can render one line per check before the merged totals.
type checkResult struct {
	index  int
	runner string
	report *verify.Report
}

// Run is the `endless-go verify` entry point. It takes a single positional task
// id (the suite directory name, e.g. E-1234) and an optional --keep flag. The
// task id is required: resolving "the cwd's task" is the Python CLI's job (it
// owns session/worktree context) and it passes the id through explicitly.
func Run(args []string) {
	var keep *bool
	var rest []string
	var code int
	var err error

	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	keep = fs.Bool("keep", false,
		"Keep the per-run temp dir (isolated HOME/XDG + intermediates) for debugging")
	if err = fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest = fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: endless-go verify [--keep] <task-id>")
		os.Exit(2)
	}

	code, err = run(rest[0], *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go verify: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run orchestrates one verification. It returns an exit code (0 all-pass,
// non-zero otherwise) and a non-nil error only for an infrastructure failure
// (bad discovery, setup abort, a runner that emitted no parseable stream) — a
// suite that runs cleanly but has failing tests returns (1, nil) after printing
// its summary.
func run(id string, keep bool) (code int, err error) {
	var root dt.DirPath
	var manifests map[string]*verify.Manifest
	var scripts map[string]dt.Filepath
	var script dt.Filepath
	var eff *verify.Manifest
	var ok bool
	var runDir dt.DirPath
	var env []string
	var results []checkResult
	var merged *verify.Report
	var ctrfPath dt.Filepath

	root, err = findProjectRoot()
	if err != nil {
		goto end
	}

	// The own-task-only refusal comes FIRST — before discovery, before the temp
	// dir, before anything runs. Nothing below this line may execute on behalf
	// of a task that is not the caller's.
	err = guardOwnTaskOnly(id, root)
	if err != nil {
		goto end
	}

	manifests, err = verify.Discover(root)
	if err != nil {
		goto end
	}

	eff, ok = manifests[verify.NormalizeTaskID(id)]
	if !ok {
		// A manifest is the documented form and wins when a task has both, so
		// the script is a FALLBACK rather than a second search path: same
		// directory, other filename.
		scripts, err = verify.DiscoverScripts(root)
		if err != nil {
			goto end
		}
		script, ok = scripts[verify.NormalizeTaskID(id)]
		if !ok {
			err = doterr.NewErr(ErrNoSuiteForTask,
				"task", id, "dir", suiteDir(root, id),
				"looked_for", verify.ManifestFile+", "+verify.ScriptFile,
				"suites_found", suiteCount(manifests, scripts), "root", root)
			goto end
		}
		code, err = runScriptSuite(id, script, root, keep)
		goto end
	}

	// Tier-0 boundary. needs selects a heavier substrate (containers/PTY,
	// Stage 3+); seed is E-1606. Running unisolated or unseeded would silently
	// do less than the manifest asked, so refuse loudly instead.
	if len(eff.Needs) > 0 {
		err = doterr.NewErr(ErrTierNotSupported, "task", id, "needs", strings.Join(eff.Needs, ", "))
		goto end
	}
	if len(eff.Seed) > 0 {
		err = doterr.NewErr(ErrSeedNotSupported, "task", id, "seed", strings.Join(eff.Seed, ", "))
		goto end
	}

	runDir, err = makeRunDir()
	if err != nil {
		goto end
	}
	// Teardown is registered BEFORE setup runs so a failing setup step still
	// triggers teardown + cleanup (E-1618's shell-trap parity). env is captured
	// by reference; if isolation below fails it stays nil and teardown skips its
	// steps (they must run under the isolated env or not at all).
	defer teardown(root, &env, eff.Teardown, runDir, keep)

	env, err = isolatedEnv(runDir)
	if err != nil {
		goto end
	}

	// Preconditions, in order: provision (Tier-0 no-op) -> setup -> seed
	// (guarded out above). A failing setup step aborts loudly.
	err = runSetup(eff.Setup, root, env)
	if err != nil {
		goto end
	}

	results, err = runChecks(eff.Checks, root, env, runDir)
	if err != nil {
		goto end
	}

	merged = mergeResults(results)
	ctrfPath, err = writeCTRF(id, merged)
	if err != nil {
		goto end
	}

	printSummary(id, results, merged, ctrfPath)
	if merged.Results.Summary.Failed > 0 || merged.Results.Summary.Tests == 0 {
		code = 1
	}

end:
	return code, err
}

// suiteCount reports how many DISTINCT tasks have a suite, in either form.
//
// It is a count and not a listing on purpose. The error used to enumerate every
// discovered id, which was useful while two tasks had manifests and became a
// two-hundred-id wall the moment every task's script moved into this tree — and
// a wall of ids is not a listing, it is the reader's answer buried in noise.
// What actually helps is already in the error: the directory the suite would
// live in and both filenames it was looked for under. The count only says
// whether discovery found anything at all, which distinguishes "you typed the
// wrong id" from "this project has no suites".
func suiteCount(manifests map[string]*verify.Manifest, scripts map[string]dt.Filepath) (n int) {
	var seen map[string]bool

	seen = make(map[string]bool, len(manifests)+len(scripts))
	for id := range manifests {
		seen[id] = true
	}
	for id := range scripts {
		seen[id] = true
	}
	n = len(seen)
	return n
}

// suiteDir renders the directory a task's suite would live in, for the
// not-found error. Naming the directory alongside both filenames turns "no
// suite" into an instruction: this is where it goes, and this is what it is
// called.
func suiteDir(root dt.DirPath, id string) (dir string) {
	return string(root.Join(verify.SuitesDir, strings.ToLower(id)))
}
