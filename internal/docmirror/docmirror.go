// Package docmirror owns one fact: where a task's or a decision's document
// mirror lives on disk (E-2137).
//
// A mirror is a projection of database content — a task's plan, outcome,
// reason, analysis or notes (one task_content row each, E-1531), a decision's
// body — written to a committed `.md` file so a human can read it on github.com
// without a database. The row is the source of truth; the file is derived from
// it and can always be regenerated.
//
// # Why a package with no dependencies
//
// Four unrelated places need the convention and only the convention: the
// Claude hook that refuses a hand-edit of a mirror, the sweep that keeps main's
// mirrors current, the commit path, and the Python CLI (which keeps its own copy
// — see src/endless/doc_mirror.py, and keep the two in step). A leaf package
// lets the hook — which runs on every tool call — depend on the convention
// without dragging in the database, git, or the job runner behind it.
//
// # The layout, and the one it replaces
//
//	.endless/tasks/e-2137/plan.md       <- .endless/plans/E-2137.md
//	.endless/tasks/e-2137/analysis.md   <- .endless/analyses/E-2137.md
//	.endless/tasks/e-2137/outcome.md    <- .endless/outcomes/E-2137.md
//	.endless/tasks/e-2137/verify.sh     (already there, and the task's own)
//
// `.endless/tasks/e-NNNN/` already belonged to the task: it is where that task's
// verification suite lives. Consolidating means an allowlist of "directories
// holding task content" — which went stale once already — becomes one glob,
// `.endless/tasks/e-*/*.md`, that no future content kind can invalidate.
//
// Decisions do NOT move. `ED-NNNN` has no owning task, so it is not task-scoped,
// and E-1868 is about to renumber every decision id: moving those files now
// would move each one twice and land it at a name that is about to be wrong.
package docmirror

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Kind is one mirrored task-document kind: the content name that owns it, the
// filename it is written under, the word used when talking about it, and the
// pre-consolidation directory a legacy copy may still be sitting in.
type Kind struct {
	// Name is the task_content name holding the authoritative content.
	Name taskcontent.Name

	// Stem is the mirror's filename without the extension, inside the task's
	// own directory — the content name's slug, by the one-token convention.
	Stem string

	// Label is the noun used in commit subjects and CLI output ("plan").
	Label string

	// LegacyDir is the directory under `.endless/` this kind was mirrored into
	// before consolidation, or "" for a kind that never was. Retained because
	// the sweep must still RECOGNIZE a file there in order to relocate it — a
	// worktree running an older Endless keeps writing to the old path and
	// delivers it to main whenever it lands.
	LegacyDir string
}

// legacyDirs are the pre-consolidation directories, for the three kinds that
// existed before it. Frozen: no kind added later was ever mirrored there.
var legacyDirs = map[taskcontent.Name]string{
	taskcontent.Plan:     "plans",
	taskcontent.Outcome:  "outcomes",
	taskcontent.Analysis: "analyses",
}

// TaskKinds is every task-scoped document kind, one per content name. Derived
// from taskcontent rather than listed (E-1531), so a content kind declared
// there is mirrored, recognized and swept with no change here: the path, both
// recognizers and the sweep all read from this one list.
//
// `notes` and `reason` arrived with E-1531. notes had no mirror before, so its
// arrival makes a previously invisible field visible in the repo; reason is the
// closing half of what outcome.md used to hold.
var TaskKinds = func() []Kind {
	kinds := make([]Kind, 0, len(taskcontent.All()))
	for _, n := range taskcontent.All() {
		kinds = append(kinds, Kind{Name: n, Stem: n.Slug(), Label: n.Slug(), LegacyDir: legacyDirs[n]})
	}
	return kinds
}()

// TasksRoot is the directory holding one subdirectory per task.
const TasksRoot = ".endless/tasks"

// DecisionsDir is where decision mirrors live, and where they stay.
const DecisionsDir = ".endless/decisions"

