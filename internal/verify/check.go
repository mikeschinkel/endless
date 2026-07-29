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
type Check struct {
	Runner  string   `toml:"runner"`
	Tests   []string `toml:"tests"`
	Paths   []string `toml:"paths"`
	Command string   `toml:"command"`
	Format  Format   `toml:"format"`
}

// ResolvedFormat returns the native result-stream format this check emits: the
// driver's inferred format for a structured driver, or the declared Format
// (defaulting to tap) for a command-only (generic) check. It assumes the check
// has passed validation, so a driver always resolves.
func (c Check) ResolvedFormat() (format Format) {
	var d RunnerDriver
	var err error

	d, err = LookupDriver(c.Runner)
	switch {
	case err == nil && d.Format() != "":
		format = d.Format()
	case c.Format != "":
		format = c.Format
	default:
		format = FormatTAP
	}
	return format
}

// ResolvedCommand returns the literal shell command this check runs with no
// Endless present: the translated native filter for a structured check, or the
// raw Command otherwise. It is the bare-clone (exit-code-only) form, kept solely
// as the bridge that keeps RenderRunScript green; the driver execution path does
// not use it.
//
// E-1792-removable: RenderRunScript's pure-sh emission (and therefore this
// method) is retired by E-1792. It assumes the check has passed validation.
func (c Check) ResolvedCommand() (cmd string) {
	return bareCloneCommand(c)
}

// bareCloneCommand derives the exit-code-only command a bare clone runs for a
// check: the family's native filter for a structured selection, else the literal
// Command. It dispatches on the parsed family rather than a driver method so the
// E-1792-removable bare-clone bridge stays isolated from the execution interface.
func bareCloneCommand(c Check) (cmd string) {
	var family string

	if c.Command != "" {
		cmd = c.Command
		goto end
	}
	family, _, _ = parseRunner(c.Runner)
	switch family {
	case "gotest":
		cmd = goTestCmd(false, c.Tests, c.Paths)
	case "pytest":
		cmd = pytestCmd(c.Tests, c.Paths)
	default:
		cmd = c.Command
	}
end:
	return cmd
}

// validateCheck enforces the two-form rules for a single check against the driver
// its runner resolves to. Errors wrap ErrInvalidManifest. The index is attached
// as metadata so a failing check in a list is identifiable.
func validateCheck(c Check, index int) (err error) {
	var d RunnerDriver

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
