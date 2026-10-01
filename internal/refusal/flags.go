package refusal

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
)

// Flags is a flag.FlagSet that cannot write to stderr behind your back.
//
// A plain flag.FlagSet defaults its output to os.Stderr from INSIDE the flag
// package, so "flag provided but not defined: -x" and the usage block that
// follows it reach the user without this repository's source ever naming
// os.Stderr. TestNoStderrOutsideThisPackage cannot see that write, and neither
// can a reviewer reading the call site — which is exactly the unclassified
// refusal this whole task exists to end, hiding in a library default.
//
// flag.ExitOnError makes it worse: the flag package prints and then calls
// os.Exit(2) itself, so the site has no opportunity to classify anything even
// if it wanted to. NewFlags is always ContinueOnError for that reason. The
// caller gets the error, the text flag would have printed, and the obligation
// to name a class.
type Flags struct {
	*flag.FlagSet
	out bytes.Buffer
}

// NewFlags returns a Flags named name, with flag's own output captured.
//
// name is the one flag prints in "Usage of <name>:", so it should read as the
// verb the user typed — "worktree in-use", not "in-use" — wherever the two
// differ.
func NewFlags(name string) *Flags {
	f := &Flags{FlagSet: flag.NewFlagSet(name, flag.ContinueOnError)}
	f.SetOutput(&f.out)
	return f
}

// Output is everything flag would have written to stderr: its error line, and
// the usage block it appends. Hand it to Text so a person reads exactly what
// they read before.
//
// It is also the right thing to hand to Detail when the site's own summary is
// better than flag's error line, which is usually the case for a required flag
// the site checks itself.
// It deliberately SHADOWS flag.FlagSet.Output, which returns the io.Writer.
// A site converting to this type wants the captured text, not the sink; the
// sink is Writer, and needing it at all is rare.
func (f *Flags) Output() string {
	return f.out.String()
}

// Writer is the capture buffer itself, for the one thing that genuinely needs
// a sink: a site installing its own fs.Usage function, whose output must land
// in the same capture as flag's so the two arrive together.
func (f *Flags) Writer() io.Writer {
	return &f.out
}

// Defaults renders the flag set's own usage block without printing it, for a
// site that wants it on a refusal it raised for its own reasons.
//
// NOT named Usage. flag.FlagSet exposes Usage as a FIELD — the function it
// calls when parsing fails — and a method of that name on the embedding struct
// shadows it, so `fs.Usage = func() {...}` stops compiling at every site that
// customises the block. Two conversions hit that within an hour of this type
// existing, which is a good enough measure of how surprising it is.
func (f *Flags) Defaults() string {
	before := f.out.Len()
	f.PrintDefaults()
	return f.out.String()[before:]
}

// ExitOnHelp reproduces, for -h and --help, exactly what flag.ExitOnError used
// to do from inside Parse: print the usage block flag has already rendered, and
// exit 0.
//
// It matters because NewFlags is always ContinueOnError. Without it, a verb
// converted from an ExitOnError flag set answers `-h` by falling into the site's
// parse-error branch — which prints the same text and exits 2. Asking for help
// and being told you failed is not the same command.
//
// Not a refusal: `-h` is a request that succeeded. It goes out as Info, on
// stderr, which is where flag itself put it.
func (f *Flags) ExitOnHelp(err error) {
	if !errors.Is(err, flag.ErrHelp) {
		return
	}
	Info(strings.TrimRight(f.Output(), "\n")).Print()
	os.Exit(0)
}
