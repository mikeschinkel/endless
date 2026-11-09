package faults_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

func transientFault() faults.Fault {
	return faults.Fault{
		Code:        faults.ErrCodeMonitorVisibilityUnreadable,
		Source:      "monitorvis",
		Fingerprint: "monitor-visibility:/tmp/tmux-501/endless-tasks",
		Summary:     "monitor-wake could not ask tmux which windows are visible",
		Detail:      "no server running on /tmp/tmux-501/endless-tasks",
	}
}

var transientRun = faults.TransientRun{
	Source:      "monitorvis",
	Fingerprint: "monitor-visibility:/tmp/tmux-501/endless-tasks",
	Threshold:   3,
}

func openFaults(t *testing.T) []faults.Incident {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return incidents
}

func runs(t *testing.T) []faults.TransientRunRecord {
	t.Helper()
	got, err := faults.TransientRuns(0)
	if err != nil {
		t.Fatalf("TransientRuns: %v", err)
	}
	return got
}

// Below the threshold an occurrence is logged and counted, and raises nothing.
func TestRecordTransient_BelowThresholdLogsWithoutAFault(t *testing.T) {
	_, logDir := newBoundStore(t)

	for i := 0; i < transientRun.Threshold-1; i++ {
		if faults.RecordTransient(transientRun, transientFault()) {
			t.Fatalf("occurrence %d raised a fault below the threshold", i+1)
		}
	}

	if got := openFaults(t); len(got) != 0 {
		t.Fatalf("open incidents = %+v, want none", got)
	}
	got := runs(t)
	if len(got) != 1 || got[0].Occurrences != 2 || got[0].ClosedAt != "" || got[0].FaultedAt != "" {
		t.Fatalf("runs = %+v, want one open run of 2", got)
	}

	data, err := os.ReadFile(filepath.Join(logDir, "errors.jsonl"))
	if err != nil {
		t.Fatalf("read detail log: %v", err)
	}
	if n := strings.Count(string(data), `"kind":"transient"`); n != 2 {
		t.Errorf("detail log has %d transient lines, want 2:\n%s", n, data)
	}
	if n := faults.LogOnlyCount(); n != 0 {
		t.Errorf("LogOnlyCount = %d: a transient line must not read as a log-only fault", n)
	}
}

// At the threshold the failure becomes a fault, and the run says so.
func TestRecordTransient_ThresholdRaisesAndMarksTheRun(t *testing.T) {
	newBoundStore(t)

	var raised bool
	for i := 0; i < transientRun.Threshold; i++ {
		raised = faults.RecordTransient(transientRun, transientFault())
	}
	if !raised {
		t.Fatal("the threshold occurrence raised no fault")
	}
	got := openFaults(t)
	if len(got) != 1 || got[0].Code != faults.ErrCodeMonitorVisibilityUnreadable.ID {
		t.Fatalf("open incidents = %+v, want one WARN-0033", got)
	}
	if r := runs(t); len(r) != 1 || r[0].FaultedAt == "" {
		t.Fatalf("runs = %+v, want the run marked faulted", r)
	}
}

// A success after transient failures closes the run without a fault, and the
// next failure starts a new run from one.
func TestResolveTransient_ClosesTheRunWithoutAFault(t *testing.T) {
	newBoundStore(t)

	faults.RecordTransient(transientRun, transientFault())
	faults.RecordTransient(transientRun, transientFault())

	if !faults.ResolveTransient(transientRun.Source, transientRun.Fingerprint) {
		t.Fatal("ResolveTransient closed nothing")
	}
	if faults.ResolveTransient(transientRun.Source, transientRun.Fingerprint) {
		t.Error("a second resolve closed a run that was already closed")
	}
	if got := openFaults(t); len(got) != 0 {
		t.Fatalf("open incidents = %+v, want none", got)
	}

	r := runs(t)
	if len(r) != 1 || r[0].ClosedAt == "" || r[0].FaultedAt != "" || r[0].Occurrences != 2 {
		t.Fatalf("runs = %+v, want one closed, unfaulted run of 2", r)
	}
	if _, ok := r[0].TimeToClear(); !ok {
		t.Errorf("TimeToClear not computable for %+v", r[0])
	}

	faults.RecordTransient(transientRun, transientFault())
	r = runs(t)
	if len(r) != 2 || r[0].Occurrences != 1 || r[0].ClosedAt != "" {
		t.Fatalf("runs = %+v, want a new open run of 1 on top", r)
	}
}

// With no store to count on, the occurrence is reported rather than lost.
func TestRecordTransient_UnboundFailsTowardReporting(t *testing.T) {
	faults.Bind(nil, nil, nil, nil)
	if !faults.RecordTransient(transientRun, transientFault()) {
		t.Error("an uncountable occurrence was swallowed instead of reported")
	}
}
