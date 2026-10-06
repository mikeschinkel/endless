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
// application's state (no task DB, no SQLite). Replacing HOME and
// XDG_CONFIG_HOME is generic isolation — it makes the developer's real config
// unreachable (ED-1583) — and it does NOT hand an Endless-as-SUT suite a fresh
// database: a suite runs in its task's worktree, whose sandbox is addressed by
// `--db sandbox`, not by either variable. Starting that sandbox fresh is a
// separate job (E-1608), not a side effect of this isolation.
//
// Every run starts from a fresh sandbox (E-1608): before any setup step or
// check, the runner resets the worktree's canonical sandbox through the same
// `sandbox reset` a user runs. Freshness is designed in here rather than
// inherited from HOME/XDG isolation.
//
// Tier-0 boundary: a suite that declares needs (substrate escalation, Stage 3+)
// or seed (E-1606) fails loudly rather than run something weaker than asked.
package verifycmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
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
	var keep, printDir *bool
	var rest []string
	var dir dt.DirPath
	var code int
	var err error

	fs := refusal.NewFlags("verify")
	keep = fs.Bool("keep", false,
		"Keep the per-run temp dir (isolated HOME/XDG + intermediates) for debugging")
	printDir = fs.Bool("report-dir", false,
		"Print the directory this task's run reports are written to, and run nothing")
	if err = fs.Parse(args); err != nil {
		// flag.ExitOnError used to print and exit from inside the flag package,
		// which is why there was nothing to classify at this line before. Both
		// of its exits are reproduced here, with flag's own text in both cases
		// so a person reads exactly what they read before: 0 for -h, which is a
		// notice and not a refusal, and 2 for a flag that has to be retyped.
		if errors.Is(err, flag.ErrHelp) {
			refusal.Info(fs.Output()).Print()
			os.Exit(0)
		}
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("verify").Text(fs.Output()).Exit(2)
	}
	rest = fs.Args()
	if len(rest) != 1 {
		// run_verify always passes exactly one id, so a wrong count is somebody
		// invoking the binary by hand.
		refusal.NoReport(
			"Usage: endless-go verify [--keep|--report-dir] <task-id>",
			"Pass exactly one task id and retry",
		).Command("verify").Exit(2)
	}

	if *printDir {
		// `endless task verify` asks rather than re-deriving the cache dir, so
		// where reports live has one definition: reportDir (E-2243).
		dir, err = reportDir(rest[0])
		if err != nil {
			classify(err).Exit(1)
		}
		fmt.Println(string(dir))
		os.Exit(0)
	}

	code, err = run(rest[0], *keep)
	if err != nil {
		classify(err).Exit(1)
	}
	os.Exit(code)
}

// classify names the class of every error `run` can return, at the one place
// they all pass through.
//
// Doing it here rather than at each construction is what the shape of this
// package allows: the errors are doterr sentinels whose rendering — the
// sentinel chain plus the key=value tail — is what a person reads, and the
// sentinel is also what says which failure happened. So the switch resolves the
// inventory's CONDITIONAL relay row in code instead of handing the agent a
// question, for everything except the four that genuinely turn on WHO wrote the
// thing that failed. Nothing this process can see answers that, so those stay
// ReportIf and the agent, which holds the conversation, decides.
//
// The default is a fault, and deliberately: errors from internal/verify carry
// no class yet, and "nobody decided" has to read as Endless failing rather than
// as a quiet invitation to retry. When that package is converted its rows will
// arrive already classified and belong above this line, not below it.
func classify(err error) *refusal.Error {
	var foreign *ForeignLandedSuite

	// The message a person has always read, prefix and all.
	msg := fmt.Sprintf("endless-go verify: %v", err)

	switch {
	case errors.As(err, &foreign):
		// The refusal says what to run instead, so there is nothing to
		// escalate — and it is multi-line, so the verdict carries a compression
		// of it while Text keeps the paragraphs a person reads.
		return refusal.NoReport(
			fmt.Sprintf("endless-go verify: refusing to run %s's verification suite — it has landed, and it is not yours",
				foreign.Requested),
			"Verify your own task instead, or run from your task's worktree",
		).Command("verify").Text(msg)

	case errors.Is(err, ErrNoSuiteForTask):
		return refusal.NoReport(msg,
			"Re-check the task id, or author verify.sh or verify.toml in the directory named above").
			Command("verify")

	case errors.Is(err, ErrTierNotSupported):
		return refusal.ReportIf(msg,
			"the user wrote the needs this manifest declares",
			"restate the manifest without needs and retry",
			"running the suite weaker than it asked for is theirs to allow",
		).Command("verify")

	case errors.Is(err, ErrSeedNotSupported):
		return refusal.ReportIf(msg,
			"the user wrote the seed this manifest declares",
			"restate the manifest without seed and retry",
			"dropping it weakens the proof they asked for",
		).Command("verify")

	case errors.Is(err, ErrScriptStart):
		return refusal.NoReport(msg,
			"Make verify.sh executable and give it a valid shebang, then retry").
			Command("verify")

	case errors.Is(err, ErrSetupStep):
		return refusal.ReportIf(msg,
			"the step failed because a tool the user has to install is missing",
			"fix the setup step and retry",
			"installing that tool is theirs to do",
		).Command("verify")

	case errors.Is(err, ErrCheckFailedNoResults):
		return refusal.ReportIf(msg,
			"the captured stderr names a tool the user has to install",
			"fix the build error or the check command and retry",
			"installing that tool is theirs to do",
		).Command("verify")

	case errors.Is(err, ErrProjectRootNotFound):
		return refusal.ReportIf(msg,
			"this project was never initialized for Endless",
			"cd into the project checkout and retry",
			"initializing a project is theirs to choose",
		).Command("verify")

	case errors.Is(err, ErrResolvingRoot):
		return refusal.Report(msg,
			"what to do about a working directory that can no longer be resolved").
			Command("verify")

	case errors.Is(err, ErrMakingRunDir):
		return refusal.Report(msg,
			"whether to free space or point TMPDIR at a writable directory").
			Command("verify")

	case errors.Is(err, ErrIsolatingEnv):
		return refusal.Report(msg,
			"what to do about a temp directory Endless just created and cannot write into").
			Command("verify")

	case errors.Is(err, ErrWritingCTRF):
		return refusal.Report(msg,
			"whether to fix the cache directory — the suite ran, but its verdict could not be written").
			Command("verify")

	case errors.Is(err, verify.ErrNoResultStream):
		// The script path's stat/read of the TAP file in the run dir. The
		// driver path shares this sentinel with a condition of its own
		// (internal/verify's row); it is left as that package's to resolve.
		return refusal.Report(msg,
			"what to do about a result stream the run directory would not yield").
			Command("verify")
	}
	return refusal.Faultf("%s", msg).Cause(err).Command("verify")
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
		err = resetSandbox(root)
		if err != nil {
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

	err = resetSandbox(root)
	if err != nil {
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
	env, err = suiteEnv(env, id, root)
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
