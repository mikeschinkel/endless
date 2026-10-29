package verify

import (
	"github.com/mikeschinkel/go-doterr"
)

// Check is one runner invocation contributing to a task's verification proof. A
// Manifest holds a list of these (the [[check]] array): one ticket composes
// multiple runners into one report. A check takes one of two forms, determined by
// the SelectionKind of the driver its Runner resolves to (see LookupDriver).
//
// Structured driver (Runner names a registered family whose driver accepts a
// structured selection, e.g. gotest or pytest/uv): Endless infers the result
// Format from the driver and translates a structured selection (Tests names +
// optional Paths scope) into the native filter the driver executes. An explicit
// Command is also accepted as an escape hatch for selections the structured form
// can't express; an explicit Format is accepted only when it equals the inferred
// value.
//
// Command-only driver (any other Runner — the generic driver): Command is the
// literal invocation Endless runs verbatim, and Format (default tap) declares the
// native result stream it emits. Tests and Paths are not valid on such a check.
//
// A third form needs no runner at all: a shell check (Sh) runs one command and
// passes on exit 0, optionally matching its stdout (Contains, NotContains,
// Equals). It is one result, built from the exit code and the matchers rather
// than parsed from a stream, so a fact no native test covers needs no TAP.
type Check struct {
	// Name labels this check's result: the test name of a shell check, and
	// the "check" label on every test of any check (see LabelCheck).
	Name string `toml:"name"`

	Runner  string   `toml:"runner"`
	Tests   []string `toml:"tests"`
	Paths   []string `toml:"paths"`
	Command string   `toml:"command"`
	Format  Format   `toml:"format"`

	// Sh is a shell check's command, run with `sh -c` at the worktree root. It
	// excludes Runner, Command, Tests, Paths and Format.
	Sh string `toml:"sh"`

	// The matchers a shell check may add to its exit code, each read against
	// its stdout. Equals ignores trailing newlines, as `$(...)` does, and is a
	// pointer so that equals = "" (no output at all) is a matcher too. A check
	// that needs stderr matched redirects it with 2>&1.
	Contains    string  `toml:"contains"`
	NotContains string  `toml:"not_contains"`
	Equals      *string `toml:"equals"`

	// As names whose environment this check runs in. Empty (or "person") is the
	// default every check gets: a person's, whoever started the run. "agent"
	// layers the supported harness's environment on top (E-2278), so a check
	// that tests the agent's experience says so instead of inheriting it from
	// whoever happened to call the runner.
	As string `toml:"as"`

	// Tmux runs this check inside a fixture pane on the run's private tmux
	// server, with TMUX and TMUX_PANE naming it. Without it a check runs
	// outside tmux, and no check can reach the user's live server either way.
	Tmux bool `toml:"tmux"`
}

// The values Check.As accepts.
const (
	AsPerson = "person"
	AsAgent  = "agent"
)

// ResolvedFormat returns the native result-stream format this check emits: the
// driver's inferred format for a structured driver, or the declared Format
// (defaulting to tap) for a command-only (generic) check. It assumes the check
// has passed validation, so a driver always resolves.
func (c Check) ResolvedFormat() (format Format) {
	var d RunnerDriver
	var err error

	d, err = LookupDriver(c.Runner)
	switch {
	case c.Sh != "":
		// A shell check's result is built from its exit code; no stream.
		format = ""
	case err == nil && d.Format() != "":
		format = d.Format()
	case c.Format != "":
		format = c.Format
	default:
		format = FormatTAP
	}
	return format
}

// validateCheck enforces the two-form rules for a single check against the driver
// its runner resolves to. Errors wrap ErrInvalidManifest. The index is attached
// as metadata so a failing check in a list is identifiable.
func validateCheck(c Check, index int) (err error) {
	var d RunnerDriver

	switch c.As {
	case "", AsPerson, AsAgent:
	default:
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownCheckAs,
			"index", index, "as", c.As, "allowed", AsPerson+", "+AsAgent)
		goto end
	}

	if c.Sh != "" {
		err = validateShellCheck(c, index)
		goto end
	}

	if key := c.matcherKey(); key != "" {
		err = doterr.NewErr(ErrInvalidManifest, ErrMatcherRequiresSh,
			"index", index, "key", key)
		goto end
	}

	if c.Runner == "" {
		err = doterr.NewErr(ErrInvalidManifest, ErrCheckMissingRunner, "index", index)
		goto end
	}

	d, err = LookupDriver(c.Runner)
	if err != nil {
		err = doterr.NewErr(ErrInvalidManifest, err, "index", index, "runner", c.Runner)
		goto end
	}

	switch d.Selection() {
	case SelectionStructured:
		err = validateStructuredCheck(c, d, index)
	case SelectionCommandOnly:
		err = validateCommandCheck(c, index)
	}
end:
	return err
}

// validateStructuredCheck validates a check whose driver accepts a structured
// selection: command XOR a (tests/paths) selection, and an explicit format only
// when it matches the driver's inferred one.
func validateStructuredCheck(c Check, d RunnerDriver, index int) (err error) {
	hasSelection := len(c.Tests) > 0 || len(c.Paths) > 0

	switch {
	case c.Command != "" && hasSelection:
		err = doterr.NewErr(ErrInvalidManifest, ErrFirstClassCommandConflict,
			"index", index, "runner", c.Runner)
	case c.Command == "" && !hasSelection:
		err = doterr.NewErr(ErrInvalidManifest, ErrFirstClassNeedsSelection,
			"index", index, "runner", c.Runner)
	case c.Format != "" && c.Format != d.Format():
		err = doterr.NewErr(ErrInvalidManifest, ErrFormatMismatch,
			"index", index, "runner", c.Runner,
			"format", string(c.Format), "inferred", string(d.Format()))
	}
	return err
}

// validateCommandCheck validates a check bound to the command-only generic
// driver: a literal command is required, tests/paths are not allowed, and a
// declared format (if any) must be known.
func validateCommandCheck(c Check, index int) (err error) {
	switch {
	case len(c.Tests) > 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrTestsRequireFirstClass,
			"index", index, "runner", c.Runner, "structured", driverFamilies())
	case len(c.Paths) > 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrPathsRequireFirstClass,
			"index", index, "runner", c.Runner, "structured", driverFamilies())
	case c.Command == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrRawCheckNeedsCommand,
			"index", index, "runner", c.Runner)
	case c.Format != "" && !c.Format.Valid():
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownFormat,
			"index", index, "format", string(c.Format), "supported", formatList())
	}
	return err
}

// validateShellCheck validates a shell check: sh stands alone, so a key that
// selects or describes a runner is refused by name rather than ignored.
func validateShellCheck(c Check, index int) (err error) {
	var key string

	switch {
	case c.Runner != "":
		key = "runner"
	case c.Command != "":
		key = "command"
	case len(c.Tests) > 0:
		key = "tests"
	case len(c.Paths) > 0:
		key = "paths"
	case c.Format != "":
		key = "format"
	}
	if key != "" {
		err = doterr.NewErr(ErrInvalidManifest, ErrShConflict, "index", index, "key", key)
	}
	return err
}

// matcherKey names the first output matcher this check sets, or "" when it
// sets none.
func (c Check) matcherKey() (key string) {
	switch {
	case c.Contains != "":
		key = "contains"
	case c.NotContains != "":
		key = "not_contains"
	case c.Equals != nil:
		key = "equals"
	}
	return key
}
