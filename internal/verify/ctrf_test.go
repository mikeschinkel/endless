package verify_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// Write emits one indented JSON document with the pinned CTRF field names and a
// trailing newline.
func TestReport_Write(t *testing.T) {
	r := &verify.Report{Results: verify.Results{
		Tool:    verify.Tool{Name: "go test"},
		Summary: verify.Summary{Tests: 1, Passed: 1, Start: 10, Stop: 20},
		Tests: []verify.Test{
			{Name: "TestA", Status: verify.StatusPassed, Duration: 5, Suite: "pkg"},
		},
	}}

	var buf bytes.Buffer
	if err := r.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()

	if !strings.HasSuffix(out, "\n") {
		t.Error("Write output is missing the trailing newline")
	}
	for _, want := range []string{
		`"results"`, `"tool"`, `"name": "go test"`, `"summary"`,
		`"tests": 1`, `"passed": 1`, `"tests": [`, `"name": "TestA"`,
		`"status": "passed"`, `"duration": 5`, `"suite": "pkg"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Write output missing %s\n---\n%s", want, out)
		}
	}
	// Optional empty fields must be omitted, not emitted as empty strings.
	for _, absent := range []string{`"message"`, `"trace"`, `"stdout"`, `"attachments"`, `"extra"`} {
		if strings.Contains(out, absent) {
			t.Errorf("Write output should omit empty %s\n---\n%s", absent, out)
		}
	}
}

// MergeReports concatenates tests, sums the summary counts, and spans the
// earliest start to the latest stop, naming the writer "endless".
func TestMergeReports(t *testing.T) {
	a := &verify.Report{Results: verify.Results{
		Tool:    verify.Tool{Name: "go test"},
		Summary: verify.Summary{Tests: 2, Passed: 1, Failed: 1, Start: 100, Stop: 300},
		Tests: []verify.Test{
			{Name: "TestA", Status: verify.StatusPassed},
			{Name: "TestB", Status: verify.StatusFailed},
		},
	}}
	b := &verify.Report{Results: verify.Results{
		Tool:    verify.Tool{Name: "pytest"},
		Summary: verify.Summary{Tests: 1, Skipped: 1, Start: 50, Stop: 250},
		Tests: []verify.Test{
			{Name: "test_c", Status: verify.StatusSkipped},
		},
	}}

	got := verify.MergeReports(a, nil, b)

	if got.Results.Tool.Name != "endless" {
		t.Errorf("merged tool name = %q, want endless", got.Results.Tool.Name)
	}
	wantSummary := verify.Summary{Tests: 3, Passed: 1, Failed: 1, Skipped: 1, Start: 50, Stop: 300}
	if got.Results.Summary != wantSummary {
		t.Errorf("merged summary = %+v, want %+v", got.Results.Summary, wantSummary)
	}
	if len(got.Results.Tests) != 3 {
		t.Errorf("merged tests len = %d, want 3", len(got.Results.Tests))
	}
}

// Normalize rejects an unknown format with an error wrapping ErrUnknownFormat.
func TestNormalize_UnknownFormat(t *testing.T) {
	_, err := verify.Normalize(verify.Format("junit-xml"), []byte("<x/>"))
	if !errors.Is(err, verify.ErrUnknownFormat) {
		t.Errorf("Normalize(unknown) error = %v, want wraps ErrUnknownFormat", err)
	}
}
