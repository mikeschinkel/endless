package verify

import (
	"github.com/mikeschinkel/go-dt"
)

// genericDriver serves every runner family with no registered driver — the
// "shell" type and any arbitrary/vendor tool (bats, Zig, Erlang, …). It execs the
// check's literal command via `sh -c` and requires a normalizable result stream
// on stdout (its declared format, default tap). This is the low-fidelity
// admission path the design keeps friction-y on purpose: the price of verifying
// in any tool is emitting a standardized result, not a bare exit code. It infers
// no format and translates no selection — validation requires a command and
// rejects tests/paths.
type genericDriver struct{}

func (genericDriver) Family() string { return "" }

// Format returns "" because the generic driver's format is DECLARED per check
// (Check.Format, default tap), not inferred from a fixed contract.
func (genericDriver) Format() Format { return "" }

func (genericDriver) Selection() SelectionKind { return SelectionCommandOnly }

// SupportsVariant accepts any variant: a custom "family/variant" runner resolves
// here and the driver runs its literal command verbatim regardless of the
// variant, so there is nothing to reject.
func (genericDriver) SupportsVariant(_ string) (ok bool) { return true }

// Run execs the check's literal command via `sh -c` under root+env, reads the
// declared-format stream from stdout, and normalizes it. The declared format
// defaults to tap when unset (the committed shell/TAP suites rely on this).
func (genericDriver) Run(check Check, root dt.DirPath, env []string, reportPath dt.Filepath) (result RunResult, err error) {
	var format Format

	format = check.Format
	if format == "" {
		format = FormatTAP
	}
	result, err = runAndNormalize([]string{"sh", "-c", check.Command},
		StreamStdout, format, root, env, reportPath)
	return result, err
}
