package verify_test

import (
	"reflect"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// A pytest-json-report document with a pass, a fail (carrying crash + longrepr),
// and a skip. Stage durations sum to the per-test Duration.
const pytestReport = `{
  "created": 1750672800.0,
  "duration": 1.5,
  "tests": [
    {
      "nodeid": "tests/test_x.py::test_a",
      "outcome": "passed",
      "setup":    {"duration": 0.001, "outcome": "passed"},
      "call":     {"duration": 0.009, "outcome": "passed"},
      "teardown": {"duration": 0.0,   "outcome": "passed"}
    },
    {
      "nodeid": "tests/test_x.py::test_b",
      "outcome": "failed",
      "setup":    {"duration": 0.0, "outcome": "passed"},
      "call":     {"duration": 0.1, "outcome": "failed",
                   "crash": {"message": "assert 1 == 2"},
                   "longrepr": "def test_b():\n>   assert 1 == 2\nE   assert 1 == 2"},
      "teardown": {"duration": 0.0, "outcome": "passed"}
    },
    {
      "nodeid": "tests/test_x.py::test_c",
      "outcome": "skipped",
      "setup":    {"duration": 0.0, "outcome": "skipped"},
      "call":     {"duration": 0.0, "outcome": "skipped"},
      "teardown": {"duration": 0.0, "outcome": "passed"}
    }
  ]
}`

func TestParsePytestJSON(t *testing.T) {
	got, err := verify.Normalize(verify.FormatPytestJSON, []byte(pytestReport))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	want := &verify.Report{Results: verify.Results{
		Tool: verify.Tool{Name: "pytest"},
		Summary: verify.Summary{
			Tests: 3, Passed: 1, Failed: 1, Skipped: 1,
			Start: 1750672800000,
			Stop:  1750672801500,
		},
		Tests: []verify.Test{
			{Name: "tests/test_x.py::test_a", Status: verify.StatusPassed, Duration: 10, Suite: "tests/test_x.py"},
			{Name: "tests/test_x.py::test_b", Status: verify.StatusFailed, Duration: 100, Suite: "tests/test_x.py",
				Message: "assert 1 == 2",
				Trace:   "def test_b():\n>   assert 1 == 2\nE   assert 1 == 2"},
			{Name: "tests/test_x.py::test_c", Status: verify.StatusSkipped, Duration: 0, Suite: "tests/test_x.py"},
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize(pytest-json) mismatch:\n got: %+v\nwant: %+v", got.Results, want.Results)
	}
}

// pytest outcome mapping: error -> failed, xfailed -> skipped, xpassed -> passed.
func TestParsePytestJSON_OutcomeMapping(t *testing.T) {
	const doc = `{"created":0,"duration":0,"tests":[
		{"nodeid":"a::e","outcome":"error","call":{"outcome":"error"}},
		{"nodeid":"a::xf","outcome":"xfailed","call":{"outcome":"xfailed"}},
		{"nodeid":"a::xp","outcome":"xpassed","call":{"outcome":"xpassed"}}
	]}`
	got, err := verify.Normalize(verify.FormatPytestJSON, []byte(doc))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []verify.Status{verify.StatusFailed, verify.StatusSkipped, verify.StatusPassed}
	for i, st := range want {
		if got.Results.Tests[i].Status != st {
			t.Errorf("test %d status = %q, want %q", i, got.Results.Tests[i].Status, st)
		}
	}
}
