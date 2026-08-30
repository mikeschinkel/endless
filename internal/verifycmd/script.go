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
	// EnvRunMarker proves the suite was started BY the runner. The shared
	// harness refuses when it is absent, so a direct `./verify.sh` exits
	// non-zero with a message naming the sanctioned command instead of running
	// outside the isolation and the own-task-only guard.
	//
	// It is deliberately not documented as a user-settable escape. An override
	// an agent can set is an override an agent can rationalize, which is the
	// exact failure this whole task exists to stop. Its value is the per-run
	// temp directory, which is useful when debugging and useless as a bypass.
	EnvRunMarker = "ENDLESS_VERIFY_RUN"

	// EnvTAPPath is where a suite writes its TAP stream. Writing to a FILE
	// rather than stdout is what lets the suite keep its own human-readable
	// output on the terminal, live and in colour, while still handing the
	// runner per-assertion results to normalize.
	EnvTAPPath = "ENDLESS_VERIFY_TAP"

	// EnvTaskID is the task being verified, in canonical E-NNNN form, so a
	// suite can name itself without hardcoding an id twice.
	EnvTaskID = "ENDLESS_VERIFY_TASK"
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

	tapPath, err = scriptTAPPath(runDir)
	if err != nil {
		goto end
	}
	env = append(env,
		EnvRunMarker+"="+string(runDir),
		EnvTAPPath+"="+string(tapPath),
		EnvTaskID+"="+verify.NormalizeTaskID(id),
	)

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
