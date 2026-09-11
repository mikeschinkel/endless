package monitor

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// E-2130 — an interrupted default-branch resolve is not an unresolvable repo.
//
// E-2113 classified a signalled git child at the source and guarded the three
// fault recorders with it. Two of those guards — recordDefaultBranchFault and
// recordReapDefaultBranchFault — were correct and unreachable: every step of
// resolveDefaultBranch swallowed its runGit error and returned "", so the
// classification died inside the resolver and each caller saw only
// ErrDefaultBranchUnresolved. A Ctrl-C landing during resolution therefore
// recorded ERR-0011 against a healthy worktree, and defaultBranchCache memoized
// that verdict so the rest of the process read it too.
//
// Signals are not fabricable (see git_interrupt_test.go), so every test here
// signals a REAL child through the production runGit.

// gitShimInterrupting installs a `git` that exits 0 with empty output for any
// invocation matching ok, and kills itself with SIGINT for every other. ok is
// matched against the whole argument line, so "status" lets probe 1 through
// while the resolver's symbolic-ref/config/rev-parse calls all die.
//
// It returns the PATH as it was BEFORE the shim, so a test can put the real git
// back without naming a directory this machine happens to keep it in.
func gitShimInterrupting(t *testing.T, ok string) (realPath string) {
	t.Helper()
	realPath = os.Getenv("PATH")
	body := `kill -INT $$; sleep 5`
	if ok != "" {
		body = `case "$*" in *` + ok + `*) exit 0 ;; esac
` + body
	}
	writeGitShim(t, body)
	return realPath
}

// TestDefaultBranchPropagatesInterrupt is the defect at its source. Before the
// fix this returned ErrDefaultBranchUnresolved — a positive claim that nothing
// in this repository names a default branch, made out of four probes that never
// got to run.
func TestDefaultBranchPropagatesInterrupt(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)
	gitShimInterrupting(t, "")

	branch, err := DefaultBranch(context.Background(), t.TempDir())
	if !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("DefaultBranch err = %v, want it to satisfy errors.Is(err, ErrGitInterrupted)", err)
	}
	if errors.Is(err, ErrDefaultBranchUnresolved) {
		t.Error("an interrupted resolve must not also claim the branch is unresolvable")
	}
	if branch != "" {
		t.Errorf("DefaultBranch returned %q alongside an error", branch)
	}
}

// TestInterruptedResolveIsNotMemoized is the half that outlives the signal. The
// background jobs run inside `session monitor`, a process that keeps going, so a
// cached interrupt is not one lost tick — it is every later probe of that
// directory answering from a verdict no probe ever reached. (It was the
// PreToolUse/PostToolUse reaper that made the point until E-2128 took the reaper
// off the hook path; the hazard is the same, in a longer-lived process.)
func TestInterruptedResolveIsNotMemoized(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := fixtureRepo(t, "master") // built with the real git, and always fine

	realPath := gitShimInterrupting(t, "")
	if _, err := DefaultBranch(context.Background(), dir); !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("setup: DefaultBranch err = %v, want an interrupt", err)
	}

	t.Setenv("PATH", realPath) // the signal is over; the real git is back
	got, err := DefaultBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultBranch after the interrupt: %v", err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch = %q, want master — the interrupt was cached", got)
	}
}

// TestInterruptedConfiguredBranchIsNotCalledATypo covers step 1, whose failure
// is NOT a fall-through but an accusation: it names the operator's own
// .endless/config.json as wrong. An interrupted existence check must not make
// it, because it never learned whether the branch is there.
func TestInterruptedConfiguredBranchIsNotCalledATypo(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	dir := t.TempDir()
	writeProjectConfig(t, dir, "release")
	gitShimInterrupting(t, "")

	_, err := DefaultBranch(context.Background(), dir)
	if !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("DefaultBranch err = %v, want an interrupt", err)
	}
	if errors.Is(err, ErrDefaultBranchUnresolved) {
		t.Errorf("an interrupted check accused the project config: %v", err)
	}
}