// TaskDir returns a task's own directory, repo-relative.
//
// The casing is lowercase `e-NNNN`, matching the directory verification suites
// have always used. It is spelled here exactly once so no caller has to
// remember — a hand-written path gets the case wrong silently on a
// case-insensitive filesystem and loudly on everyone else's.
func TaskDir(taskID int64) (dir string) {
	return fmt.Sprintf("%s/e-%d", TasksRoot, taskID)
}

// TaskDocPath returns the repo-relative path of one task document mirror.
func TaskDocPath(taskID int64, stem string) (path string) {
	return fmt.Sprintf("%s/%s.md", TaskDir(taskID), stem)
}

// LegacyTaskDocPath returns where this kind's mirror was written before
// consolidation. Used only to FIND a file to relocate; nothing writes here.
func LegacyTaskDocPath(kind Kind, taskID int64) (path string) {
	return fmt.Sprintf(".endless/%s/E-%d.md", kind.LegacyDir, taskID)
}

// DecisionDocPath returns the repo-relative path of a decision body mirror.
func DecisionDocPath(decisionID int64) (path string) {
	return fmt.Sprintf("%s/ED-%d.md", DecisionsDir, decisionID)
}

// TaskDocSubject is the commit subject for one task document mirror. The id is
// in the subject so two different tasks' mirrors never amend over each other —
// `canAmend` requires the subject to match, and that is the only thing keeping
// them apart.
func TaskDocSubject(action, label string, taskID int64) (subject string) {
	return fmt.Sprintf("Endless: %s %s for E-%d", action, label, taskID)
}

// DecisionDocSubject is the commit subject for a decision body mirror.
func DecisionDocSubject(action string, decisionID int64) (subject string) {
	return fmt.Sprintf("Endless: %s decision ED-%d", action, decisionID)
}

// TaskDocRe matches a task document mirror at its consolidated path, absolute
// or repo-relative. The alternation is closed on purpose: `.endless/tasks/e-N/`
// also holds `verify.sh` and `verify.toml`, which are the TASK's files to write.
// A pattern that matched the whole directory would refuse a session's own
// verification suite.
//
// Closed, but not typed out: the alternation is built from TaskKinds, so it
// names exactly the stems the sweep writes. A literal list here was the one
// place where "a new content kind is just an INSERT" stopped being true.
var TaskDocRe = regexp.MustCompile(`(^|/)\.endless/tasks/e-\d+/(` + stemAlternation() + `)\.md$`)

// LegacyTaskDocRe matches a task document mirror at its pre-consolidation path.
var LegacyTaskDocRe = regexp.MustCompile(`(^|/)\.endless/(` + legacyDirAlternation() + `)/E-\d+\.md$`)

// stemAlternation is every task kind's stem, as a regexp alternation.
func stemAlternation() string {
	stems := make([]string, len(TaskKinds))
	for i, k := range TaskKinds {
		stems[i] = regexp.QuoteMeta(k.Stem)
	}
	return strings.Join(stems, "|")
}

// legacyDirAlternation is every kind's pre-consolidation directory, as a
// regexp alternation. Kinds that never had one contribute nothing.
func legacyDirAlternation() string {
	var dirs []string
	for _, k := range TaskKinds {
		if k.LegacyDir != "" {
			dirs = append(dirs, regexp.QuoteMeta(k.LegacyDir))
		}
	}
	return strings.Join(dirs, "|")
}

// DecisionDocRe matches a decision body mirror.
var DecisionDocRe = regexp.MustCompile(`(^|/)\.endless/decisions/ED-\d+\.md$`)

// IsMirrorPath reports whether path is any document mirror Endless writes —
// consolidated, legacy, or a decision. It is the "this content belongs to the
// database, not to you" test.
func IsMirrorPath(path string) (isMirror bool) {
	return TaskDocRe.MatchString(path) ||
		LegacyTaskDocRe.MatchString(path) ||
		DecisionDocRe.MatchString(path)
}
