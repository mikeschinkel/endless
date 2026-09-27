package docmirror

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// TestResolve pins the one question every mirror reader asks: git hands me a
// changed file — which task and content name own what it should say?
//
// The consolidated and legacy shapes must resolve to the SAME source, because
// the whole point of keeping the legacy recognizer is that a file in the old
// place still belongs to the same content and can be relocated rather than
// puzzled over.
func TestResolve(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		ok         bool
		taskID     int64
		decisionID int64
		content    taskcontent.Name
		legacy     bool
	}{
		{name: "plan", path: ".endless/tasks/e-2137/plan.md",
			ok: true, taskID: 2137, content: taskcontent.Plan},
		{name: "outcome", path: ".endless/tasks/e-7/outcome.md",
			ok: true, taskID: 7, content: taskcontent.Outcome},
		{name: "analysis", path: ".endless/tasks/e-7/analysis.md",
			ok: true, taskID: 7, content: taskcontent.Analysis},
		{name: "absolute", path: "/Users/x/p/.endless/tasks/e-7/plan.md",
			ok: true, taskID: 7, content: taskcontent.Plan},

		{name: "legacy plan", path: ".endless/plans/E-2137.md",
			ok: true, taskID: 2137, content: taskcontent.Plan, legacy: true},
		{name: "legacy outcome", path: ".endless/outcomes/E-7.md",
			ok: true, taskID: 7, content: taskcontent.Outcome, legacy: true},
		{name: "legacy analysis — plural directory, singular name",
			path: ".endless/analyses/E-7.md",
			ok:   true, taskID: 7, content: taskcontent.Analysis, legacy: true},

		{name: "reason (E-1531)", path: ".endless/tasks/e-7/reason.md",
			ok: true, taskID: 7, content: taskcontent.Reason},
		{name: "notes (E-1531)", path: ".endless/tasks/e-7/notes.md",
			ok: true, taskID: 7, content: taskcontent.Notes},
		{name: "no legacy directory for a kind born after consolidation",
			path: ".endless/reasons/E-7.md"},

		{name: "decision", path: ".endless/decisions/ED-1550.md",
			ok: true, decisionID: 1550},

		{name: "the task's own suite", path: ".endless/tasks/e-7/verify.sh"},
		{name: "a stray note in the task's directory", path: ".endless/tasks/e-7/scratch.md"},
		{name: "the task's own manifest", path: ".endless/tasks/e-7/verify.toml"},
		{name: "ordinary source", path: "src/foo.md"},
		{name: "uppercase task directory", path: ".endless/tasks/E-7/plan.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, ok := Resolve(tc.path)
			if ok != tc.ok {
				t.Fatalf("Resolve(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if !ok {
				return
			}
			if src.TaskID != tc.taskID || src.DecisionID != tc.decisionID ||
				src.Name != tc.content || src.Legacy != tc.legacy {
				t.Errorf("Resolve(%q) = %+v, want task=%d decision=%d name=%v legacy=%v",
					tc.path, src, tc.taskID, tc.decisionID, tc.content, tc.legacy)
			}
		})
	}
}

// TestPathsRoundTripThroughResolve is the invariant that keeps the writer and
// the reader from drifting apart: every path this package CONSTRUCTS must be a
// path it RECOGNIZES, back to the same id and content name.
func TestPathsRoundTripThroughResolve(t *testing.T) {
	const id = 4242
	for _, kind := range TaskKinds {
		paths := map[string]string{"consolidated": TaskDocPath(id, kind.Stem)}
		if kind.LegacyDir != "" {
			paths["legacy"] = LegacyTaskDocPath(kind, id)
		}
		for label, path := range paths {
			src, ok := Resolve(path)
			if !ok {
				t.Fatalf("%s %s: Resolve(%q) did not recognize a path we built",
					kind.Stem, label, path)
			}
			if src.TaskID != id || src.Name != kind.Name {
				t.Errorf("%s %s: Resolve(%q) = task %d name %v, want task %d name %v",
					kind.Stem, label, path, src.TaskID, src.Name, id, kind.Name)
			}
		}
	}
	src, ok := Resolve(DecisionDocPath(id))
	if !ok || src.DecisionID != id {
		t.Errorf("Resolve(%q) = %+v, %v; want decision %d",
			DecisionDocPath(id), src, ok, id)
	}
}

