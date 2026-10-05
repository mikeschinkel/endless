// Package baserewrite is the one definition of "the base branch was rewritten
// under this branch" (E-2232, E-2233).
//
// When base is rebased or amended after a branch forks — `git pull` with
// pull.rebase onto a commit made on the host is enough — every replayed commit
// gets a new SHA, the merge-base falls back to before the rewrite, and the
// branch keeps its old copies of base's commits. Those copies are not the
// branch's work, but SHA-level git says they are.
//
// The land gate refuses a branch carrying copies, the main-sync job reports the
// open branches that carry them, and `endless worktree check` names the one you
// are in. All three ask this package, so they cannot disagree about what a
// rewrite is.
//
// It runs git through the caller's runner rather than its own: each caller
// already owns how its git children are built (sanitized environment, context
// deadline, a test seam), and this package must not import any of them.
package baserewrite

import (
	"fmt"
	"strings"
)

// Git runs one git command in the repository being judged and returns stdout.
type Git func(args ...string) (string, error)

// Sides is each side's own commits since the two diverged, and the branch's
// commits that base already carries under another SHA.
type Sides struct {
	Branch     []string
	Base       []string
	Equivalent []string
}

// Split divides <base>...<head> by patch identity. A commit whose change is
// already on the other side under another SHA belongs to neither side: it is
// what a rewritten base leaves behind, and it lands in Equivalent.
func Split(git Git, base, head string) (s Sides, err error) {
	branch, err := marked(git, "--right-only", base, head)
	if err != nil {
		return s, err
	}
	onBase, err := marked(git, "--left-only", base, head)
	if err != nil {
		return s, err
	}
	// With one side selected, --cherry-mark prints that side's arrow for a
	// commit of its own and "=" for one the other side also carries.
	s.Branch = branch[">"]
	s.Base = onBase["<"]
	s.Equivalent = branch["="]
	return s, nil
}

// BranchSide is Split's branch half alone, at half the cost: Branch and
// Equivalent are filled and Base is left empty. It is enough to answer "was
// base rewritten under this branch?", which is all a report needs.
func BranchSide(git Git, base, head string) (s Sides, err error) {
	branch, err := marked(git, "--right-only", base, head)
	if err != nil {
		return s, err
	}
	s.Branch = branch[">"]
	s.Equivalent = branch["="]
	return s, nil
}

func marked(git Git, side, base, head string) (map[string][]string, error) {
	out, err := git("log", side, "--cherry-mark", "--no-merges",
		"--format=%m %H", base+"..."+head)
	if err != nil {
		return nil, fmt.Errorf("compare commits on %s and %s: %w", base, head, err)
	}
	m := map[string][]string{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		mark, sha, _ := strings.Cut(ln, " ")
		m[mark] = append(m[mark], sha)
	}
	return m, nil
}