// TestInterruptedDefaultBranchRecordsNoFault is the incident that reached the
// fault list: ERR-0011 "the repository's default branch could not be resolved",
// naming a worktree whose only problem was that someone quit `session monitor`.
//
// Driven through WorktreeUnsettledDetailAt since E-2128: the display path no
// longer resolves the default branch — it reads the base NAME from the cache's
// watermark — so the resolver is now reached from the paths that compute.
func TestInterruptedDefaultBranchRecordsNoFault(t *testing.T) {
	bindFaultsForTest(t)
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)
	// Probe 1 answers cleanly, so the run reaches the resolver — exactly the
	// interleaving a Ctrl-C mid-tick produces.
	gitShimInterrupting(t, "status")

	d := WorktreeUnsettledDetailAt(context.Background(), t.TempDir())

	// The verdict is deliberately unchanged: E-1940's invariant is that a
	// worktree nobody could inspect never renders as verified clean.
	if !d.IsUndetermined() || !d.Unsettled() {
		t.Errorf("undetermined=%v unsettled=%v, want both true", d.IsUndetermined(), d.Unsettled())
	}
	if !d.Interrupted {
		t.Error("Interrupted was not set")
	}
	if got := d.UndeterminedReason(); got != "default branch resolution interrupted" {
		t.Errorf("UndeterminedReason() = %q, want the interrupted wording", got)
	}
	assertNoIncidents(t)
}

// TestInterruptedReapDefaultBranchRecordsNoFault covers the second recorder —
// the one whose process survives the signal. It also pins the verdict: an
// interrupt establishes nothing, so the reaper must skip, never reap.
func TestInterruptedReapDefaultBranchRecordsNoFault(t *testing.T) {
	bindFaultsForTest(t)
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	// Minted before the fixture, which replaces runGit with its own stub: the
	// interrupted value has to come from the PRODUCTION runGit to be real.
	interrupted := runGitInterrupted(t)

	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	// `git rev-parse --verify` is where every candidate is checked, so this is
	// where a signal reaching the sweep lands.
	f.revParseErr = interrupted
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(context.Background(), f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("maybeReapWorktree: %v", err)
	}
	if reaped {
		t.Fatal("an interrupted resolve must skip, never reap")
	}
	assertNoIncidents(t)
}

// TestOrdinaryUnresolvableStillRecords is the boundary the guard must not
// cross. ERR-0011 exists because an unresolvable default branch disables the
// unsettled probe and the reaper's unmerged-commits condition for the whole
// project — silencing that is the failure mode of over-applying E-2113.
func TestOrdinaryUnresolvableStillRecords(t *testing.T) {
	bindFaultsForTest(t)
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)
	(&unsettledStub{baseUnresolvable: true}).install(t)

	WorktreeUnsettledDetailAt(context.Background(), unsettledStubDir)

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want exactly 1", len(incidents))
	}
	if incidents[0].Code != faults.ErrCodeDefaultBranchUnresolved.ID {
		t.Errorf("code = %s, want %s", incidents[0].Code, faults.ErrCodeDefaultBranchUnresolved.ID)
	}
}

// TestOrdinaryFailureStillFallsThrough is the other boundary, and the reason
// branchIfExists returns interruptOnly(err) rather than err. `rev-parse
// --verify` exits non-zero for every candidate that is not a branch; that is
// this resolver's normal answer of "not this one". Propagating it would stop
// the fall-through at step 2 and break the four-step order E-1166 exists for.
func TestOrdinaryFailureStillFallsThrough(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	// origin/HEAD unset and init.defaultBranch naming a branch that is not here
	// — both steps fail, and `master` at step 4 must still be found.
	dir := fixtureRepo(t, "master")
	gitConfigSet(t, dir, "init.defaultBranch", "main")

	got, err := DefaultBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch = %q, want master", got)
	}
}
