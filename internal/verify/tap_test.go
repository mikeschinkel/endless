package verify_test

import (
	"reflect"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// A TAP14 stream: a plan line, a pass, a fail with a YAML diagnostic block, a
// SKIP, and a TODO directive.
const tapStream = `TAP version 14
1..4
ok 1 - adds two numbers
not ok 2 - subtracts
  ---
  message: 'want 1 got -1'
  severity: fail
  ...
ok 3 - windows only # SKIP not on this platform
not ok 4 - parses nested config # TODO not implemented yet
`

func TestParseTAP(t *testing.T) {
	got, err := verify.Normalize(verify.FormatTAP, []byte(tapStream))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	want := &verify.Report{Results: verify.Results{
		Tool: verify.Tool{Name: "tap"},
		Summary: verify.Summary{
			Tests: 4, Passed: 1, Failed: 1, Skipped: 1, Pending: 1,
		},
		Tests: []verify.Test{
			{Name: "adds two numbers", Status: verify.StatusPassed},
			{Name: "subtracts", Status: verify.StatusFailed,
				Trace: "  message: 'want 1 got -1'\n  severity: fail"},
			{Name: "windows only", Status: verify.StatusSkipped},
			{Name: "parses nested config", Status: verify.StatusPending},
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize(tap) mismatch:\n got: %+v\nwant: %+v", got.Results, want.Results)
	}
}
