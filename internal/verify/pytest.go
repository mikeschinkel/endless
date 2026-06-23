package verify

import (
	"encoding/json"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// pytestReport is the top-level object of the pytest-json-report plugin's
// .report.json. pytest core emits no JSON natively; that plugin is the de-facto
// standard producer and the one Endless pins to. Only the fields the normalizer
// needs are decoded.
type pytestReport struct {
	Created  float64      `json:"created"`  // epoch seconds when the run started
	Duration float64      `json:"duration"` // wall-clock seconds of the run
	Tests    []pytestTest `json:"tests"`
}

// pytestTest is one collected test. Each lifecycle stage (setup/call/teardown)
// carries its own duration and outcome; the test's overall Outcome is the
// plugin's roll-up.
type pytestTest struct {
	NodeID   string      `json:"nodeid"`
	Outcome  string      `json:"outcome"`
	Setup    pytestStage `json:"setup"`
	Call     pytestStage `json:"call"`
	Teardown pytestStage `json:"teardown"`
}

// pytestStage is one phase of a test's lifecycle. Longrepr holds the failure
// representation; Crash, when present, holds a one-line failure message.
type pytestStage struct {
	Duration float64      `json:"duration"`
	Outcome  string       `json:"outcome"`
	Longrepr string       `json:"longrepr"`
	Crash    *pytestCrash `json:"crash"`
}

// pytestCrash is the concise failure message the plugin extracts for a failing
// stage.
type pytestCrash struct {
	Message string `json:"message"`
}

// parsePytestJSON normalizes a pytest-json-report document. Per test: Name is
// the nodeid, Suite is its file portion, Duration is the summed stage durations
// in milliseconds, and Status maps the pytest outcome. A failing test takes its
// Message from call.crash.message and Trace from call.longrepr. Summary Start is
// the run's created time; Stop is created + duration.
func parsePytestJSON(raw []byte) (rpt *Report, err error) {
	var (
		pr    pytestReport
		tests []Test
		t     Test
	)

	err = json.Unmarshal(raw, &pr)
	if err != nil {
		err = doterr.NewErr(ErrParsingPytestJSON, err)
		goto end
	}

	tests = make([]Test, 0, len(pr.Tests))
	for _, pt := range pr.Tests {
		t = Test{
			Name:     pt.NodeID,
			Suite:    pytestSuite(pt.NodeID),
			Status:   pytestStatus(pt.Outcome),
			Duration: secondsToMillis(pt.Setup.Duration + pt.Call.Duration + pt.Teardown.Duration),
		}
		if t.Status == StatusFailed {
			if pt.Call.Crash != nil {
				t.Message = pt.Call.Crash.Message
			}
			t.Trace = pt.Call.Longrepr
		}
		tests = append(tests, t)
	}

	rpt = &Report{}
	rpt.Results.Tool.Name = "pytest"
	rpt.Results.Tests = tests
	rpt.Results.Summary = newSummary(tests)
	if pr.Created != 0 {
		rpt.Results.Summary.Start = secondsToMillis(pr.Created)
		rpt.Results.Summary.Stop = secondsToMillis(pr.Created + pr.Duration)
	}

end:
	return rpt, err
}

// pytestStatus maps a pytest outcome to a CTRF status. error is a failure;
// xfailed (expected failure) is a skip; xpassed (unexpected pass) is a pass; an
// unrecognized outcome is other.
func pytestStatus(outcome string) (status Status) {
	switch outcome {
	case "passed", "xpassed":
		status = StatusPassed
	case "failed", "error":
		status = StatusFailed
	case "skipped", "xfailed":
		status = StatusSkipped
	default:
		status = StatusOther
	}
	return status
}

// pytestSuite returns the file portion of a pytest nodeid (everything before the
// first "::"), or the whole nodeid when it carries no separator.
func pytestSuite(nodeID string) (suite string) {
	var sep int

	suite = nodeID
	sep = strings.Index(nodeID, "::")
	if sep >= 0 {
		suite = nodeID[:sep]
	}
	return suite
}
