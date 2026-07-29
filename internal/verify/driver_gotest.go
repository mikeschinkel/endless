package verify

import (
	"strings"

	"github.com/mikeschinkel/go-dt"
)

// gotestDriver is the registered driver for the "gotest" family: a structured
// selection (Tests names + optional Paths scope) translates to `go test -run
// '^(…)$'`, the stream is the test2json (`-json`) output on stdout, and the
// inferred format is gotest-json. It ships only its bare form (no variant).
type gotestDriver struct{}

func (gotestDriver) Family() string { return "gotest" }

func (gotestDriver) Format() Format { return FormatGotestJSON }

func (gotestDriver) Selection() SelectionKind { return SelectionStructured }

// SupportsVariant accepts only the bare gotest form: Go's tool needs no
// launcher/environment variant, so a "gotest/x" runner is rejected.
func (gotestDriver) SupportsVariant(variant string) (ok bool) {
	return variant == ""
}

// Run executes the check: a structured selection becomes an instrumented `go
// test -json …` invocation whose stdout is the stream; a command-mode check runs
// its literal command (also expected to emit test2json on stdout). Either way the
// stream normalizes with gotest-json.
func (gotestDriver) Run(check Check, root dt.DirPath, env []string, reportPath dt.Filepath) (result RunResult, err error) {
	var argv []string

	argv = []string{"sh", "-c", goTestCmd(true, check.Tests, check.Paths)}
	if check.Command != "" {
		argv = []string{"sh", "-c", check.Command}
	}
	result, err = runAndNormalize(argv, StreamStdout, FormatGotestJSON, root, env, reportPath)
	return result, err
}

// goTestCmd builds a `go test` invocation from a structured selection, with -json
// added when jsonStream is set (the driver's capture form) and omitted for the
// bare-clone form. Test names are anchored exactly (^(A|B)$) so a name never
// matches a longer one (a bare TestFoo would otherwise also select TestFooBar).
// Paths scope the packages searched and default to the whole module (./...).
func goTestCmd(jsonStream bool, tests, paths []string) (cmd string) {
	var b strings.Builder

	b.WriteString("go test")
	if jsonStream {
		b.WriteString(" -json")
	}
	if len(tests) > 0 {
		b.WriteString(" -run '^(")
		b.WriteString(strings.Join(tests, "|"))
		b.WriteString(")$'")
	}
	b.WriteByte(' ')
	scope := "./..."
	if len(paths) > 0 {
		scope = strings.Join(paths, " ")
	}
	b.WriteString(scope)
	cmd = b.String()
	return cmd
}
