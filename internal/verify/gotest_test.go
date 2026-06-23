package verify_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/verify"
)

// millis parses an RFC3339 timestamp to epoch milliseconds for building the
// expected summary times, mirroring what the normalizer does internally.
func millis(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return tm.UnixMilli()
}

// A go test -json stream with one pass, one fail (carrying output), and one
// skip, plus the package-level events that frame the run's timing.
const gotestStream = `{"Time":"2026-06-23T10:00:00Z","Action":"run","Package":"pkg","Test":"TestA"}
{"Time":"2026-06-23T10:00:00.5Z","Action":"pass","Package":"pkg","Test":"TestA","Elapsed":0.5}
{"Time":"2026-06-23T10:00:01Z","Action":"run","Package":"pkg","Test":"TestB"}
{"Time":"2026-06-23T10:00:01Z","Action":"output","Package":"pkg","Test":"TestB","Output":"    foo_test.go:10: want 1 got 2\n"}
{"Time":"2026-06-23T10:00:01.2Z","Action":"fail","Package":"pkg","Test":"TestB","Elapsed":0.2}
{"Time":"2026-06-23T10:00:01.3Z","Action":"run","Package":"pkg","Test":"TestC"}
{"Time":"2026-06-23T10:00:01.3Z","Action":"skip","Package":"pkg","Test":"TestC","Elapsed":0}
{"Time":"2026-06-23T10:00:01.4Z","Action":"pass","Package":"pkg","Elapsed":1.4}
`

func TestParseGotestJSON(t *testing.T) {
	got, err := verify.Normalize(verify.FormatGotestJSON, []byte(gotestStream))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	want := &verify.Report{Results: verify.Results{
		Tool: verify.Tool{Name: "go test"},
		Summary: verify.Summary{
			Tests: 3, Passed: 1, Failed: 1, Skipped: 1,
			Start: millis(t, "2026-06-23T10:00:00Z"),
			Stop:  millis(t, "2026-06-23T10:00:01.4Z"),
		},
		Tests: []verify.Test{
			{Name: "TestA", Status: verify.StatusPassed, Duration: 500, Suite: "pkg"},
			{Name: "TestB", Status: verify.StatusFailed, Duration: 200, Suite: "pkg",
				Message: "foo_test.go:10: want 1 got 2",
				Trace:   "    foo_test.go:10: want 1 got 2"},
			{Name: "TestC", Status: verify.StatusSkipped, Duration: 0, Suite: "pkg"},
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize(gotest-json) mismatch:\n got: %+v\nwant: %+v", got.Results, want.Results)
	}
}

// A malformed line in the stream surfaces as a parse error wrapping
// ErrParsingGotestJSON, not a silent partial result.
func TestParseGotestJSON_Malformed(t *testing.T) {
	_, err := verify.Normalize(verify.FormatGotestJSON, []byte("{not json}\n"))
	if err == nil {
		t.Fatal("expected error for malformed stream, got nil")
	}
}
