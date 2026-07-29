package verifycmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// findProjectRoot walks up from the current directory to the first ancestor that
// contains a .endless directory — the project (or worktree) checkout root whose
// .endless/tasks/*/verify.toml suites Discover reads. It fails loudly if none is
// found before the filesystem root.
func findProjectRoot() (root dt.DirPath, err error) {
	var cwd, dir, parent dt.DirPath
	var exists bool

	cwd, err = dt.Getwd()
	if err != nil {
		err = doterr.NewErr(ErrResolvingRoot, err)
		goto end
	}
	for dir = cwd; ; dir = parent {
		exists, _ = dir.Join(".endless").Exists()
		if exists {
			root = dir
			goto end
		}
		parent = dir.Dir()
		if parent == dir {
			err = doterr.NewErr(ErrProjectRootNotFound, "cwd", cwd)
			goto end
		}
	}
end:
	return root, err
}

// makeRunDir creates a fresh per-run temp directory (the Tier-0 isolation
// substrate: it holds the temp HOME/XDG and per-check intermediates). The OS
// temp root gives each run — and so each concurrent `endless task verify` — its own
// unique directory, which is what keeps concurrent runs from colliding.
func makeRunDir() (dir dt.DirPath, err error) {
	var s string

	s, err = os.MkdirTemp("", "endless-verify-")
	if err != nil {
		err = doterr.NewErr(ErrMakingRunDir, err)
		goto end
	}
	dir = dt.DirPath(s)
end:
	return dir, err
}

// isolatedEnv creates the temp HOME and XDG_CONFIG_HOME under runDir and returns
// the environment the suite runs under: the parent environment with HOME and
// XDG_CONFIG_HOME replaced so the suite cannot read or pollute the developer's
// real home/config. Nothing here is app-specific; that Endless's own DB lives
// under XDG and thus gets isolated for free is incidental.
func isolatedEnv(runDir dt.DirPath) (env []string, err error) {
	var home, xdg dt.DirPath

	home = runDir.Join("home")
	xdg = runDir.Join("xdg")
	err = home.MkdirAll(0o755)
	if err != nil {
		err = doterr.NewErr(ErrIsolatingEnv, err, "dir", home)
		goto end
	}
	err = xdg.MkdirAll(0o755)
	if err != nil {
		err = doterr.NewErr(ErrIsolatingEnv, err, "dir", xdg)
		goto end
	}
	env = replaceEnv(os.Environ(), string(home), string(xdg))
end:
	return env, err
}

// replaceEnv returns base with any existing HOME / XDG_CONFIG_HOME entries
// dropped and fresh ones appended, so the isolated values win deterministically
// regardless of the platform's duplicate-key resolution.
func replaceEnv(base []string, home, xdg string) (env []string) {
	env = make([]string, 0, len(base)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "XDG_CONFIG_HOME="+xdg)
	return env
}

// runSetup runs the merged setup steps in order under the isolated env, with cwd
// at the project root. A step that fails to start or exits non-zero aborts the
// run loudly (setup is a precondition, not a test).
func runSetup(steps []string, root dt.DirPath, env []string) (err error) {
	var stderr []byte
	var exit int

	for i, step := range steps {
		_, stderr, exit, err = runShell(step, root, env)
		if err != nil {
			err = doterr.NewErr(ErrSetupStep, err, "index", i, "step", step)
			goto end
		}
		if exit != 0 {
			err = doterr.NewErr(ErrSetupStep, "index", i, "step", step, "exit", exit, "stderr", tail(stderr))
			goto end
		}
	}
end:
	return err
}

