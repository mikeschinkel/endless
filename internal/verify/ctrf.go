package verify

import (
	"encoding/json"
	"io"
	"math"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// This file implements section C of the E-1596 interface contract: the stable
// CTRF-subset output envelope Endless writes from a native test stream. It is a
// vendored SUBSET of the CTRF schema (pinned to SpecVersion): the field names
// and shapes match CTRF, so a written report validates against that schema, but
// only the fields Endless needs today are implemented. Endless reads native producers
// itself (see normalize.go) and writes this document; there is no runtime
// dependency on any external CTRF reporter.
//
// Add a CTRF field here only when a concrete consumer needs it.

// Status is a CTRF test outcome. It is a string-backed wire value (matching the
// CTRF JSON), not a database enum, so it mirrors Format rather than the
// integer-backed DB enum pattern.
type Status string

const (
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
	StatusPending Status = "pending"
	StatusOther   Status = "other"
)

// ReportFormat is the constant document identifier CTRF requires at the root.
const ReportFormat = "CTRF"

// SpecVersion is the CTRF specification release this subset is pinned to.
// Changing it means re-reading that release's schema against every field here.
const SpecVersion = "0.2.0"

// Report is the top-level CTRF document Endless emits. ReportFormat and
// SpecVersion are required by the schema; Write stamps them, so a report built
// in memory need not set them.
type Report struct {
	ReportFormat string  `json:"reportFormat"`
	SpecVersion  string  `json:"specVersion"`
	Results      Results `json:"results"`
}

// Results holds the producing tool, the summary counts, and the per-test list.
type Results struct {
	Tool    Tool    `json:"tool"`
	Summary Summary `json:"summary"`
	Tests   []Test  `json:"tests"`
}

// Tool names the producer of the results. A single normalized stream names its
// native producer (e.g. "go test", "pytest"); a merged report names "endless",
// the writer of the combined document.
type Tool struct {
	Name string `json:"name"`
}

// Summary is the aggregate of the test list. Counts partition Tests by Status.
// Start and Stop are epoch milliseconds; 0 means the native stream carried no
// usable timing.
type Summary struct {
	Tests   int   `json:"tests"`
	Passed  int   `json:"passed"`
	Failed  int   `json:"failed"`
	Skipped int   `json:"skipped"`
	Pending int   `json:"pending"`
	Other   int   `json:"other"`
	Start   int64 `json:"start"`
	Stop    int64 `json:"stop"`
}

// Test is one normalized test result. Name, Status, and Duration (milliseconds)
// are always present; the rest are optional and omitted when empty. Suite is
// the hierarchy from the top level to the test's immediate parent, and Stdout
// and Stderr are output lines, as the schema types them. Labels carries
// key-value metadata; Endless writes LabelCheck there. Attachments and Extra
// are the non-text/passthrough hooks from the contract — they have no producer
// yet (Extra is carried verbatim as raw JSON so the writer stays deterministic
// without a typed schema).
type Test struct {
	Name        string            `json:"name"`
	Status      Status            `json:"status"`
	Duration    int64             `json:"duration"`
	Message     string            `json:"message,omitempty"`
	Trace       string            `json:"trace,omitempty"`
	Suite       []string          `json:"suite,omitempty"`
	Stdout      []string          `json:"stdout,omitempty"`
	Stderr      []string          `json:"stderr,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Extra       json.RawMessage   `json:"extra,omitempty"`
}

// LabelCheck is the Labels key naming the [[check]] a test came from, set when
// that check declares a name. A merged report concatenates many checks' tests,
// and this is what traces one back to the check that produced it.
const LabelCheck = "check"

// LabelTests sets LabelCheck to name on every test in the report. An empty name
// leaves the report unchanged.
func (r *Report) LabelTests(name string) {
	if name == "" {
		return
	}
	for i := range r.Results.Tests {
		if r.Results.Tests[i].Labels == nil {
			r.Results.Tests[i].Labels = map[string]string{}
		}
		r.Results.Tests[i].Labels[LabelCheck] = name
	}
}

// outputLines splits captured output into the line array CTRF's stdout and
// stderr hold, dropping the final newline so it does not become an empty line.
// Empty output yields nil, which the writer omits.
func outputLines(b []byte) (lines []string) {
	var s string

	s = strings.TrimRight(string(b), "\n")
	if s == "" {
		goto end
	}
	lines = strings.Split(s, "\n")
end:
	return lines
}

// Attachment points at a non-text outcome (screenshot, HTTP transcript) by path.
// It is part of the subset for forward shape only; no normalizer emits one yet.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Path        string `json:"path"`
}

// secondsToMillis converts a fractional-seconds duration (as the native streams
// report it) to integer milliseconds, rounding so summed sub-millisecond stages
// don't truncate away (e.g. 0.001+0.009s == 10ms, not 9).
func secondsToMillis(seconds float64) (ms int64) {
	return int64(math.Round(seconds * 1000))
}

// newSummary derives the summary counts from a test list so every normalizer
// computes them identically. Times are filled in by the caller (each native
// stream carries timing differently).
func newSummary(tests []Test) (s Summary) {
	s.Tests = len(tests)
	for _, t := range tests {
		switch t.Status {
		case StatusPassed:
			s.Passed++
		case StatusFailed:
			s.Failed++
		case StatusSkipped:
			s.Skipped++
		case StatusPending:
			s.Pending++
		default:
			s.Other++
		}
	}
	return s
}

// Write emits the report as a single indented JSON document (trailing newline)
// to w. It is the public emission entry point for a normalized or merged report.
func (r *Report) Write(w io.Writer) (err error) {
	var data []byte
	var doc Report

	doc = *r
	doc.ReportFormat = ReportFormat
	doc.SpecVersion = SpecVersion
	data, err = json.MarshalIndent(&doc, "", "  ")
	if err != nil {
		err = doterr.NewErr(ErrWritingReport, err)
		goto end
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	if err != nil {
		err = doterr.NewErr(ErrWritingReport, err)
	}

end:
	return err
}

// MergeReports combines per-check reports into one document, per the contract:
// a verification is a list of checks and their normalized results merge into a
// single CTRF report. Tests are concatenated in argument order; summary counts
// are recomputed from the combined list (so they sum); Start is the earliest
// non-zero start and Stop the latest stop across inputs. The merged tool name is
// "endless" — Endless is the writer of the combined document. Nil reports are
// skipped.
func MergeReports(reports ...*Report) (merged *Report) {
	var start, stop int64

	merged = &Report{}
	merged.Results.Tool.Name = "endless"
	for _, r := range reports {
		if r == nil {
			continue
		}
		merged.Results.Tests = append(merged.Results.Tests, r.Results.Tests...)
		s := r.Results.Summary
		if s.Start != 0 && (start == 0 || s.Start < start) {
			start = s.Start
		}
		if s.Stop > stop {
			stop = s.Stop
		}
	}
	merged.Results.Summary = newSummary(merged.Results.Tests)
	merged.Results.Summary.Start = start
	merged.Results.Summary.Stop = stop
	return merged
}
