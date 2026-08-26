package monitor

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// unsettledStub drives runGit for the probes the predicate runs, plus the
// display-only enrichment calls.
//
// The default-branch resolver (E-1940) also shells out, so the stub answers its
// calls too: `rev-parse --verify` succeeds (every candidate exists) and the
// candidate chain lands on `main` unless baseUnresolvable says otherwise. Note
// that symbolic-ref is used by BOTH the resolver (origin/HEAD) and the
// enrichment (HEAD), so the dispatch keys on the ref being asked about, never
// on args[0] alone.
type unsettledStub struct {
	status     string
	statusErr  error
	revList    string
	revListErr error
	log        string
	logErr     error
	branch     string
	branchErr  error
	// baseUnresolvable makes every branch candidate fail to resolve, which is
	// the state DefaultBranch reports as ErrDefaultBranchUnresolved.
	baseUnresolvable bool
	// revListArgs records the arguments of the counting rev-list, so a test can
	// assert which revisions were excluded.
	revListArgs *[]string
}

func (s unsettledStub) install(t *testing.T) {
	t.Helper()
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	t.Cleanup(resetDefaultBranchCache)
	resetDefaultBranchCache()
	runGit = func(dir string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			return s.status, s.statusErr
		case "rev-list":
			if s.revListArgs != nil {
				*s.revListArgs = append([]string{}, args...)
			}
			return s.revList, s.revListErr
		case "log":
			return s.log, s.logErr
		case "symbolic-ref":
			// The resolver asks for origin/HEAD; the enrichment asks for HEAD.
			if args[len(args)-1] == "refs/remotes/origin/HEAD" {
				return "", errors.New("not a symbolic ref")
			}
			return s.branch, s.branchErr
		case "config":
			return "", errors.New("unset")
		case "rev-parse":
			if s.baseUnresolvable {
				return "", errors.New("unknown revision")
			}
			return "deadbeef\n", nil
		}
		return "", nil
	}
}

// legacyUnsettled is a verbatim copy of the pre-E-1865 taskWorktreeUnsettled git
// logic (minus the DB resolution): fail-open, hardcoded `main`, no credit for a
// recorded landing. Kept as an independent oracle so the tests below can state
// where today's verdict deliberately differs from it, rather than asserting the
// new code against itself.
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

// TestUnsettledDivergesFromLegacyOnlyWhereIntended is the successor to E-1865's
// "must match the legacy predicate exactly" guard. E-1940 broke that equality on
// purpose, in ONE direction: where a probe could not run, the legacy predicate
// answered settled and today's answers undetermined-so-unsettled.
//
// Stating it as "diverges exactly here and nowhere else" keeps both halves under
// test. A regression that re-opened the fail-open hole fails on the divergent
// cases; a regression that made some unrelated verdict flip fails on the rest.
func TestUnsettledDivergesFromLegacyOnlyWhereIntended(t *testing.T) {
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

				legacy := legacyUnsettled("/wt")
				d := WorktreeUnsettledAt("/wt")

				// A probe that could not run is the ONLY licensed divergence.
				probeFailed := st.err != nil || rl.err != nil || rl.name == "unparsable"
				if d.IsUndetermined() != probeFailed {
					t.Fatalf("IsUndetermined() = %v, want %v (reason %q)",
						d.IsUndetermined(), probeFailed, d.Reason())
				}
				if probeFailed {
					if !d.Unsettled() {
						t.Errorf("a probe that could not run must NOT read as the all-clear")
					}
					// A status failure short-circuits, so an unrunnable status
					// probe is the reported cause even when rev-list also fails.
					if st.err != nil && d.StatusErr == "" {
						t.Errorf("status failure not recorded: %+v", d)
					}
					return
				}
				if got := d.Unsettled(); got != legacy {
					t.Errorf("verdict diverged where it must not: got %v, want %v (%s)",
						got, legacy, d.Reason())
				}
			})
		}
	}
}

// TestUndeterminedIsNotUnlanded pins the distinction the detail view exists to
// draw. Both make the row ◆, and collapsing them is what made "I could not
// tell" indistinguishable from "you have work to land".
func TestUndeterminedIsNotUnlanded(t *testing.T) {
	unsettledStub{revListErr: errors.New("boom")}.install(t)
	d := WorktreeUnsettledAt("/wt")

	if !d.Unsettled() || !d.IsUndetermined() {
		t.Fatalf("failed probe must be unsettled AND undetermined: %+v", d)
	}
	if d.IsUnlanded() || d.IsModified() {
		t.Errorf("undetermined must not claim a sub-state it could not measure: %+v", d)
	}
	if got := d.UndeterminedReason(); !strings.Contains(got, "rev-list") {
		t.Errorf("UndeterminedReason() = %q, want it to name the failing probe", got)
	}
	if got := d.Reason(); !strings.HasPrefix(got, "undetermined (") {
		t.Errorf("Reason() = %q, want it to lead with undetermined", got)
	}
}

