package verify

import (
	"bytes"
	"errors"
	"os/exec"
	"sort"
	"strings"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// RunnerDriver owns a registered runner-type's level-2 behavioral contract
// (ED-1535/1536) and EXECUTES a check itself: it selects (translates the
// structured tests/paths into the native filter), invokes the tool via os/exec
// under the caller's root + isolated env, captures the native result stream
// (stdout or a report file), and normalizes it to the CTRF-subset Report. A
// driver never emits a command string for an outer shell to run — delegating
// execution to a shell would be a second engine Endless does not control.
//
// Drivers are keyed by runner FAMILY (the part before an optional "/variant";
// see parseRunner). One driver serves every variant of its family — the pytest
// driver handles both bare "pytest" and "pytest/uv" — reading the declared
// variant off the check's Runner at Run time. A runner naming no registered
// family resolves to the generic driver, which runs the literal command.
type RunnerDriver interface {
	// Family is the registered runner family this driver serves ("gotest",
	// "pytest"); the generic driver returns "" (it serves every unregistered
	// family).
	Family() string

	// Format is the fixed result-stream format a structured driver's contract
	// emits (gotest-json, pytest-json). The generic driver returns "" because its
	// format is declared per-check rather than inferred.
	Format() Format

	// Selection reports whether the driver's contract accepts a structured
	// tests/paths selection (SelectionStructured) or requires a literal command
	// (SelectionCommandOnly). Validation enforces the two-form rules from this.
	Selection() SelectionKind

	// SupportsVariant reports whether variant (the part after "family/") is one
	// this driver implements. A structured family that ships only its bare form
	// accepts "" and rejects everything else; the generic driver accepts any
	// variant (a custom "family/variant" it runs verbatim).
	SupportsVariant(variant string) (ok bool)

	// Run executes check under root with env, captures its native result stream
	// (writing a file-emitting runner's stream to reportPath), and normalizes it
	// to a CTRF-subset report. The returned RunResult carries the process exit
	// code and captured stderr so the caller can apply the non-zero-exit /
	// no-failures build-error guard. An error means the tool could not run, emit
	// a stream, or produce a parseable result — never a mere test failure.
	Run(check Check, root dt.DirPath, env []string, reportPath dt.Filepath) (result RunResult, err error)
}

// SelectionKind classifies a driver's contract by whether it accepts a
// structured (tests/paths) selection Endless translates to the native filter, or
// only a literal command it runs verbatim.
type SelectionKind int

const (
	// SelectionStructured drivers (gotest, pytest) translate tests/paths to the
	// native filter and infer their result format; an explicit command is an
	// accepted escape hatch.
	SelectionStructured SelectionKind = iota
	// SelectionCommandOnly drivers (generic) require a literal command and a
	// declared result format; tests/paths are not valid.
	SelectionCommandOnly
)

// StreamSource identifies where a driver's tool emits its native result stream
// so Run knows how to capture the bytes to normalize: StreamStdout means the
// stream is the command's standard output (gotest -json, a TAP-emitting shell
// command); StreamFile means the tool writes it to the report path Endless
// supplied (pytest's json-report plugin emits a file, not stdout).
type StreamSource int

const (
	StreamStdout StreamSource = iota
	StreamFile
)

// RunResult is a driver's normalized outcome for one check: the CTRF-subset
// Report, plus the process Exit code and captured Stderr. Exit and Stderr let
// the runner distinguish a genuine test failure (Report.Summary.Failed > 0) from
// a build/runner error (non-zero exit that produced no parsed failures).
type RunResult struct {
	Report *Report
	Exit   int
	Stderr []byte
}

// driverRegistry is the canonical registry of registered runner-type drivers,
// keyed by family. Adding a first-class runner later is one entry here plus its
// driver file. A family absent from this map resolves to the generic driver.
var driverRegistry = map[string]RunnerDriver{
	"gotest": gotestDriver{},
	"pytest": pytestDriver{},
}

// LookupDriver resolves a runner string to the driver that executes it. It parses
// the family/variant (ED-1537 subset), rejects a malformed name, resolves the
// family to its registered driver (or the generic driver when unregistered), and
// rejects a variant the resolved driver does not implement. It assumes nothing
// about validation order — callers on the validated path never see an error.
func LookupDriver(runner string) (d RunnerDriver, err error) {
	var family, variant string
	var ok bool

	family, variant, err = parseRunner(runner)
	if err != nil {
		goto end
	}
	d, ok = driverRegistry[family]
	if !ok {
		d = genericDriver{}
	}
	if !d.SupportsVariant(variant) {
		d = nil
		err = doterr.NewErr(ErrUnknownVariant,
			"runner", runner, "family", family, "variant", variant)
		goto end
	}
end:
	return d, err
}

// parseRunner splits a runner string into its family and optional variant on a
// single "/" (the ED-1537 grammar subset: "/" separates family from variant, as
// in "pytest/uv"). An empty family, an empty variant (trailing "/"), or more than
// one "/" is malformed and rejected — the ".": vendor-namespace nesting and the
// full vnd.* tree are E-1793, out of scope here.
func parseRunner(runner string) (family, variant string, err error) {
	var parts []string

	parts = strings.Split(runner, "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		family = parts[0]
	case len(parts) == 2 && parts[0] != "" && parts[1] != "":
		family = parts[0]
		variant = parts[1]
	default:
		err = doterr.NewErr(ErrMalformedRunner, "runner", runner)
	}
	return family, variant, err
}

// driverFamilies renders the registered driver family names as a sorted,
// comma-separated string for error metadata (which families accept a structured
// selection).
func driverFamilies() (list string) {
	var names []string

	names = make([]string, 0, len(driverRegistry))
	for name := range driverRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	list = strings.Join(names, ", ")
	return list
}

// runAndNormalize is the shared execution core every driver's Run funnels
// through: it runs argv under root+env, captures stdout/stderr/exit, reads the
// native stream per src (stdout, or reportPath for a file-emitting tool), and
// normalizes it with format. It returns a RunResult on any completed run —
// including a failing one — and a non-nil error only when the tool could not
// start, wrote no readable stream, or emitted an unparseable one.
func runAndNormalize(argv []string, src StreamSource, format Format, root dt.DirPath, env []string, reportPath dt.Filepath) (result RunResult, err error) {
	var stdout, stderr, raw []byte
	var exit int
	var rpt *Report

	stdout, stderr, exit, err = execCapture(argv, root, env)
	if err != nil {
		err = doterr.NewErr(ErrCheckStart, err, "command", strings.Join(argv, " "))
		goto end
	}

	switch src {
	case StreamStdout:
		raw = stdout
	case StreamFile:
		raw, err = reportPath.ReadFile()
		if err != nil {
			err = doterr.NewErr(ErrNoResultStream, err,
				"report", string(reportPath), "exit", exit, "stderr", tailBytes(stderr))
			goto end
		}
	}

	rpt, err = Normalize(format, raw)
	if err != nil {
		err = doterr.NewErr(err, "exit", exit, "stderr", tailBytes(stderr))
		goto end
	}
	result = RunResult{Report: rpt, Exit: exit, Stderr: stderr}
end:
	return result, err
}

// execCapture runs argv directly (no intervening shell) with cwd at root and the
// isolated env, capturing stdout and stderr. A non-zero process exit is returned
// as exit with err nil so a driver can tell an expected test failure from an
// infrastructure error (err non-nil means the process could not run at all). A
// driver that needs shell semantics (the generic driver's literal command)
// passes argv = {"sh", "-c", command}.
func execCapture(argv []string, root dt.DirPath, env []string) (stdout, stderr []byte, exit int, err error) {
	var outBuf, errBuf bytes.Buffer
	var ee *exec.ExitError
	var cmd *exec.Cmd

	cmd = exec.Command(argv[0], argv[1:]...)
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

// tailBytes trims and caps captured stderr for error metadata so a huge failing
// command dump doesn't swamp the message. It mirrors verifycmd.tail (the two
// packages keep their own copy of this two-line helper rather than export it).
func tailBytes(b []byte) (s string) {
	const max = 2000

	s = strings.TrimSpace(string(b))
	if len(s) > max {
		s = "..." + s[len(s)-max:]
	}
	return s
}