// runChecks resolves each check's driver and lets it execute the run — select,
// invoke via os/exec, capture the native stream (stdout or a per-check report
// file), and normalize to a CTRF report. runChecks owns the per-check report-file
// allocation and the exit-semantics guard: a check that exits non-zero without
// reporting any test failure (a build/runner error, not a test failure) aborts
// loudly. A driver that could not start, emit, or parse its stream returns an
// error that aborts the run. Genuine test failures are collected and reported.
func runChecks(checks []verify.Check, root dt.DirPath, env []string, runDir dt.DirPath) (results []checkResult, err error) {
	var reportsDir dt.DirPath

	reportsDir = runDir.Join("reports")
	err = reportsDir.MkdirAll(0o755)
	if err != nil {
		err = doterr.NewErr(ErrMakingRunDir, err, "dir", reportsDir)
		goto end
	}

	for i, chk := range checks {
		reportFile := dt.FilepathJoin(reportsDir, fmtCheckReport(i))

		driver, derr := verify.LookupDriver(chk.Runner)
		if derr != nil {
			err = doterr.NewErr(derr, "check", i, "runner", chk.Runner)
			goto end
		}

		res, rerr := driver.Run(chk, root, env, reportFile)
		if rerr != nil {
			err = doterr.NewErr(rerr, "check", i, "runner", chk.Runner)
			goto end
		}

		// A non-zero exit not explained by any parsed failure is a build or
		// runner error (e.g. a package that failed to compile emits no test
		// events), which must not pass as a clean run.
		if res.Exit != 0 && res.Report.Results.Summary.Failed == 0 {
			err = doterr.NewErr(ErrCheckFailedNoResults,
				"check", i, "runner", chk.Runner, "exit", res.Exit, "stderr", tail(res.Stderr))
			goto end
		}
		results = append(results, checkResult{index: i, runner: chk.Runner, report: res.Report})
	}
end:
	return results, err
}

// runShell runs cmdStr via `sh -c` with cwd at the project root and the isolated
// env, capturing stdout and stderr. A non-zero process exit is returned as exit
// (with err nil) so callers can distinguish an expected test failure from an
// infrastructure error (err non-nil means the process could not run at all).
func runShell(cmdStr string, root dt.DirPath, env []string) (stdout, stderr []byte, exit int, err error) {
	var outBuf, errBuf bytes.Buffer
	var ee *exec.ExitError

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = string(root)
	cmd.Env = env
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = outBuf.Bytes()
	stderr = errBuf.Bytes()
	if err != nil && errors.As(err, &ee) {
		exit = ee.ExitCode()
		err = nil
	}
	return stdout, stderr, exit, err
}

// teardown runs the suite's teardown steps (always, mirroring E-1618's shell
// trap) and then removes the per-run temp dir unless keep is set. env is passed
// by pointer because it is filled in after teardown is deferred; a nil env means
// isolation never completed, so the steps (which must run isolated) are skipped.
// Teardown failures are reported but never change the run's exit code.
func teardown(root dt.DirPath, envp *[]string, steps []string, runDir dt.DirPath, keep bool) {
	var env []string

	if envp != nil {
		env = *envp
	}
	if env != nil {
		for i, step := range steps {
			_, stderr, exit, err := runShell(step, root, env)
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr, "endless-go verify: teardown step %d (%q) failed to start: %v\n", i, step, err)
			case exit != 0:
				fmt.Fprintf(os.Stderr, "endless-go verify: teardown step %d (%q) exited %d: %s\n", i, step, exit, tail(stderr))
			}
		}
	}
	if keep {
		fmt.Fprintf(os.Stderr, "kept per-run dir: %s\n", displayPath(string(runDir)))
		return
	}
	if err := runDir.RemoveAll(); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go verify: could not remove per-run dir %s: %v\n", displayPath(string(runDir)), err)
	}
}

// displayPath renders path cwd-relative, else under ~, else unchanged — the
// house convention for user-facing paths (mirrors monitor.displayPath).
func displayPath(path string) (out string) {
	var cwd, home, rel string
	var err error

	out = path
	cwd, err = os.Getwd()
	if err == nil {
		rel, err = filepath.Rel(cwd, path)
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			out = rel
			goto end
		}
	}
	home, err = os.UserHomeDir()
	if err == nil && home != "" {
		rel, err = filepath.Rel(home, path)
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			out = filepath.Join("~", rel)
			goto end
		}
	}
end:
	return out
}

// tail trims and caps stderr for error metadata so a huge failing-command dump
// doesn't swamp the message.
func tail(b []byte) (s string) {
	const max = 2000

	s = strings.TrimSpace(string(b))
	if len(s) > max {
		s = "..." + s[len(s)-max:]
	}
	return s
}
