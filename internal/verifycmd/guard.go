package verifycmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// ForeignLandedSuite is the own-task-only refusal: a request to run the
// verification suite of a task that has LANDED and is not one of the caller's
// own. It carries the facts the refusal is built from so the message can show
// its work rather than assert a conclusion.
//
// It is an error type rather than a doterr sentinel because the refusal has to
// TEACH — name what to do instead, and why a landed suite's result is
// meaningless — and a key=value metadata tail cannot carry that. The sentinel
// ErrForeignLandedSuite is still what callers match on.
type ForeignLandedSuite struct {
	// Requested is the task whose suite was asked for, in canonical form.
	Requested string
	// Owned are the tasks the caller may verify, and Sources says where each
	// came from (index-aligned).
	Owned   []string
	Sources []string
	// SuitesDir is the directory the rules for these suites are documented in.
	SuitesDir string
}

func (v *ForeignLandedSuite) Unwrap() error { return ErrForeignLandedSuite }

// Error renders the whole refusal, including the alternative and the reason.
// Saying only "no" to an agent that believes it has a good reason is how a rule
// gets rationalized past; the reason has to arrive with the refusal.
func (v *ForeignLandedSuite) Error() (msg string) {
	var b strings.Builder

	fmt.Fprintf(&b, "refusing to run %s's verification suite — it has landed, and it is not yours.\n\n", v.Requested)
	b.WriteString("A verification suite is a land-time proof of ONE task at ONE moment, not a\n")
	b.WriteString("regression suite. Its fixtures and assertions were pinned to the tree that\n")
	b.WriteString("existed when it landed, so whatever it reports now — pass OR fail — says\n")
	b.WriteString("nothing about your work. Acting on a failure in it means changing working\n")
	b.WriteString("code to satisfy a check that no longer describes it.\n\n")

	switch len(v.Owned) {
	case 0:
		b.WriteString("  yours:     nothing — no session task, and this is not a task worktree\n")
	default:
		for i, id := range v.Owned {
			label := "  yours:    "
			if i > 0 {
				label = "            "
			}
			fmt.Fprintf(&b, "%s %s (%s)\n", label, id, v.Sources[i])
		}
	}
	fmt.Fprintf(&b, "  requested: %s (landed)\n\n", v.Requested)

	b.WriteString("Instead:\n")
	switch len(v.Owned) {
	case 0:
		b.WriteString("  • run this from your task's worktree, and verify that task.\n")
	default:
		fmt.Fprintf(&b, "  • verify your own task:  endless task verify %s\n", v.Owned[0])
	}
	b.WriteString("  • coverage that must survive a land belongs in the project's own test\n")
	b.WriteString("    suite, not in another task's land-time proof.\n\n")
	fmt.Fprintf(&b, "The rules for this directory, in full: %s/CLAUDE.md", v.SuitesDir)

	msg = b.String()
	return msg
}

// guardOwnTaskOnly enforces the own-task-only rule at the one place that knows
// the task id for certain — before discovery, before isolation, before anything
// runs.
//
// The predicate is a CONJUNCTION, and both halves are load-bearing:
//
//   - landed, because an unlanded suite is live work whose result is meaningful
//     to whoever is looking at it, and
//   - not the caller's own, because a task that landed and was then REOPENED is
//     still the claiming session's task. Re-verifying and re-landing your own
//     reopened work is the documented path when shipped work turns out wrong; a
//     guard keyed on landed-ness alone would break it, which is why the second
//     conjunct is the point rather than an incidental detail.
//
// It fails OPEN on every unanswerable question — an id that is not a task ref,
// a database that cannot be read. This guard exists to stop a mistake somebody
// is making, not to be a precondition for working: a false refusal would block
// the one person who is doing the right thing, on the machine where the suite
// was written.
func guardOwnTaskOnly(id string, root dt.DirPath) (err error) {
	var num int64
	var own monitor.SuiteOwnership
	var owned []string

	num, err = taskNumber(id)
	if err != nil {
		err = nil
		goto end
	}

	own, err = monitor.SuiteOwnershipFor(num, string(root))
	if err != nil || !own.Known || !own.Landed || own.Owned {
		// A read failure is reported, but never as a refusal: see above.
		err = nil
		goto end
	}

	owned = make([]string, len(own.Tasks))
	for i, t := range own.Tasks {
		owned[i] = fmt.Sprintf("E-%d", t)
	}
	err = &ForeignLandedSuite{
		Requested: strings.ToUpper(id),
		Owned:     owned,
		Sources:   own.Source,
		SuitesDir: verify.SuitesDir,
	}
end:
	return err
}

// taskNumber parses the integer task number out of a canonical task reference
// ("E-1603", "e-1603", "1603"). A reference it cannot parse is not an error to
// report — it is a request the guard has nothing to say about — so callers
// treat the error as "no opinion".
func taskNumber(id string) (num int64, err error) {
	var s string

	s = strings.TrimSpace(id)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "E-"), "e-")
	num, err = strconv.ParseInt(s, 10, 64)
	if err == nil && num <= 0 {
		err = fmt.Errorf("task number must be positive: %q", id)
	}
	return num, err
}
