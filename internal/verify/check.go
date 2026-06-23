package verify

import (
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// Check is one runner invocation contributing to a task's verification proof. A
// Manifest holds a list of these (the [[check]] array): one ticket composes
// multiple runners into one report. A check takes one of two forms.
//
// First-class runner (Runner names a registered firstClassRunner, e.g. gotest
// or pytest): Endless infers the result Format from the runner and translates a
// structured selection (Tests names + optional Paths scope) into the native
// filter command. An explicit Command is also accepted as an escape hatch for
// selections the structured form can't express; an explicit Format is accepted
// only when it equals the inferred value.
//
// Raw command (any other Runner): Command is the literal invocation Endless runs
// verbatim, and Format (default tap) declares the native result stream it emits.
// Tests and Paths are not valid on a raw check.
type Check struct {
	Runner  string   `toml:"runner"`
	Tests   []string `toml:"tests"`
	Paths   []string `toml:"paths"`
	Command string   `toml:"command"`
	Format  Format   `toml:"format"`
}

// firstClassRunner describes a runner Endless can translate to and infer a
// result format for. Adding a runner later is one entry in firstClassRunners:
// its native filter translation and its inferred format. translate receives the
// check's Tests (runner-native identifiers) and Paths (where to look) and
// returns the literal command a bare clone runs.
type firstClassRunner struct {
	name      string
	format    Format
	translate func(tests, paths []string) string
}

// firstClassRunners is the canonical registry of first-class runners. It is a
// slice (not a map) so iteration order is deterministic for error metadata.
var firstClassRunners = []firstClassRunner{
	{name: "gotest", format: FormatGotestJSON, translate: translateGotest},
	{name: "pytest", format: FormatPytestJSON, translate: translatePytest},
}

// lookupFirstClass returns the registry entry for a runner name, and whether the
// name is a first-class runner at all.
func lookupFirstClass(name string) (fc firstClassRunner, ok bool) {
	for _, fc = range firstClassRunners {
		if fc.name == name {
			ok = true
			goto end
		}
	}
	fc = firstClassRunner{}
end:
	return fc, ok
}

// firstClassNames renders the registered first-class runner names as a
// comma-separated string for error metadata.
func firstClassNames() (list string) {
	var parts []string

	parts = make([]string, len(firstClassRunners))
	for i, fc := range firstClassRunners {
		parts[i] = fc.name
	}
	list = strings.Join(parts, ", ")
	return list
}

// translateGotest builds a `go test` invocation from a structured selection.
// Test names are anchored exactly (^(A|B)$) so a name never matches a longer
// one (a bare TestFoo would otherwise also select TestFooBar). Paths scope the
// packages searched and default to the whole module (./...).
func translateGotest(tests, paths []string) (cmd string) {
	var b strings.Builder

	b.WriteString("go test")
	if len(tests) > 0 {
		b.WriteString(" -run '^(")
		b.WriteString(strings.Join(tests, "|"))
		b.WriteString(")$'")
	}
	b.WriteByte(' ')
	if len(paths) > 0 {
		b.WriteString(strings.Join(paths, " "))
	} else {
		b.WriteString("./...")
	}
	cmd = b.String()
	return cmd
}

// translatePytest builds a `pytest` invocation. Pytest accepts both file/dir
// paths and path::test nodeids as positional arguments, so Paths and Tests both
// become positional selectors (paths first). With neither, it collects the
// whole suite.
func translatePytest(tests, paths []string) (cmd string) {
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

// IsFirstClass reports whether this check's runner is a registered first-class
// runner (Endless translates its selection and infers its format).
func (c Check) IsFirstClass() (ok bool) {
	_, ok = lookupFirstClass(c.Runner)
	return ok
}

// ResolvedFormat returns the native result-stream format this check emits: the
// runner's inferred format for a first-class runner, or the declared Format
// (defaulting to tap) for a raw command. It assumes the check has passed
// validation.
func (c Check) ResolvedFormat() (format Format) {
	fc, ok := lookupFirstClass(c.Runner)
	switch {
	case ok:
		format = fc.format
	case c.Format != "":
		format = c.Format
	default:
		format = FormatTAP
	}
	return format
}

// ResolvedCommand returns the literal shell command this check runs with no
// Endless present: the translated native filter for a first-class structured
// check, or the raw Command otherwise (a first-class command-mode check or a raw
// check). It assumes the check has passed validation.
func (c Check) ResolvedCommand() (cmd string) {
	fc, ok := lookupFirstClass(c.Runner)
	switch {
	case ok && c.Command == "":
		cmd = fc.translate(c.Tests, c.Paths)
	default:
		cmd = c.Command
	}
	return cmd
}

// validateCheck enforces the two-form rules for a single check. Errors wrap
// ErrInvalidManifest. The index is attached as metadata so a failing check in a
// list is identifiable.
func validateCheck(c Check, index int) (err error) {
	var fc firstClassRunner
	var ok bool

	if c.Runner == "" {
		err = doterr.NewErr(ErrInvalidManifest, ErrCheckMissingRunner, "index", index)
		goto end
	}

	fc, ok = lookupFirstClass(c.Runner)
	if !ok {
		err = validateRawCheck(c, index)
		goto end
	}
	err = validateFirstClassCheck(c, fc, index)

end:
	return err
}

// validateFirstClassCheck validates a check whose runner is first-class: command
// XOR a structured (tests/paths) selection, and an explicit format only when it
// matches the inferred one.
func validateFirstClassCheck(c Check, fc firstClassRunner, index int) (err error) {
	hasSelection := len(c.Tests) > 0 || len(c.Paths) > 0

	switch {
	case c.Command != "" && hasSelection:
		err = doterr.NewErr(ErrInvalidManifest, ErrFirstClassCommandConflict,
			"index", index, "runner", c.Runner)
	case c.Command == "" && !hasSelection:
		err = doterr.NewErr(ErrInvalidManifest, ErrFirstClassNeedsSelection,
			"index", index, "runner", c.Runner)
	case c.Format != "" && c.Format != fc.format:
		err = doterr.NewErr(ErrInvalidManifest, ErrFormatMismatch,
			"index", index, "runner", c.Runner,
			"format", string(c.Format), "inferred", string(fc.format))
	}
	return err
}

// validateRawCheck validates a non-first-class check: a literal command is
// required, tests/paths are not allowed, and a declared format (if any) must be
// known.
func validateRawCheck(c Check, index int) (err error) {
	switch {
	case len(c.Tests) > 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrTestsRequireFirstClass,
			"index", index, "runner", c.Runner, "first_class", firstClassNames())
	case len(c.Paths) > 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrPathsRequireFirstClass,
			"index", index, "runner", c.Runner, "first_class", firstClassNames())
	case c.Command == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrRawCheckNeedsCommand,
			"index", index, "runner", c.Runner)
	case c.Format != "" && !c.Format.Valid():
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownFormat,
			"index", index, "format", string(c.Format), "supported", formatList())
	}
	return err
}