// TestTaskDirCasing pins the casing, which is the one thing about these paths a
// human gets wrong and a case-insensitive filesystem then hides.
func TestTaskDirCasing(t *testing.T) {
	if got, want := TaskDir(2137), ".endless/tasks/e-2137"; got != want {
		t.Errorf("TaskDir(2137) = %q, want %q", got, want)
	}
}

// TestSubjectsAreIDSpecific pins what keeps two tasks' mirrors from amending
// over each other: canAmend requires the subject to match, and the id is the
// only thing in the subject that differs between them.
func TestSubjectsAreIDSpecific(t *testing.T) {
	a := TaskDocSubject("update", "plan", 1)
	b := TaskDocSubject("update", "plan", 2)
	if a == b {
		t.Fatalf("two tasks share the subject %q", a)
	}
	if got, want := a, "Endless: update plan for E-1"; got != want {
		t.Errorf("TaskDocSubject = %q, want %q", got, want)
	}
	if got, want := DecisionDocSubject("add", 9), "Endless: add decision ED-9"; got != want {
		t.Errorf("DecisionDocSubject = %q, want %q", got, want)
	}
}

// TestIsMirrorPath covers the predicate the orphan-branch check and the
// branch-history cleanup both partition on.
func TestIsMirrorPath(t *testing.T) {
	mirrors := []string{
		".endless/tasks/e-1/plan.md",
		".endless/tasks/e-1/outcome.md",
		".endless/tasks/e-1/analysis.md",
		".endless/tasks/e-1/reason.md",
		".endless/tasks/e-1/notes.md",
		".endless/plans/E-1.md",
		".endless/outcomes/E-1.md",
		".endless/analyses/E-1.md",
		".endless/decisions/ED-1.md",
	}
	for _, p := range mirrors {
		if !IsMirrorPath(p) {
			t.Errorf("IsMirrorPath(%q) = false, want true", p)
		}
	}
	notMirrors := []string{
		".endless/tasks/e-1/verify.sh",
		".endless/tasks/e-1/verify.toml",
		".endless/tasks/_harness.sh",
		".endless/db-ledger/0001.jsonl",
		".endless/verbs.jsonl",
		"src/endless/task_cmd.py",
	}
	for _, p := range notMirrors {
		if IsMirrorPath(p) {
			t.Errorf("IsMirrorPath(%q) = true, want false", p)
		}
	}
}

// TestEveryContentNameIsMirrored pins E-1531's promise for the mirror layer:
// TaskKinds is derived from taskcontent, so every content name has a mirror and
// a recognizer with no edit here — and the recognizer names nothing else.
func TestEveryContentNameIsMirrored(t *testing.T) {
	if len(TaskKinds) != len(taskcontent.All()) {
		t.Fatalf("TaskKinds has %d kinds, taskcontent declares %d", len(TaskKinds), len(taskcontent.All()))
	}
	for _, n := range taskcontent.All() {
		kind, ok := KindByName(n)
		if !ok {
			t.Errorf("no mirror kind for content name %s", n.Slug())
			continue
		}
		if kind.Stem != n.Slug() {
			t.Errorf("%s mirrors to %s.md; the convention is the name's own token", n.Slug(), kind.Stem)
		}
		if !TaskDocRe.MatchString(TaskDocPath(7, kind.Stem)) {
			t.Errorf("TaskDocRe does not recognize %s", TaskDocPath(7, kind.Stem))
		}
	}
}
