package monitor

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// unsettledStub drives runGit for the two probes the predicate runs, plus the
// display-only enrichment calls.
type unsettledStub struct {
	status     string
	statusErr  error
	revList    string
	revListErr error
	log        string
	logErr     error
	branch     string
	branchErr  error
}

func (s unsettledStub) install(t *testing.T) {
	t.Helper()
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(dir string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			return s.status, s.statusErr
		case "rev-list":
			return s.revList, s.revListErr
		case "log":
			return s.log, s.logErr
		case "symbolic-ref":
			return s.branch, s.branchErr
		}
		return "", nil
	}
}

// legacyUnsettled is a verbatim copy of the pre-E-1865 taskWorktreeUnsettled git
// logic (minus the DB resolution). TestUnsettledMatchesLegacyPredicate asserts
// the refactor did not change a single verdict — this is the whole safety
// argument for collapsing the predicate into UnsettledDetail, so it is kept as
// an independent oracle rather than expressed in terms of the new code.
func legacyUnsettled(wt string) bool {
	out, gerr := runGit(wt, "status", "--porcelain")
	if gerr != nil {
		return false
	}
	if strings.TrimSpace(out) != "" {
		return true
	}
	out, gerr = runGit(wt, "rev-list", "main..HEAD", "--count")
	if gerr != nil {
		return false
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	return perr == nil && n > 0
}

// TestUnsettledMatchesLegacyPredicate is the regression guard for E-1865's
// refactor: for every combination of git outcomes, the new detail-based verdict
// must equal the old boolean predicate. It covers the two orderings that a naive
// rewrite gets wrong — modified-with-failing-rev-list (still unsettled, because
// the original short-circuits on status before rev-list runs) and an unparsable
// count (settled).
func TestUnsettledMatchesLegacyPredicate(t *testing.T) {
	gitErr := errors.New("fatal: not a git repository")
	statuses := []struct {
		name string
		out  string
		err  error
	}{
		{"clean", "", nil},
		{"user file modified", " M src/endless/cli.py\n", nil},
		{"auto-managed only", " M .endless/verbs.jsonl\n", nil},
		{"status fails", "", gitErr},
	}
	revLists := []struct {
		name string
		out  string
		err  error
	}{
		{"zero ahead", "0\n", nil},
		{"three ahead", "3\n", nil},
		{"rev-list fails", "", gitErr},
		{"unparsable", "not-a-number\n", nil},
	}

	for _, st := range statuses {
		for _, rl := range revLists {
			t.Run(st.name+"/"+rl.name, func(t *testing.T) {
				unsettledStub{
					status: st.out, statusErr: st.err,
					revList: rl.out, revListErr: rl.err,
				}.install(t)

				want := legacyUnsettled("/wt")
				got := WorktreeUnsettledAt("/wt").Unsettled()
				if got != want {
					t.Errorf("verdict diverged from legacy predicate: got %v, want %v", got, want)
				}
			})
		}
	}
}

// TestVerdictPathSkipsEnrichment guards the hot path.
// AnnotateSessionStatusUnsettled runs the verdict probe once per row on EVERY
// flat `session status` render, so the marker must never pay for the commit
// subjects and branch that only `task unsettled <id>` displays. With dozens of
// worktrees those extra two calls per row are dozens of needless subprocesses.
func TestVerdictPathSkipsEnrichment(t *testing.T) {
	var called []string
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(dir string, args ...string) (string, error) {
		called = append(called, args[0])
		switch args[0] {
		case "status":
			return "", nil
		case "rev-list":
			return "3\n", nil
		case "log":
			return "abc1234 subject\n", nil
		case "symbolic-ref":
			return "task/x\n", nil
		}
		return "", nil
	}

	// Verdict-only: exactly the two predicate probes.
	d := WorktreeUnsettledAt("/wt")
	if !d.Unsettled() {
		t.Fatal("expected unsettled")
	}
	for _, c := range called {
		if c == "log" || c == "symbolic-ref" {
			t.Errorf("verdict path ran display-only git %q (calls: %v)", c, called)
		}
	}
	if len(called) != 2 {
		t.Errorf("verdict path ran %d git calls (%v), want 2", len(called), called)
	}

	// Detail path: same verdict, plus the enrichment.
	called = nil
	full := WorktreeUnsettledDetailAt("/wt")
	if full.Unsettled() != d.Unsettled() || full.UnlandedCount != d.UnlandedCount {
		t.Errorf("enrichment changed the verdict: %+v vs %+v", full, d)
	}
	if len(full.UnlandedLog) == 0 || full.Branch == "" {
		t.Errorf("detail path did not enrich: %+v", full)
	}
}

// TestUnsettledPartitionsModified proves auto-managed churn is reported
// separately but STILL counts toward unsettled — the ◆ predicate does not filter
// it, so an explanation that filtered it would explain a marker the user is not
// seeing.
func TestUnsettledPartitionsModified(t *testing.T) {
	unsettledStub{
		status:  " M src/endless/cli.py\n?? notes.txt\n M .endless/verbs.jsonl\n",
		revList: "0\n",
	}.install(t)

	d := WorktreeUnsettledAt("/wt")
	if !d.Unsettled() {
		t.Fatal("worktree with modifications must be unsettled")
	}
	if len(d.Modified) != 2 {
		t.Errorf("Modified = %v, want 2 user paths", d.Modified)
	}
	if len(d.AutoManaged) != 1 || d.AutoManaged[0] != ".endless/verbs.jsonl" {
		t.Errorf("AutoManaged = %v, want [.endless/verbs.jsonl]", d.AutoManaged)
	}
	if !d.IsModified() || d.IsUnlanded() {
		t.Errorf("sub-states wrong: modified=%v unlanded=%v", d.IsModified(), d.IsUnlanded())
	}
}

// TestUnsettledAutoManagedOnlyReason covers the case that prompted E-1865's
// "which is it?" question in its most confusing form: a ◆ caused purely by
// endless's own ledger churn, where the user sees no work of their own.
func TestUnsettledAutoManagedOnlyReason(t *testing.T) {
	unsettledStub{status: " M .endless/verbs.jsonl\n", revList: "0\n"}.install(t)

	d := WorktreeUnsettledAt("/wt")
	if !d.Unsettled() {
		t.Fatal("auto-managed churn alone must still be unsettled (matches the ◆)")
	}
	if got := d.Reason(); got != "modified (1 auto-managed only)" {
		t.Errorf("Reason() = %q", got)
	}
}

// TestUnsettledUnlandedOnly covers the clean-but-unlanded steady state: the
// normal pre-land condition, where the fix is `worktree land`, not a commit.
func TestUnsettledUnlandedOnly(t *testing.T) {
	unsettledStub{
		status:  "",
		revList: "2\n",
		log:     "abc1234 first\ndef5678 second\n",
		branch:  "task/1865-x\n",
	}.install(t)

	// Detail variant: this test asserts the display enrichment.
	d := WorktreeUnsettledDetailAt("/wt")
	if !d.Unsettled() || d.IsModified() || !d.IsUnlanded() {
		t.Fatalf("want unlanded-only; got unsettled=%v modified=%v unlanded=%v",
			d.Unsettled(), d.IsModified(), d.IsUnlanded())
	}
	if d.UnlandedCount != 2 || len(d.UnlandedLog) != 2 {
		t.Errorf("count=%d log=%v, want 2 and 2 entries", d.UnlandedCount, d.UnlandedLog)
	}
	if d.Branch != "task/1865-x" {
		t.Errorf("Branch = %q, want task/1865-x", d.Branch)
	}
	if got := d.Reason(); got != "unlanded (2 commits)" {
		t.Errorf("Reason() = %q", got)
	}
}

// TestUnsettledBothSubStates proves the reason names BOTH fixes when both apply
// — the case where telling the user only one of them would leave them stuck.
func TestUnsettledBothSubStates(t *testing.T) {
	unsettledStub{
		status:  " M src/endless/cli.py\n",
		revList: "1\n",
		log:     "abc1234 first\n",
	}.install(t)

	d := WorktreeUnsettledAt("/wt")
	if !d.IsModified() || !d.IsUnlanded() {
		t.Fatal("want both sub-states true")
	}
	if got := d.Reason(); got != "modified (1 file) + unlanded (1 commit)" {
		t.Errorf("Reason() = %q", got)
	}
}

// TestUnsettledSettledAndNoWorktree covers the two "nothing to explain" answers,
// which the list view must render distinctly: a worktree that is genuinely
// settled, and a task that never had one.
func TestUnsettledSettledAndNoWorktree(t *testing.T) {
	unsettledStub{status: "", revList: "0\n"}.install(t)
	d := WorktreeUnsettledAt("/wt")
	if d.Unsettled() {
		t.Error("clean + fully landed must be settled")
	}
	if got := d.Reason(); got != "settled" {
		t.Errorf("Reason() = %q, want settled", got)
	}

	none := WorktreeUnsettledAt("")
	if none.HasWorktree || none.Unsettled() {
		t.Error("empty path must yield no worktree and settled")
	}
	if got := none.Reason(); got != "no worktree" {
		t.Errorf("Reason() = %q, want 'no worktree'", got)
	}
}

// TestUnsettledDisplayFailuresDoNotChangeVerdict proves the enrichment calls are
// advisory: a failing `git log` or `symbolic-ref` may cost detail but must never
// flip (or suppress) the verdict.
func TestUnsettledDisplayFailuresDoNotChangeVerdict(t *testing.T) {
	boom := errors.New("git exploded")
	unsettledStub{
		status: "", revList: "4\n",
		logErr:    boom,
		branchErr: boom,
	}.install(t)

	// Detail variant: only it attempts the enrichment that fails here.
	d := WorktreeUnsettledDetailAt("/wt")
	if !d.Unsettled() || d.UnlandedCount != 4 {
		t.Fatalf("verdict changed by display-only failures: %+v", d)
	}
	if len(d.UnlandedLog) != 0 || d.Branch != "" {
		t.Errorf("expected empty display fields, got log=%v branch=%q", d.UnlandedLog, d.Branch)
	}
}

// TestStatusPathsParsing covers the porcelain shapes that trip up naive parsing:
// renames (the destination is what git tracks) and quoted paths.
func TestStatusPathsParsing(t *testing.T) {
	in := "R  old/name.go -> new/name.go\n" +
		" M \"quoted path.py\"\n" +
		"?? plain.txt\n"
	got := statusPaths(in)
	want := []string{"new/name.go", "quoted path.py", "plain.txt"}
	if len(got) != len(want) {
		t.Fatalf("statusPaths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d = %q, want %q", i, got[i], want[i])
		}
	}
}
