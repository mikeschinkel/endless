package verify

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/mikeschinkel/go-doterr"
)

// goTestEvent is one record of the `go test -json` stream (the test2json
// schema). Only the fields the normalizer uses are decoded. Package-level
// events carry an empty Test; build events carry neither Test nor Package and
// are ignored.
type goTestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// parseGotestJSON normalizes a `go test -json` stream. The stream is a sequence
// of JSON objects (one per line); each test emits run/output*/{pass,fail,skip}.
// Output lines are accumulated per (package,test) and attached to a failing
// test as Message (first non-blank line) and Trace (full output). Duration is
// the action's Elapsed seconds converted to milliseconds. Tests appear in the
// order their terminal action fires (go test's completion order), so output is
// deterministic. Summary Start/Stop come from the min/max event timestamp.
func parseGotestJSON(raw []byte) (rpt *Report, err error) {
	var (
		dec     = json.NewDecoder(bytes.NewReader(raw))
		outputs = map[string]*strings.Builder{}
		tests   []Test
		start   int64
		stop    int64
		e       goTestEvent
		key     string
		ms      int64
		t       Test
		b       *strings.Builder
	)

	for dec.More() {
		e = goTestEvent{}
		err = dec.Decode(&e)
		if err != nil {
			err = doterr.NewErr(ErrParsingGotestJSON, err)
			rpt = nil
			goto end
		}

		if e.Time != "" {
			ms = rfc3339Millis(e.Time)
			if ms != 0 {
				if start == 0 || ms < start {
					start = ms
				}
				if ms > stop {
					stop = ms
				}
			}
		}

		// Package- and build-level events carry no Test name.
		if e.Test == "" {
			continue
		}

		key = e.Package + "\x00" + e.Test
		switch e.Action {
		case "output":
			b = outputs[key]
			if b == nil {
				b = &strings.Builder{}
				outputs[key] = b
			}
			b.WriteString(e.Output)
		case "pass", "fail", "skip":
			t = Test{
				Name:     e.Test,
				Suite:    e.Package,
				Duration: secondsToMillis(e.Elapsed),
				Status:   gotestStatus(e.Action),
			}
			if t.Status == StatusFailed {
				b = outputs[key]
				if b != nil {
					t.Message, t.Trace = splitOutput(b.String())
				}
			}
			tests = append(tests, t)
		}
	}

	rpt = &Report{}
	rpt.Results.Tool.Name = "go test"
	rpt.Results.Tests = tests
	rpt.Results.Summary = newSummary(tests)
	rpt.Results.Summary.Start = start
	rpt.Results.Summary.Stop = stop

end:
	return rpt, err
}

// gotestStatus maps a go test terminal action to a CTRF status.
func gotestStatus(action string) (status Status) {
	switch action {
	case "pass":
		status = StatusPassed
	case "skip":
		status = StatusSkipped
	case "fail":
		status = StatusFailed
	}
	return status
}

// splitOutput derives a concise Message and the full Trace from accumulated test
// output. Message is the first non-blank line; Trace is the whole output with
// trailing whitespace trimmed.
func splitOutput(out string) (message, trace string) {
	var line string

	trace = strings.TrimRight(out, "\n \t")
	for _, line = range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			message = line
			goto end
		}
	}

end:
	return message, trace
}

// rfc3339Millis parses an RFC3339 timestamp to epoch milliseconds, returning 0
// when the value is absent or unparseable (timing is best-effort).
func rfc3339Millis(s string) (ms int64) {
	var tm time.Time
	var err error

	tm, err = time.Parse(time.RFC3339Nano, s)
	if err != nil {
		goto end
	}
	ms = tm.UnixMilli()

end:
	return ms
}