// TestUnresolvedDefaultBranchIsUndetermined covers the failure that used to be
// permanent and invisible: on a repo whose default branch is neither resolvable
// nor `main`, the hardcoded probe exited 128 on every tick and the fail-open
// verdict rendered a clean row forever.
func TestUnresolvedDefaultBranchIsUndetermined(t *testing.T) {
	unsettledStub{baseUnresolvable: true, revList: "0\n"}.install(t)
	d := WorktreeUnsettledAt("/wt")

	if !d.Unsettled() || !d.IsUndetermined() || d.BaseErr == "" {
		t.Fatalf("unresolvable default branch must read as undetermined: %+v", d)
	}
	if d.Base != "" {
		t.Errorf("Base = %q, want empty — no caller may be handed a guess", d.Base)
	}
	if got := d.Reason(); !strings.Contains(got, "default branch unresolved") {
		t.Errorf("Reason() = %q", got)
	}
}

// TestUnlandedRangeCreditsRecordedLandings is the core of E-1940's second half:
// `worktree land` rebases, so the branch keeps SHAs that main will never have,
// and the range must exclude each recorded landing or the count reports landed
// work as unlanded forever.
func TestUnlandedRangeCreditsRecordedLandings(t *testing.T) {
	var got []string
	unsettledStub{revList: "0\n", revListArgs: &got}.install(t)

	worktreeUnsettledAt("/wt", []string{"aaa111", "bbb222"}, false)

	want := []string{"rev-list", "--count", "--ignore-missing", "HEAD", "^main", "^aaa111", "^bbb222"}
	if len(got) != len(want) {
		t.Fatalf("rev-list args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rev-list args = %v, want %v", got, want)
		}
	}
}

// TestNoLandingsLeavesTheLegacyRange proves the credit is additive. A task that
// never landed must be measured exactly as before, or the fix would quietly
// change the answer for every worktree in flight.
func TestNoLandingsLeavesTheLegacyRange(t *testing.T) {
	var got []string
	unsettledStub{revList: "2\n", revListArgs: &got}.install(t)

	d := worktreeUnsettledAt("/wt", nil, false)

	for _, arg := range got {
		if strings.HasPrefix(arg, "^") && arg != "^main" {
			t.Errorf("nothing was landed, yet %q was excluded (args %v)", arg, got)
		}
	}
	if !d.IsUnlanded() || d.UnlandedCount != 2 {
		t.Errorf("want 2 unlanded commits, got %+v", d)
	}
}

// TestVerdictPathSkipsEnrichment guards the hot path.
// AnnotateSessionStatusUnsettled runs the verdict probe once per row on EVERY
// flat `session status` render, so the marker must never pay for the commit
// subjects and branch that only `task unsettled <id>` displays. With dozens of
// worktrees those extra two calls per row are dozens of needless subprocesses.
func TestVerdictPathSkipsEnrichment(t *testing.T) {
	var called [][]string
	unsettledStub{
		status:      "",
		revList:     "3\n",
		log:         "abc1234 subject\n",
		branch:      "task/x\n",
		revListArgs: nil,
	}.install(t)
	inner := runGit
	runGit = func(dir string, args ...string) (string, error) {
		called = append(called, args)
		return inner(dir, args...)
	}

	// Verdict-only: the two predicate probes, and nothing display-only.
	d := WorktreeUnsettledAt("/wt")
	if !d.Unsettled() {
		t.Fatal("expected unsettled")
	}
	for _, c := range called {
		if c[0] == "log" || (c[0] == "symbolic-ref" && c[len(c)-1] == "HEAD") {
			t.Errorf("verdict path ran display-only git %v (calls: %v)", c, called)
		}
	}

	// The resolver's own calls must be paid ONCE per repo, not once per row:
	// `session monitor` re-renders every two seconds.
	before := len(called)
	if second := WorktreeUnsettledAt("/wt"); second.UnlandedCount != d.UnlandedCount {
		t.Fatalf("second probe disagreed: %+v vs %+v", second, d)
	}
	if n := len(called) - before; n != 2 {
		t.Errorf("second probe ran %d git calls, want 2 (default branch not memoized)", n)
	}

	// Detail path: same verdict, plus the enrichment.
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
