// Package landgate decides whether `endless worktree land` may proceed past the
// point where a branch's history is still its own (E-2184).
//
// Two parallel worktrees that each add a migration take the same next number.
// Git sees two different filenames and rebases cleanly; the collision surfaces
// afterwards, on every connect, as a migration tool refusing a duplicate
// version — or, when the higher number lands first, as a database "ahead of"
// its binary. Nothing in the land used to notice. The decision recorded on
// E-2182 is that land refuses and tells the agent how to fix it, rather than
// renumbering verified code itself.
//
// The gate has two halves, and a project uses either, both or neither:
//
//   - The built-in check. A project declares its migration directories in
//     .endless/config.json ("migrations": {"dirs": [...]}). Land refuses when
//     the branch ADDS files there and the base branch has ALSO added files
//     there since the two diverged. It is a git diff over configured paths and
//     knows nothing of goose, Alembic or Prisma, so it holds for any tool.
//     Each side counts only its own commits: a commit the other side already
//     carries under another SHA — what a rewritten base leaves on a branch —
//     belongs to neither, and when the branch holds any the land is refused
//     with `git rebase <base>` as the whole fix (E-2232).
//
//   - An optional project hook, .endless/hooks/pre-land.sh, for rules the
//     built-in check does not cover. It may veto the land and explain why.
//
// Either way the result is one Verdict carrying a one-line Summary and a Block
// meant to be pasted to the agent, so the caller has one renderer for both.
//
// The gate must run BEFORE land rebases the branch. Afterwards the merge-base
// is the base tip, the base has "gained" nothing, and the check passes a
// collision it exists to catch.
package landgate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/events"
)

// HookPath is where a project keeps its pre-land hook, relative to the project
// root. It sits beside post-worktree-create.sh and post-land/.
const HookPath = ".endless/hooks/pre-land.sh"

// Sources name what refused. The caller classifies by it: a migration
// collision is the agent's to fix, a hook's veto may or may not be, and a hook
// that cannot run is fixed on main, which is the user's.
const (
	SourceMigrations        = "migrations"
	SourceBaseRewritten     = "base_rewritten"
	SourceHook              = "hook"
	SourceHookNotExecutable = "hook_not_executable"
)

// Args is what the gate needs to know about one land.
type Args struct {
	// ProjectRoot is the main checkout: where the config and the hook are read.
	// The branch's own copies are deliberately NOT consulted — a branch must not
	// be able to wave itself through by editing the rule that gates it.
	ProjectRoot string
	// Worktree is the task worktree whose HEAD is being landed.
	Worktree string
	// Base is the branch the land merges into.
	Base string
	// Task is the canonical task id, E-NNN, for the instructions.
	Task string
}

// Rename is one of the branch's migrations and, when the gate can work it out,
// the name it must take to follow what the base branch gained.
type Rename struct {
	Path string `json:"path"`
	// To is the new repo-relative path, or "" when the names carry no numeric
	// prefix the gate can advance (e.g. Alembic's hash revisions).
	To string `json:"to,omitempty"`
}

// Verdict is the gate's answer. Refused false means the land may proceed and
// every other field is empty.
type Verdict struct {
	Refused bool   `json:"refused"`
	Source  string `json:"source,omitempty"`
	// Summary is ONE line: the whole verdict, readable alone.
	Summary string `json:"summary,omitempty"`
	// Block is the fix, written to be pasted to an agent and read by a person.
	Block string `json:"block,omitempty"`

	MergeBase string   `json:"merge_base,omitempty"`
	Landed    []string `json:"landed,omitempty"`
	Branch    []Rename `json:"branch,omitempty"`
}

// Check runs the built-in migration check, then the project's hook. The first
// refusal wins: a land blocked for two reasons is fixed one reason at a time,
// and the hook is not asked about a branch the built-in check already refused.
func Check(a Args) (v Verdict, err error) {
	var dirs []string
	dirs, err = migrationDirs(a.ProjectRoot)
	if err != nil {
		return v, err
	}
	if len(dirs) > 0 {
		v, err = checkMigrations(a, dirs)
		if err != nil || v.Refused {
			return v, err
		}
	}
	return runHook(a)
}

