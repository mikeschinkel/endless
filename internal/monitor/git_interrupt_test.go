package monitor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

// E-2113 — an interrupted probe is not a failed probe.
//
// Quitting `session monitor` with Ctrl-C sends SIGINT to the pane's whole
// foreground process group. monitor.runGit uses a plain exec.Command, so every
// in-flight git probe is in that group and dies with it. E-1940's fail-closed
// rule then read "git could not run" and recorded ERR-0010 against a worktree
// that was fine — observed for E-1972 thirteen seconds after a land, one
// occurrence, while the probe answered cleanly on demand.
//
// Signals are not fabricable: os.ProcessState cannot be constructed, and a
// hand-rolled fake would test the fake. Every case below signals a REAL child.

// signalledErr runs a child that kills itself with sig and returns the error
// exec reports for it.
func signalledErr(t *testing.T, sig string) error {
	t.Helper()
	// The trailing sleep keeps the shell alive long enough for the signal it
	// sent itself to be delivered, rather than racing a normal exit.
	err := exec.Command("sh", "-c", "kill -"+sig+" $$; sleep 5").Run()
	if err == nil {
		t.Fatalf("child was expected to die on SIG%s", sig)
	}
	return err
}

// writeGitShim puts a fake `git` at the front of PATH so runGit — the real one,
// wrap and all — runs a child whose fate the test dictates. What is under test
// is runGit's classification of a signalled child, not git's own behaviour.
func writeGitShim(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestKilledBySIGINT pins the classifier to SIGINT alone. The narrowness is the
// decision: SIGTERM/SIGHUP/SIGKILL keep recording, because a probe big enough to
// be OOM-killed is a real operational fact.
func TestKilledBySIGINT(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"SIGINT", signalledErr(t, "INT"), true},
		{"SIGTERM", signalledErr(t, "TERM"), false},
		{"SIGKILL", signalledErr(t, "KILL"), false},
		{"SIGHUP", signalledErr(t, "HUP"), false},
		{"ordinary non-zero exit", exec.Command("sh", "-c", "exit 3").Run(), false},
		{"nil", nil, false},
		{"not an ExitError", errors.New("exec: \"git\": executable file not found in $PATH"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := killedBySIGINT(tc.err); got != tc.want {
				t.Errorf("killedBySIGINT(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRunGitClassifiesAtTheSource is why no fault recorder has to re-derive
// this: the meaning is attached where the subprocess ran.
func TestRunGitClassifiesAtTheSource(t *testing.T) {
	writeGitShim(t, "kill -INT $$; sleep 5")

	_, err := runGit(t.TempDir(), "status", "--porcelain")
	if !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("runGit err = %v, want it to satisfy errors.Is(err, ErrGitInterrupted)", err)
	}
}

// TestRunGitLeavesOrdinaryFailuresAlone is the contrast: the classification must
// not swallow the failures ERR-0010 exists to report.
func TestRunGitLeavesOrdinaryFailuresAlone(t *testing.T) {
	writeGitShim(t, "echo 'fatal: not a git repository' >&2; exit 128")

	_, err := runGit(t.TempDir(), "status", "--porcelain")
	if err == nil {
		t.Fatal("a failing git must still return an error")
	}
	if errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("exit 128 was classified as interrupted: %v", err)
	}
}

// TestInterruptClassificationSurvivesGitProbeError guards the wrapper that used
// to drop it: gitProbeError renders its cause to a string, so without Unwrap the
// guard downstream could never see through the probe label.
func TestInterruptClassificationSurvivesGitProbeError(t *testing.T) {
	interrupted := runGitInterrupted(t)

	wrapped := error(gitProbeError{
		Command: "git range-diff",
		Detail:  "signal: interrupt",
		Err:     interrupted,
	})
	if !errors.Is(wrapped, ErrGitInterrupted) {
		t.Error("errors.Is could not reach the classification through gitProbeError")
	}
	// probeCommand still reads the label it is the dedup key for.
	if got := probeCommand(wrapped); got != "git range-diff" {
		t.Errorf("probeCommand = %q, want %q", got, "git range-diff")
	}

	ordinary := error(gitProbeError{
		Command: "git range-diff",
		Detail:  "fatal: need two commit ranges",
		Err:     errors.New("exit status 128"),
	})
	if errors.Is(ordinary, ErrGitInterrupted) {
		t.Error("an ordinary git failure must not read as interrupted")
	}

	// The synthetic errors this file raises on its own carry no cause at all.
	synthetic := error(gitProbeError{Command: "git merge-base", Detail: "no common ancestor"})
	if errors.Is(synthetic, ErrGitInterrupted) {
		t.Error("a synthetic probe error must not read as interrupted")
	}
	if synthetic.Error() != "git merge-base: no common ancestor" {
		t.Errorf("Error() = %q, want the unchanged rendering", synthetic.Error())
	}
}

// runGitInterrupted returns a real, already-classified interrupted error by
// driving the production runGit against a shim that interrupts itself — the
// exact value the probes see when Ctrl-C reaches their git child.
func runGitInterrupted(t *testing.T) error {
	t.Helper()
	writeGitShim(t, "kill -INT $$; sleep 5")
	_, err := runGit(t.TempDir(), "status", "--porcelain")
	if !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("shim did not produce an interrupted error: %v", err)
	}
	return err
}

// TestInterruptedProbeRecordsNoFault is the defect, stated: the reported
// incident was ERR-0010 for `git status --porcelain` on a healthy worktree.
func TestInterruptedProbeRecordsNoFault(t *testing.T) {
	bindFaultsForTest(t)
	interrupted := runGitInterrupted(t)
	unsettledStub{statusErr: interrupted}.install(t)

	d := WorktreeUnsettledAt("/wt/e-1972")

	// The verdict is deliberately unchanged. E-1940's invariant — a worktree
	// nobody could inspect must never render as verified clean — is intact.
	if !d.IsUndetermined() {
		t.Error("an interrupted probe established nothing; it must stay undetermined")
	}
	if !d.Unsettled() {
		t.Error("undetermined must still mark the row")
	}
	if !d.Interrupted {
		t.Error("Interrupted was not set")
	}
	assertNoIncidents(t)
}

// TestInterruptedUnlandedProbeRecordsNoFault covers the probe actually observed
// in the wild — the classification has to survive gitProbeError to get here.
func TestInterruptedUnlandedProbeRecordsNoFault(t *testing.T) {
	bindFaultsForTest(t)
	interrupted := runGitInterrupted(t)
	unsettledStub{revList: "3\n", rangeDiffErr: interrupted}.install(t)

	d := WorktreeUnsettledAt("/wt/e-1972")

	if !d.Unsettled() || !d.Interrupted {
		t.Errorf("unsettled=%v interrupted=%v, want both true", d.Unsettled(), d.Interrupted)
	}
	assertNoIncidents(t)
}

// TestOrdinaryProbeFailureStillRecords is the half that must not regress: the
// guard is for signalled children only, and a genuinely broken worktree is still
// an incident.
func TestOrdinaryProbeFailureStillRecords(t *testing.T) {
	bindFaultsForTest(t)
	unsettledStub{statusErr: errors.New("fatal: not a git repository")}.install(t)

	WorktreeUnsettledAt("/wt/e-1972")

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want exactly 1", len(incidents))
	}
	if incidents[0].Code != faults.ErrCodeWorktreeProbeFailed.ID {
		t.Errorf("code = %s, want %s", incidents[0].Code, faults.ErrCodeWorktreeProbeFailed.ID)
	}
}

// TestUndeterminedReasonSaysInterrupted covers the surface whose whole job is
// explaining the ◆. It used to read "git status failed: signal: interrupt" —
// which is the false statement, not merely the incident.
func TestUndeterminedReasonSaysInterrupted(t *testing.T) {
	tests := []struct {
		name string
		d    UnsettledDetail
		want string
	}{
		{"status interrupted",
			UnsettledDetail{StatusErr: "signal: interrupt", Interrupted: true},
			"git status interrupted"},
		{"status failed",
			UnsettledDetail{StatusErr: "fatal: not a git repository"},
			"git status failed: fatal: not a git repository"},
		{"unlanded interrupted",
			UnsettledDetail{UnlandedErr: "git range-diff: signal: interrupt", Interrupted: true},
			"unlanded commit count interrupted"},
		{"unlanded failed",
			UnsettledDetail{UnlandedErr: "git range-diff: fatal: need two commit ranges"},
			"unlanded commits could not be counted: git range-diff: fatal: need two commit ranges"},
		{"base interrupted",
			UnsettledDetail{BaseErr: "cannot resolve", Interrupted: true},
			"default branch resolution interrupted"},
		{"base failed",
			UnsettledDetail{BaseErr: "cannot resolve"},
			"default branch unresolved: cannot resolve"},
		{"lookup is never a subprocess",
			UnsettledDetail{LookupErr: "no such project", Interrupted: true},
			"worktree lookup failed: no such project"},
		{"every probe ran", UnsettledDetail{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.UndeterminedReason(); got != tc.want {
				t.Errorf("UndeterminedReason() = %q, want %q", got, tc.want)
			}
		})
	}

	// Reason() carries the wording into the list view unchanged.
	r := UnsettledDetail{HasWorktree: true, StatusErr: "signal: interrupt", Interrupted: true}.Reason()
	if !strings.Contains(r, "undetermined (git status interrupted)") {
		t.Errorf("Reason() = %q, want it to carry the interrupted wording", r)
	}
}

// assertNoIncidents fails with what WAS recorded, so a regression names the code
// and summary rather than a bare count.
func assertNoIncidents(t *testing.T) {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	for _, in := range incidents {
		t.Errorf("interrupted probe recorded %s: %q", in.Code, in.Summary)
	}
}
