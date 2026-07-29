package verify

import (
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// pytestDriver is the registered driver for the "pytest" family (ED-1536). Its
// result stream is the pytest-json-report plugin's document written to the report
// file (pytest core emits no JSON), so the inferred format is pytest-json. The
// family exists to carry launcher variants: the declared "pytest/uv" invokes the
// uv project's pytest, while bare "pytest" resolves a launcher by a documented
// fallback precedence. Both variants share one driver, dispatching on the
// variant parsed off the check's Runner.
type pytestDriver struct{}

func (pytestDriver) Family() string { return "pytest" }

func (pytestDriver) Format() Format { return FormatPytestJSON }

func (pytestDriver) Selection() SelectionKind { return SelectionStructured }

// SupportsVariant accepts the bare form ("") and the declared "uv" launcher.
// Other launchers (pytest/venv, pytest/poetry) are deliberately not built yet —
// the seam accommodates them as future variants, but declaring one now is an
// error rather than a silent fallthrough.
func (pytestDriver) SupportsVariant(variant string) (ok bool) {
	switch variant {
	case "", "uv":
		ok = true
	}
	return ok
}

// Run executes the check. A structured selection resolves the launcher for the
// declared variant (see resolvePytestLauncher), then invokes pytest with
// --json-report writing to reportPath (the stream is that file). A command-mode
// check runs its literal command and is expected to emit the pytest-json document
// on stdout. Either way the stream normalizes with pytest-json.
func (pytestDriver) Run(check Check, root dt.DirPath, env []string, reportPath dt.Filepath) (result RunResult, err error) {
	var argv, launcher []string
	var variant string

	if check.Command != "" {
		result, err = runAndNormalize([]string{"sh", "-c", check.Command},
			StreamStdout, FormatPytestJSON, root, env, reportPath)
		goto end
	}

	_, variant, err = parseRunner(check.Runner)
	if err != nil {
		goto end
	}
	launcher, err = resolvePytestLauncher(variant, root)
	if err != nil {
		goto end
	}

	argv = make([]string, 0, len(launcher)+len(check.Paths)+len(check.Tests)+2)
	argv = append(argv, launcher...)
	argv = append(argv, check.Paths...)
	argv = append(argv, check.Tests...)
	argv = append(argv, "--json-report", "--json-report-file="+string(reportPath))

	result, err = runAndNormalize(argv, StreamFile, FormatPytestJSON, root, env, reportPath)
end:
	return result, err
}

// resolvePytestLauncher resolves the argv prefix that launches pytest for a
// variant, robustly under the runner's HOME/XDG isolation. The uv project's own
// venv holds a plain `pytest` executable after `uv sync`; it needs no network and
// survives a temp HOME, so it is preferred. `uv run` is the next choice (passed
// --no-sync so a cold uv cache under the temp HOME cannot trigger a network
// sync). The bare "" family adds a final fallback to `pytest` on PATH; the
// declared "uv" variant does not fall through to PATH — declaring uv means the
// project's pytest, not whatever bare pytest happens to be installed.
//
// PATH lookups (uv, pytest) use the ambient PATH, which equals the runner's
// isolated env: isolation replaces only HOME/XDG_CONFIG_HOME, never PATH. The
// robustness the contract requires comes from preferring the on-disk venv
// executable, which needs no network under a temp HOME.
func resolvePytestLauncher(variant string, root dt.DirPath) (launcher []string, err error) {
	var venvPytest dt.Filepath
	var lerr error

	venvPytest = dt.FilepathJoin(root, ".venv/bin/pytest")
	if isExecutableFile(venvPytest) {
		launcher = []string{string(venvPytest)}
		goto end
	}
	_, lerr = exec.LookPath("uv")
	if lerr == nil {
		launcher = []string{"uv", "run", "--no-sync", "pytest"}
		goto end
	}
	if variant == "" {
		_, lerr = exec.LookPath("pytest")
		if lerr == nil {
			launcher = []string{"pytest"}
			goto end
		}
	}
	err = doterr.NewErr(ErrPytestLauncher,
		"runner", pytestRunner(variant), "root", string(root),
		"tried", pytestTried(variant))
end:
	return launcher, err
}

// isExecutableFile reports whether fp is an existing regular file with an execute
// bit set — the test for a usable venv `pytest` executable.
func isExecutableFile(fp dt.Filepath) (ok bool) {
	var info os.FileInfo
	var err error

	info, err = os.Stat(string(fp))
	if err != nil {
		goto end
	}
	ok = info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
end:
	return ok
}

// pytestRunner renders the runner string for a resolved variant, for error
// metadata ("pytest" or "pytest/uv").
func pytestRunner(variant string) (runner string) {
	runner = "pytest"
	if variant != "" {
		runner += "/" + variant
	}
	return runner
}

// pytestTried renders the launcher fallback chain a variant attempts, for the
// ErrPytestLauncher metadata so a failure names what was looked for.
func pytestTried(variant string) (tried string) {
	chain := []string{".venv/bin/pytest", "uv run --no-sync pytest"}
	if variant == "" {
		chain = append(chain, "pytest (PATH)")
	}
	tried = strings.Join(chain, " -> ")
	return tried
}

// pytestCmd builds the bare-clone `pytest` command from a structured selection.
// Pytest accepts both file/dir paths and path::test nodeids as positional
// arguments, so Paths and Tests both become positional selectors (paths first).
// With neither, it collects the whole suite. This is used only by the bare-clone
// bridge (bareCloneCommand); the execution path resolves a launcher instead.
func pytestCmd(tests, paths []string) (cmd string) {
	var args []string

	args = make([]string, 0, len(paths)+len(tests))
	args = append(args, paths...)
	args = append(args, tests...)

	cmd = "pytest"
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	return cmd
}
