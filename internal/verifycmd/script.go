package verifycmd

import (
	"errors"
	"os"
	"os/exec"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// Environment the runner exports into a script suite. These are the seam
// between the runner and a suite that sources the shared harness, and they are
// the reason a harness-sourcing script cannot be run any other way.
const (
	// There is deliberately no "you were started by the runner" marker here.
	// ENDLESS_VERIFY_RUN was one, and it was a claim rather than a check:
	// nothing ever read its value, one `export` satisfied it, and its presence
	// stood in for enforcement that was never built behind it. E-2090 replaced
	// it with .endless/tasks/_guard.sh, which asks the two questions directly —
	// is this suite in its own task's worktree, and is a real config reachable
	// from here — neither of which the caller can answer for itself.

	// EnvTAPPath is where a suite writes its TAP stream. Writing to a FILE
	// rather than stdout is what lets the suite keep its own human-readable
	// output on the terminal, live and in colour, while still handing the
	// runner per-assertion results to normalize.
	EnvTAPPath = "ENDLESS_VERIFY_TAP"

	// EnvTaskID is the task being verified, in canonical E-NNNN form, so a
	// suite can name itself without hardcoding an id twice.
	EnvTaskID = "ENDLESS_VERIFY_TASK"

	// EnvSuiteDir is the task's own suite directory. A suite that ships a
	// helper file beside itself reads it from here instead of retyping the
	// path — which is the same class of mistake in a manifest as a hardcoded
	// id: .endless/tasks/e-1603/verify.toml named its own directory "E-1603",
	// a casing discovery tolerates and the shell does not, so the check ran
	// only because APFS is case-insensitive.
	EnvSuiteDir = "ENDLESS_VERIFY_DIR"
)

// tapFile is the per-run filename a script suite's TAP stream is written to.
const tapFile = "verify.tap"

// runScriptSuite runs a task's verify.sh under the same Tier-0 isolation the
// manifest path uses — a fresh temp dir plus a temp HOME and XDG_CONFIG_HOME —
// and normalizes its outcome into the same CTRF envelope.
//
// The script's stdout and stderr are passed STRAIGHT THROUGH rather than
// captured. A script suite's output is its report: it prints a pass/fail line
// per check and a summary, and a reader watching a two-minute suite needs to see
// that as it happens. Handing it the real terminal also preserves the isatty
// colour decision every one of these scripts already makes. The structured
// results arrive by the other channel (EnvTAPPath), which is why nothing is lost
// by not capturing.
//
// The returned code is the SCRIPT's exit code, verbatim — so a suite's verdict
// through the runner is identical to what it was when it was executed directly,
// including the exit-2 setup-error convention these scripts share.
func runScriptSuite(id string, script dt.Filepath, root dt.DirPath, keep bool) (code int, err error) {
	var runDir dt.DirPath
	var env []string
	var tapPath dt.Filepath
	var rpt *verify.Report
	var merged *verify.Report
	var ctrfPath dt.Filepath
	var results []checkResult

	runDir, err = makeRunDir()
	if err != nil {
		goto end
	}
	defer teardown(root, &env, nil, runDir, keep)

	env, err = isolatedEnv(runDir)
	if err != nil {
		goto end
	}

	env, err = suiteEnv(env, id, root)
	if err != nil {
		goto end
	}

	tapPath, err = scriptTAPPath(runDir)
	if err != nil {
		goto end
	}
	env = append(env, EnvTAPPath+"="+string(tapPath))

	code, err = execScript(script, root, env)
	if err != nil {
		goto end
	}

	rpt, err = scriptReport(tapPath, code)
	if err != nil {
		goto end
	}

	results = []checkResult{{index: 0, runner: verify.ScriptFile, report: rpt}}
	merged = mergeResults(results)
	ctrfPath, err = writeCTRF(id, merged)
	if err != nil {
		goto end
	}
	printSummary(id, results, merged, ctrfPath)
end:
	return code, err
}

// suiteEnv appends the variables every suite gets, in EITHER form: the task id
// and the suite's own directory. They are added in one place rather than at
// each call site because a variable a script suite can rely on and a manifest
// suite cannot is a difference nobody would predict from the outside.
//
// Neither carries any authority. What a suite is ALLOWED to do is decided by
// _guard.sh from facts no environment can restate — the path the running file
// sits at, and whether a real config is reachable — so there is nothing here a
// caller could set to grant itself permission.
//
// EnvTAPPath is deliberately NOT here. It names where a single script suite
// writes its result stream, and a manifest's checks each declare and emit their
// own — so exporting it there would promise a channel nothing reads.
func suiteEnv(env []string, id string, root dt.DirPath) (out []string, err error) {
	var dir dt.DirPath
	var ok bool

	dir, ok, err = verify.SuiteDir(root, id)
	if err != nil {
		goto end
	}
	out = append(env,
		EnvTaskID+"="+verify.NormalizeTaskID(id),
	)
	if ok {
		out = append(out, EnvSuiteDir+"="+string(dir))
	}
end:
	if err != nil {
		out = nil
	}
	return out, err
}

// scriptTAPPath allocates the per-run TAP destination under the run dir's
// reports directory, alongside where the manifest path puts its per-check
// intermediates.
func scriptTAPPath(runDir dt.DirPath) (fp dt.Filepath, err error) {
	var reportsDir dt.DirPath

	reportsDir = runDir.Join("reports")
	err = reportsDir.MkdirAll(0o755)
	if err != nil {
		err = doterr.NewErr(ErrMakingRunDir, err, "dir", reportsDir)
		goto end
	}
	fp = dt.FilepathJoin(reportsDir, tapFile)
end:
	return fp, err
}

// execScript runs the suite with cwd at the project root and the isolated env,
// inheriting the terminal. It is exec'd directly so its own shebang chooses the
// interpreter — these are bash scripts, and running them under `sh` would change
// what they mean. A non-zero exit is a result, not an error; err is non-nil only
// when the script could not be started at all.
func execScript(script dt.Filepath, root dt.DirPath, env []string) (exit int, err error) {
	var cmd *exec.Cmd
	var ee *exec.ExitError

	cmd = exec.Command(string(script))
	cmd.Dir = string(root)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	switch {
	case err == nil:
	case errors.As(err, &ee):
		exit = ee.ExitCode()
		err = nil
	default:
		err = doterr.NewErr(ErrScriptStart, err, "script", script)
	}
	return exit, err
}

// scriptReport builds the CTRF report for a script run: the suite's own TAP
// stream when it emitted one, else a single result derived from the exit code.
//
// The fallback is what keeps a suite that predates the harness runnable through
// the front door with no rewrite. Both paths then get the same exit-code
// reconciliation, so a report can never disagree with the verdict the process
// actually returned.
func scriptReport(tapPath dt.Filepath, exit int) (rpt *verify.Report, err error) {
	var raw []byte
	var exists bool

	exists, err = tapPath.Exists()
	if err != nil {
		err = doterr.NewErr(verify.ErrNoResultStream, err, "filepath", tapPath)
		goto end
	}
	if !exists {
		rpt = verify.ExitCodeReport(verify.ScriptFile, exit)
		goto end
	}

	raw, err = tapPath.ReadFile()
	if err != nil {
		err = doterr.NewErr(verify.ErrNoResultStream, err, "filepath", tapPath)
		goto end
	}

	rpt, err = verify.Normalize(verify.FormatTAP, raw)
	if err != nil {
		goto end
	}
	if rpt.Results.Summary.Tests == 0 {
		rpt = verify.ExitCodeReport(verify.ScriptFile, exit)
		goto end
	}
	verify.AppendExitFailure(rpt, verify.ScriptFile, exit)
end:
	return rpt, err
}
