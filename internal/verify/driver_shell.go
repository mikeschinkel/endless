package verify

import (
	"fmt"
	"strings"
	"time"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// shellDriver runs a shell check (Check.Sh). Unlike every other driver it parses
// no result stream: the check is one test whose status Endless decides from the
// command's exit code and the check's output matchers, so a fact no native test
// covers is one line of TOML rather than a hand-written TAP emitter.
type shellDriver struct{}

func (shellDriver) Family() string { return "" }

// Format returns "" because a shell check emits no result stream.
func (shellDriver) Format() Format { return "" }

func (shellDriver) Selection() SelectionKind { return SelectionCommandOnly }

func (shellDriver) SupportsVariant(variant string) (ok bool) { return variant == "" }

// Run execs the check's command via `sh -c` under root+env and builds a
// one-test report: passed when it exits 0 and every matcher holds, failed
// otherwise. A failure's message states why — the exit code, or the matcher's
// expected value — and carries the captured stdout and stderr, which the test's
// stdout and stderr hold as lines too. The returned Exit is the command's own,
// and a failing exit is always explained by the failed test it produces.
func (shellDriver) Run(check Check, root dt.DirPath, env []string, _ dt.Filepath) (result RunResult, err error) {
	var stdout, stderr []byte
	var exit int
	var start, stop time.Time
	var reason string
	var t Test
	var rpt *Report

	start = time.Now()
	stdout, stderr, exit, err = execCapture([]string{"sh", "-c", check.Sh}, root, env)
	stop = time.Now()
	if err != nil {
		err = doterr.NewErr(ErrCheckStart, err, "command", check.Sh)
		goto end
	}

	t = Test{
		Name:     shellCheckName(check),
		Status:   StatusPassed,
		Duration: stop.Sub(start).Milliseconds(),
	}
	reason = shellFailure(check, exit, string(stdout))
	if reason != "" {
		t.Status = StatusFailed
		t.Message = shellMessage(reason, stdout, stderr)
		t.Stdout = outputLines(stdout)
		t.Stderr = outputLines(stderr)
	}

	rpt = &Report{}
	rpt.Results.Tool.Name = "sh"
	rpt.Results.Tests = []Test{t}
	rpt.Results.Summary = newSummary(rpt.Results.Tests)
	rpt.Results.Summary.Start = start.UnixMilli()
	rpt.Results.Summary.Stop = stop.UnixMilli()
	result = RunResult{Report: rpt, Exit: exit, Stderr: stderr}
end:
	return result, err
}

// shellCheckName is the test name a shell check reports: its name, or its
// command when it has none.
func shellCheckName(check Check) (name string) {
	name = check.Name
	if name == "" {
		name = strings.TrimSpace(check.Sh)
	}
	return name
}

// shellFailure returns why a shell check failed, or "" when it passed. A
// non-zero exit fails it before any matcher is read: output from a command
// that failed is not the output the matcher was written for.
func shellFailure(check Check, exit int, stdout string) (reason string) {
	switch {
	case exit != 0:
		reason = fmt.Sprintf("exited %d", exit)
	case check.Contains != "" && !strings.Contains(stdout, check.Contains):
		reason = fmt.Sprintf("stdout does not contain %q", check.Contains)
	case check.NotContains != "" && strings.Contains(stdout, check.NotContains):
		reason = fmt.Sprintf("stdout contains %q", check.NotContains)
	case check.Equals != nil && strings.TrimRight(stdout, "\n") != strings.TrimRight(*check.Equals, "\n"):
		reason = fmt.Sprintf("stdout does not equal %q", *check.Equals)
	}
	return reason
}

// shellMessage is a failed shell check's message: the reason, then the
// captured stdout and stderr, each under its own heading when non-empty.
func shellMessage(reason string, stdout, stderr []byte) (msg string) {
	var b strings.Builder

	b.WriteString(reason)
	for _, out := range []struct {
		label string
		data  []byte
	}{{"stdout", stdout}, {"stderr", stderr}} {
		if len(out.data) == 0 {
			continue
		}
		b.WriteString("\n--- " + out.label + " ---\n")
		b.WriteString(strings.TrimRight(string(out.data), "\n"))
	}
	msg = b.String()
	return msg
}