func migrationDirs(projectRoot string) (dirs []string, err error) {
	cfg, err := config.Load(dt.DirPath(projectRoot))
	if err != nil {
		return nil, err
	}
	for _, d := range cfg.Migrations.Dirs {
		d = strings.Trim(filepath.ToSlash(strings.TrimSpace(d)), "/")
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}

func checkMigrations(a Args, dirs []string) (v Verdict, err error) {
	mb, err := git(a.Worktree, "merge-base", a.Base, "HEAD")
	if err != nil {
		return v, fmt.Errorf("merge-base of %s and HEAD: %w", a.Base, err)
	}
	mb = strings.TrimSpace(mb)

	sides, err := ownCommits(a.Worktree, a.Base)
	if err != nil {
		return v, err
	}

	branchAdded, err := ownAdded(a.Worktree, mb, "HEAD", sides.branch, dirs)
	if err != nil {
		return v, err
	}
	landed, err := ownAdded(a.Worktree, mb, a.Base, sides.base, dirs)
	if err != nil {
		return v, err
	}

	// A real collision outranks a rewritten base: its instructions already
	// start with the rebase that clears the rewrite.
	if len(branchAdded) > 0 && len(landed) > 0 {
		var onBase string
		onBase, err = git(a.Worktree, append(
			[]string{"ls-tree", "-r", "--name-only", a.Base, "--"}, dirs...)...)
		if err != nil {
			return v, fmt.Errorf("list %s's migrations: %w", a.Base, err)
		}
		v = Verdict{
			Refused:   true,
			Source:    SourceMigrations,
			MergeBase: mb,
			Landed:    landed,
			Branch:    renames(branchAdded, lines(onBase)),
		}
		v.Summary = migrationSummary(a, v)
		v.Block = migrationBlock(a, v)
		return v, nil
	}

	if sides.equivalent > 0 {
		v = Verdict{Refused: true, Source: SourceBaseRewritten, MergeBase: mb}
		v.Summary = rewrittenSummary(a)
		v.Block = rewrittenBlock(a, sides.equivalent)
		return v, nil
	}
	return v, nil
}

// commitSides is each side's own commits since the two diverged, and how many
// of the branch's commits base already carries under another SHA.
type commitSides struct {
	branch     []string
	base       []string
	equivalent int
}

// ownCommits splits <base>...HEAD by patch identity (E-2232). A commit whose
// change is already on the other side under another SHA belongs to neither: it
// is what a rewritten base leaves behind. When base is rebased or amended after
// the branch forks — `git pull` with pull.rebase onto a commit made on the host
// is enough — every replayed commit gets a new SHA, the merge-base falls back
// to before the rewrite, and the branch's old copies of base's commits look
// like the branch's own work. Diffing from the merge-base would then report
// base's own migrations as the branch adding them.
func ownCommits(repo, base string) (s commitSides, err error) {
	marked := func(side string) (map[string][]string, error) {
		out, err := git(repo, "log", side, "--cherry-mark", "--no-merges",
			"--format=%m %H", base+"...HEAD")
		if err != nil {
			return nil, fmt.Errorf("compare commits on %s and HEAD: %w", base, err)
		}
		m := map[string][]string{}
		for _, ln := range lines(out) {
			mark, sha, _ := strings.Cut(ln, " ")
			m[mark] = append(m[mark], sha)
		}
		return m, nil
	}
	branch, err := marked("--right-only")
	if err != nil {
		return s, err
	}
	onBase, err := marked("--left-only")
	if err != nil {
		return s, err
	}
	// With one side selected, --cherry-mark prints that side's arrow for a
	// commit of its own and "=" for one the other side also carries.
	s.branch = branch[">"]
	s.base = onBase["<"]
	s.equivalent = len(branch["="])
	return s, nil
}

// ownAdded lists the files under dirs that `to` adds relative to `from` AND
// that one of `commits` adds. The first half is the net effect, so a file added
// then removed is not counted; the second confines it to that side's own work.
func ownAdded(repo, from, to string, commits, dirs []string) ([]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	net, err := added(repo, from, to, dirs)
	if err != nil || len(net) == 0 {
		return nil, err
	}
	cmd := exec.Command("git", append(
		[]string{"-C", repo, "diff-tree", "--stdin", "-r", "--no-commit-id",
			"--no-renames", "--diff-filter=A", "--name-only", "--"}, dirs...)...)
	cmd.Env = events.SanitizedGitEnv()
	cmd.Stdin = strings.NewReader(strings.Join(commits, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list migrations added by %s's own commits: %w: %s",
			to, err, strings.TrimSpace(stderr.String()))
	}
	own := map[string]bool{}
	for _, f := range lines(string(out)) {
		own[f] = true
	}
	var files []string
	for _, f := range net {
		if own[f] {
			files = append(files, f)
		}
	}
	return files, nil
}

// added lists the files under dirs that `to` adds relative to `from`.
func added(repo, from, to string, dirs []string) ([]string, error) {
	out, err := git(repo, append(
		[]string{"diff", "--no-renames", "--diff-filter=A", "--name-only", from, to, "--"},
		dirs...)...)
	if err != nil {
		return nil, fmt.Errorf("list migrations added between %s and %s: %w", from, to, err)
	}
	files := lines(out)
	sort.Strings(files)
	return files, nil
}

// numericPrefix matches the leading version of names like 00008_add_x.sql or
// 20260928120000_add_x.sql. The digits' WIDTH is kept so a renumber pads the
// way the project already pads.
var numericPrefix = regexp.MustCompile(`^(\d+)(.*)$`)

// renames proposes, for each migration the branch adds, the next free number
// after everything the base branch now holds in the same directory.
//
// Only a numeric prefix is advanced. A tool whose versions are not ordered
// integers (Alembic's revision hashes) gets no proposal, and the instructions
// fall back to naming the fix in the tool's own terms.
//
// A timestamp-prefixed tool gets "the base's newest + 1", which is still after
// everything that landed — the only property the fix needs.
func renames(branchAdded, onBase []string) []Rename {
	maxByDir := map[string]uint64{}
	for _, f := range onBase {
		if m := numericPrefix.FindStringSubmatch(path.Base(f)); m != nil {
			n, err := strconv.ParseUint(m[1], 10, 64)
			if err == nil && n > maxByDir[path.Dir(f)] {
				maxByDir[path.Dir(f)] = n
			}
		}
	}

	out := make([]Rename, 0, len(branchAdded))
	for _, f := range branchAdded {
		r := Rename{Path: f}
		dir := path.Dir(f)
		m := numericPrefix.FindStringSubmatch(path.Base(f))
		if m != nil && maxByDir[dir] > 0 {
			maxByDir[dir]++
			r.To = path.Join(dir, fmt.Sprintf("%0*d%s", len(m[1]), maxByDir[dir], m[2]))
		}
		out = append(out, r)
	}
	return out
}

func migrationSummary(a Args, v Verdict) string {
	return fmt.Sprintf(
		"cannot land %s: %s gained %s since this branch forked, and the branch "+
			"adds %s of its own — renumber after what landed, then land again. "+
			"Nothing was merged.",
		a.Task, a.Base,
		plural(len(v.Landed), "migration"),
		plural(len(v.Branch), "migration"))
}

func migrationBlock(a Args, v Verdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s gained migrations while %s's branch was open, so the "+
		"branch's own migrations are no longer next in line. Migration tools order "+
		"by version, and two migrations sharing a version — or an older version "+
		"arriving after a newer one — break every database that runs them.\n\n",
		a.Base, a.Task)

	fmt.Fprintf(&b, "Landed on %s since the branch forked:\n", a.Base)
	for _, f := range v.Landed {
		fmt.Fprintf(&b, "  %s\n", f)
	}

	b.WriteString("\nThis branch adds:\n")
	renumbered, unknown := false, false
	for _, r := range v.Branch {
		switch r.To {
		case "":
			unknown = true
			fmt.Fprintf(&b, "  %s\n", r.Path)
		case r.Path:
			fmt.Fprintf(&b, "  %s  (already numbered after what landed)\n", r.Path)
		default:
			renumbered = true
			fmt.Fprintf(&b, "  %s  →  %s\n", r.Path, r.To)
		}
	}

	fmt.Fprintf(&b, "\nFix it in the worktree, %s:\n", a.Worktree)
	fmt.Fprintf(&b, "  1. Bring in what landed: git rebase %s\n", a.Base)
	switch {
	case unknown:
		b.WriteString("  2. Re-order each migration after the ones that landed, in " +
			"whatever terms your migration tool uses for its version or parent.\n")
	case renumbered:
		b.WriteString("  2. Rename each migration as shown above, and change any version " +
			"the file or a registry declares for it to match.\n")
	default:
		b.WriteString("  2. The numbers already follow what landed; no rename is needed.\n")
	}
	b.WriteString("  3. Read what landed and check the branch's migration still holds " +
		"on top of it: the same tables, columns and data it assumed.\n")
	b.WriteString("  4. Reset the sandbox: endless sandbox reset\n" +
		"     Its database already ran the old version. Tools track migrations by " +
		"version, so a renumbered one would be skipped or applied twice there.\n")
	fmt.Fprintf(&b, "  5. Re-verify: endless task verify %s\n", a.Task)
	fmt.Fprintf(&b, "  6. Land again: endless worktree land %s\n", a.Task)
	return b.String()
}

