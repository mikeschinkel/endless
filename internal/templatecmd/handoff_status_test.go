package templatecmd

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// statusFlag captures every `--status <slug>` a rendered handoff instructs a
// session to run.
var statusFlag = regexp.MustCompile(`--status ([a-z]+)`)

// handoffTemplates are every wrapper a session can be handed: the five per-type
// spawn variants plus the claim variant, which branches on task_type rather
// than forking into five files.
var handoffTemplates = []string{
	"handoff/todo", "handoff/bugfix", "handoff/research",
	"handoff/epic", "handoff/brainstorm", "handoff/claim",
}

// TestHandoffStatusInstructionsAreLegalTransitions is the guard E-2016 needed
// and did not have.
//
// The handoff is the only lifecycle documentation a spawned session actually
// obeys — it is pasted into the session's first turn, and it names the exact
// command to finish with. When E-2016 moved research and brainstorm behind
// `unreviewed`, the transition table changed and the templates did not, so
// every spawned research session was told to run a command the executor now
// refuses. Nothing caught it: the templates are prose to the compiler, and a
// status slug inside one is invisible to the registry that E-1891 built
// precisely so status literals could not hide.
//
// This closes that. Every `--status X` a handoff prints must be an edge the
// table actually has for that task type, walked from `underway` — the status a
// session holds while reading the handoff. A future status change breaks here
// rather than in a spawned session's face.
func TestHandoffStatusInstructionsAreLegalTransitions(t *testing.T) {
	for _, tmpl := range handoffTemplates {
		for _, typ := range handoffTypes {
			t.Run(strings.TrimPrefix(tmpl, "handoff/")+"/"+typ, func(t *testing.T) {
				root := projectFixture(t)
				out, errOut, err := runRenderInProject(t, root, tmpl, handoffVarsForType(typ))
				if err != nil {
					t.Fatalf("render: %v\nstderr: %s", err, errOut)
				}

				tt, err := tasktype.Parse(typ)
				if err != nil {
					t.Fatalf("parse task type %q: %v", typ, err)
				}

				seen := map[string]bool{}
				for _, m := range statusFlag.FindAllStringSubmatch(out, -1) {
					status := m[1]
					if seen[status] {
						continue
					}
					seen[status] = true

					if !taskstatus.Valid(status) {
						t.Errorf("handoff instructs `--status %s`, which is not a status at all", status)
						continue
					}
					if !taskstatus.TransitionAllowed(taskstatus.Underway, status, tt) {
						t.Errorf("handoff instructs `--status %s`, which a %s task cannot reach "+
							"from `underway` — the executor will refuse it. Reachable: %s",
							status, typ,
							strings.Join(taskstatus.ReachableFrom(taskstatus.Underway, tt), ", "))
					}
				}
			})
		}
	}
}

// TestFindingsHandoffsNameTheReviewGate pins the positive half: the two types
// E-2016 routes through `unreviewed` are actually TOLD to use it, and are told
// not to set `completed` themselves. A template that simply dropped its status
// instruction would pass the legality test above while leaving the session with
// no idea how to finish.
func TestFindingsHandoffsNameTheReviewGate(t *testing.T) {
	for _, typ := range []string{"research", "brainstorm"} {
		for _, tmpl := range []string{"handoff/" + typ, "handoff/claim"} {
			t.Run(strings.TrimPrefix(tmpl, "handoff/")+"/"+typ, func(t *testing.T) {
				root := projectFixture(t)
				out, errOut, err := runRenderInProject(t, root, tmpl, handoffVarsForType(typ))
				if err != nil {
					t.Fatalf("render: %v\nstderr: %s", err, errOut)
				}
				if !strings.Contains(out, "--status unreviewed") {
					t.Errorf("a %s handoff never names `--status unreviewed`, so the session "+
						"has no legal way to report done\n--- output ---\n%s", typ, out)
				}
				if strings.Contains(out, "--status completed") {
					t.Errorf("a %s handoff still instructs `--status completed`, which the "+
						"executor refuses — that is E-2016's defect", typ)
				}
				if !strings.Contains(out, "Don't mark `completed` yourself") {
					t.Errorf("a %s handoff does not tell the session to leave `completed` to "+
						"its user, which is the behavior the gate exists to produce", typ)
				}
			})
		}
	}
}

// TestEveryTypeHandoffNamesSomeTerminalInstruction guards the gap both tests
// above would otherwise leave open for the OTHER types: a wrapper that names no
// status at all tells its session nothing about how to finish.
func TestEveryTypeHandoffNamesSomeTerminalInstruction(t *testing.T) {
	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			out, errOut, err := runRenderInProject(t, root, "handoff/"+typ, handoffVarsForType(typ))
			if err != nil {
				t.Fatalf("render: %v\nstderr: %s", err, errOut)
			}
			var found []string
			for _, m := range statusFlag.FindAllStringSubmatch(out, -1) {
				found = append(found, m[1])
			}
			sort.Strings(found)
			if len(found) == 0 {
				t.Errorf("the %s handoff names no `--status` at all", typ)
			}
		})
	}
}