func rewrittenSummary(a Args) string {
	return fmt.Sprintf(
		"cannot land %s: %s was rewritten since this branch forked — run "+
			"git rebase %s in the worktree, then land again. Nothing was merged.",
		a.Task, a.Base, a.Base)
}

func rewrittenBlock(a Args, equivalent int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This branch carries %s that %s already holds under "+
		"different SHAs: %s was rebased or amended after the branch forked, and "+
		"the branch kept the old copies. Land refuses until the branch drops "+
		"them, so what it checks and merges is the branch's own work on top of "+
		"%s as it is now.\n\n",
		plural(equivalent, "commit"), a.Base, a.Base, a.Base)
	fmt.Fprintf(&b, "Fix it in the worktree, %s:\n", a.Worktree)
	fmt.Fprintf(&b, "  1. git rebase %s\n"+
		"     Git drops the commits %s already has, keeping only the branch's own.\n",
		a.Base, a.Base)
	fmt.Fprintf(&b, "  2. Land again: endless worktree land %s\n\n", a.Task)
	fmt.Fprintf(&b, "Do not rename or renumber any migration for this: the ones "+
		"that look duplicated are %s's own.\n", a.Base)
	return b.String()
}

// runHook runs the project's pre-land hook, if it has one.
//
// Contract: cwd is the worktree; argv[1] is the worktree path and argv[2] the
// base branch; ENDLESS_TASK_ID and ENDLESS_BASE_BRANCH carry the same facts by
// name. Exit 0 allows the land. Any other exit refuses it, and the hook's
// stdout becomes the refusal: its first non-blank line the one-line summary,
// the rest the block for the agent. A hook that prints nothing still refuses,
// with its stderr as the explanation.
//
// A hook that exists but is not executable REFUSES rather than being skipped.
// The project wrote a gate; silently landing past it because a mode bit was
// lost is the one outcome the file cannot have been meant to allow.
func runHook(a Args) (v Verdict, err error) {
	hook := filepath.Join(a.ProjectRoot, filepath.FromSlash(HookPath))
	info, err := os.Stat(hook)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, fmt.Errorf("stat %s: %w", hook, err)
	}
	if info.Mode()&0o111 == 0 {
		return Verdict{
			Refused: true,
			Source:  SourceHookNotExecutable,
			Summary: fmt.Sprintf("cannot land %s: the project's pre-land hook "+
				"is not executable, so it could not be asked. Nothing was merged.", a.Task),
			Block: fmt.Sprintf("The project gates every land through %s, and that "+
				"file is not executable. Make it executable, then land again:\n\n"+
				"  chmod +x %s\n  endless worktree land %s\n", HookPath, hook, a.Task),
		}, nil
	}

	cmd := exec.Command(hook, a.Worktree, a.Base)
	cmd.Dir = a.Worktree
	cmd.Env = append(os.Environ(),
		"ENDLESS_TASK_ID="+a.Task,
		"ENDLESS_BASE_BRANCH="+a.Base,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return v, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return v, fmt.Errorf("run %s: %w", hook, runErr)
	}

	v = Verdict{Refused: true, Source: SourceHook}
	summary, block, _ := strings.Cut(strings.TrimLeft(stdout.String(), " \t\r\n"), "\n")
	v.Summary = strings.TrimSpace(summary)
	v.Block = strings.Trim(block, "\n")
	if v.Summary == "" {
		v.Summary = fmt.Sprintf("cannot land %s: the project's pre-land hook "+
			"refused (exit %d). Nothing was merged.", a.Task, exitErr.ExitCode())
		v.Block = strings.TrimSpace(stderr.String())
	}
	if v.Block != "" {
		v.Block += "\n"
	}
	return v, nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = events.SanitizedGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func lines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
