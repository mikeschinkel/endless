package templatecmd

import (
	"fmt"
	"strings"
	"testing"
)

// handoffVarsForType is the canonical var map the spawn path and the claim path
// both supply, parameterized by the effective task type (E-1822 added
// `task_type` so the shared handoff/_mechanics partials can branch on it).
func handoffVarsForType(typ string) string {
	return fmt.Sprintf(`{
		"spawned_id": 9999,
		"label_prefix": "E-8888/E-9999",
		"title": "Test task",
		"task_type": %q,
		"worktree_path": "/tmp/wt/e-9999",
		"branch": "task/9999-test",
		"child_count": 0,
		"children_state": "3 ready (3 total)",
		"report_gate": true,
		"bg": false
	}`, typ)
}

// handoffTypes are the five per-type spawn wrappers; the claim wrapper covers
// the same set by branching on task_type instead of forking into five files.
var handoffTypes = []string{"todo", "bugfix", "research", "epic", "brainstorm"}

// TestRender_Claim_ArrivalMechanicsClose pins the claim wrapper's three
// sections (E-1822): the claim-only arrival framing (you were NOT spawned;
// relocate into the worktree; fold your existing planning into the task), the
// shared mechanics partial, and the shared `_close` tail. The arrival framing is
// what a claimed-in session gets that no spawn template carries, so it is the
// part with no other regression guard.
func TestRender_Claim_ArrivalMechanicsClose(t *testing.T) {
	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			out, errOut, err := runRenderInProject(
				t, root, "handoff/claim", handoffVarsForType(typ),
			)
			if err != nil {
				t.Fatalf("render: %v\nstderr: %s", err, errOut)
			}
			wants := []string{
				// Arrival: not a blank-slate spawn.
				"already-running",
				"You were not spawned for it",
				// Arrival: relocate — a spawned session is born in the worktree,
				// a claimed-in one is not.
				"/cd /tmp/wt/e-9999",
				"cwd gate",
				// Arrival: carry forward the planning already in this chat.
				"endless task update E-9999 --text <path> --db main",
				// Mechanics partial.
				"Your worktree is: /tmp/wt/e-9999 (branch task/9999-test)",
				"--db main",
				"one session, one task",
				// Close tail.
				"endless task report E-9999 --draft-file <path> --db main",
				"$FULL",
				"endless worktree check",
			}
			for _, w := range wants {
				if !strings.Contains(out, w) {
					t.Errorf("claim handoff missing %q\n--- output ---\n%s", w, out)
				}
			}
		})
	}
}

// TestRender_Claim_TerminalRulePerType pins that the claim wrapper reaches the
// same per-type terminal-status/artifact rule the matching spawn wrapper does —
// the rule the retrofit case was most often getting wrong (a research task
// driven to `unverified` instead of `completed --outcome`).
func TestRender_Claim_TerminalRulePerType(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{"todo", "--status unverified --db main"},
		{"bugfix", "--status unverified --db main"},
		// E-2016: findings work reports done at the review gate, not at the
		// terminal — `completed` is the user's call once they have read it.
		{"research", "--status unreviewed --outcome-file <path> --db main"},
		{"brainstorm", "--status unreviewed --outcome-file <path> --db main"},
		{"epic", "--status completed --db main"},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			root := projectFixture(t)
			out, errOut, err := runRenderInProject(
				t, root, "handoff/claim", handoffVarsForType(c.typ),
			)
			if err != nil {
				t.Fatalf("render: %v\nstderr: %s", err, errOut)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("claim handoff for %s missing terminal rule %q\n--- output ---\n%s",
					c.typ, c.want, out)
			}
		})
	}
}

// TestRender_MechanicsPartial_SharedBySpawnAndClaim is the anti-drift guarantee
// E-1822 exists to provide: for every task type, the mechanics lines render
// IDENTICALLY from the spawn wrapper and from the claim wrapper, because both
// pull them from handoff/_mechanics.tmpl. If someone edits one rendering's copy
// of a line, this fails — which is the point.
func TestRender_MechanicsPartial_SharedBySpawnAndClaim(t *testing.T) {
	// One representative line per shared partial. The per-type partials
	// (deliverable, terminal) are covered by their own type's expectation.
	shared := map[string][]string{
		"todo": {
			"the fix is `git rebase main` or `git reset --hard main` **in place**",
			"Your worktree is: /tmp/wt/e-9999 (branch task/9999-test) — confirm with `pwd`. Don't edit the main checkout.",
			"Stay focused on E-9999 — one session, one task.",
			"When implementation is done: `endless task update E-9999 --status unverified --db main`",
		},
		"bugfix": {
			"the fix is `git rebase main` or `git reset --hard main` **in place**",
			"Stay focused on E-9999 — one session, one task.",
			"When implementation is done: `endless task update E-9999 --status unverified --db main`",
		},
		"research": {
			"the fix is `git rebase main` or `git reset --hard main` **in place**",
			"Findings are the deliverable.",
			"When findings are ready: `endless task update E-9999 --status unreviewed --outcome-file <path> --db main`",
		},
		"brainstorm": {
			"the fix is `git rebase main` or `git reset --hard main` **in place**",
			"The synthesis is the deliverable.",
			"When the synthesis is ready: `endless task update E-9999 --status unreviewed --outcome-file <path> --db main`",
		},
		"epic": {
			"the fix is `git rebase main` or `git reset --hard main` **in place**",
			"You're the coordinator on this epic.",
			"When all children are `confirmed`/`assumed`: `endless task update E-9999 --status completed --db main`",
		},
	}
	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			vars := handoffVarsForType(typ)

			spawnOut, errOut, err := runRenderInProject(t, root, "handoff/"+typ, vars)
			if err != nil {
				t.Fatalf("render handoff/%s: %v\nstderr: %s", typ, err, errOut)
			}
			claimOut, errOut, err := runRenderInProject(t, root, "handoff/claim", vars)
			if err != nil {
				t.Fatalf("render handoff/claim: %v\nstderr: %s", err, errOut)
			}
			for _, line := range shared[typ] {
				if !strings.Contains(spawnOut, line) {
					t.Errorf("spawn handoff/%s missing shared line %q\n--- output ---\n%s",
						typ, line, spawnOut)
				}
				if !strings.Contains(claimOut, line) {
					t.Errorf("claim handoff missing shared line %q for type %s\n--- output ---\n%s",
						line, typ, claimOut)
				}
			}
		})
	}
}

// TestRender_Claim_UnknownTaskType_FallsBackToTodo mirrors the Python spawn
// path's `_HANDOFF_TYPES` fallback: an absent or unrecognized type renders the
// todo branch rather than erroring or emitting an empty rule.
func TestRender_Claim_UnknownTaskType_FallsBackToTodo(t *testing.T) {
	cases := map[string]string{
		"unknown": handoffVarsForType("chore"),
		"absent": `{"spawned_id":9999,"label_prefix":"E-9999","title":"T",` +
			`"worktree_path":"/w","branch":"b","child_count":0,` +
			`"children_state":"none","report_gate":true,"bg":false}`,
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			root := projectFixture(t)
			out, errOut, err := runRenderInProject(t, root, "handoff/claim", vars)
			if err != nil {
				t.Fatalf("render: %v\nstderr: %s", err, errOut)
			}
			if !strings.Contains(out, "--status unverified --db main") {
				t.Errorf("expected todo terminal rule for %s task_type\n--- output ---\n%s", name, out)
			}
			for _, forbidden := range []string{"Findings are the deliverable", "coordinator on this epic"} {
				if strings.Contains(out, forbidden) {
					t.Errorf("unexpected non-todo branch %q rendered\n--- output ---\n%s", forbidden, out)
				}
			}
		})
	}
}

// TestRender_Claim_EpicCarriesChildrenState verifies the epic-only children
// breakdown reaches a claimed-in coordinator, and does not leak into the other
// types' claim renders.
func TestRender_Claim_EpicCarriesChildrenState(t *testing.T) {
	root := projectFixture(t)
	out, errOut, err := runRenderInProject(t, root, "handoff/claim", handoffVarsForType("epic"))
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "Children: 3 ready (3 total).") {
		t.Errorf("epic claim handoff missing children breakdown\n--- output ---\n%s", out)
	}

	root = projectFixture(t)
	out, errOut, err = runRenderInProject(t, root, "handoff/claim", handoffVarsForType("todo"))
	if err != nil {
		t.Fatalf("render: %v\nstderr: %s", err, errOut)
	}
	if strings.Contains(out, "Children: 3 ready") {
		t.Errorf("todo claim handoff should not carry the epic children breakdown\n--- output ---\n%s", out)
	}
}

// TestRender_Handoff_WorktreeRemovalIsCategorical pins the rule E-2073 exists
// to make unconditional. The old wording — "Don't run `endless worktree
// land`/`drop` without asking" — put a precondition in front of a destructive
// act, and on 2026-08-25 two sessions in ten minutes decided conversational
// text had satisfied it. Landing keeps "ask first" because it is how a task
// normally ends; removal gets no precondition to mis-evaluate.
//
// The assertions run against RENDERED output, for both the spawn wrappers and
// the claim wrapper, so a future template that grows its own copy of the rule
// is covered without anyone remembering to add it here — and so that moving the
// rule between a wrapper and a partial, which E-1947 did, is invisible to the
// guard. The three commands are named individually because a `drop`-only
// prohibition is satisfiable by reaching for `git worktree remove` — the rule
// has to name the outcome.
func TestRender_Handoff_WorktreeRemovalIsCategorical(t *testing.T) {
	// Matched against whitespace-flattened output, so an expectation is the
	// sentence a session reads rather than one template's line breaks.
	wants := []string{
		"NEVER remove a worktree",
		"endless worktree drop",
		"endless worktree reap",
		"git worktree remove",
		"endless worktree land` without asking",
		// Who does it instead, so a session that thinks removal is warranted
		// has somewhere to put that. E-2073 moved this off the spawning
		// session and onto the person running the session, because the hook
		// fires on any Claude session's Bash tool.
		"removal is not an agent's to perform",
		"the person running this session does it themselves",
		"If removal looks warranted, say so once and stop.",
		// The prose describes a gate now, not an appeal to restraint.
		"A PreToolUse hook refuses all four",
		// E-1947's guidance, which shares this partial: the answer to a
		// diverged branch is a rebase or reset IN PLACE, never a removal.
		"the fix is `git rebase main` or `git reset --hard main` **in place**",
	}
	// The precondition form, in both the numbered and the inline phrasing.
	const forbidden = "land`/`drop` without asking"

	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			vars := handoffVarsForType(typ)

			renders := map[string]string{}
			for _, name := range []string{"handoff/" + typ, "handoff/claim"} {
				out, errOut, err := runRenderInProject(t, root, name, vars)
				if err != nil {
					t.Fatalf("render %s: %v\nstderr: %s", name, err, errOut)
				}
				renders[name] = flattenWhitespace(out)
			}

			for name, out := range renders {
				for _, w := range wants {
					if !strings.Contains(out, w) {
						t.Errorf("%s missing %q\n--- output ---\n%s", name, w, out)
					}
				}
				if strings.Contains(out, forbidden) {
					t.Errorf("%s still carries the precondition form %q\n--- output ---\n%s",
						name, forbidden, out)
				}
			}
		})
	}
}

// TestRender_Handoff_DiscoveryBranchesSurviveCompression is E-2120's guard on
// the mid-task discovery rule.
//
// Bullet 1 used to ask whether a finding could be done "inside the work already
// underway" — a topical-membership question, which sessions answered honestly
// and correctly: a stray id in a guide paragraph is not part of `verb update`'s
// subject, so they fell through to "otherwise file it". That reading was given
// as the justification for over-filing more than once. The axis is what the fix
// costs to make and to review; the bound the guide actually protects is a LARGE
// unrelated change, which is a size test.
//
// The branch with no other guard is the ASK. This partial has lost material to
// compression before — it folded docs/guide/tasks.md's numbered tests into
// three bullets and kept neither the framing nor the override clause, and
// nothing failed. So each obligation is pinned individually, against RENDERED
// output for the per-type wrappers and the claim wrapper alike, and the retired
// phrasing is pinned as a negative so a future edit cannot quietly restore the
// purity test.
func TestRender_Handoff_DiscoveryBranchesSurviveCompression(t *testing.T) {
	// Matched against whitespace-flattened output, so an expectation is the
	// sentence a session reads rather than one template's line breaks.
	wants := []string{
		// The axis, and the failure mode named outright.
		"The test is COST and reviewer confusion, not kinship",
		`"not a pure example of this task" is not a reason to file`,
		// The real bound, kept: size, not relatedness.
		"The bound is size — a large or risky unrelated change still splits out",
		// Bullet 1's third obligation. The commit message and the reply are
		// transient; the task row is what anyone reads later.
		"record the grown scope on the task",
		"`endless task update E-9999 --text-file <path> --keep-status --db main`",
		// The branch that routes the residue to the user instead of into
		// `task add`, with the shape that makes the question answerable.
		"ASK me before filing — that call is mine, not yours",
		"the case for filing, the case against, and your recommendation",
		// Filing survives as the answer to that question, still linked back.
		"`--cleans-up E-9999`",
		// ED-1550's framing, which this partial had compressed away: the list
		// is not a menu of equals.
		"Filing is the exception, not the default.",
		// The override, likewise restored. `handoff_close` orders every session
		// to run the project-wide regression, so a red test it did not cause is
		// a situation the handoff creates and must answer.
		"A check you are obliged to run and report — the project-wide regression — outranks all of this",
		"You cannot report that suite green and you cannot leave it red.",
	}
	// The wording that made bullet 1 a kinship test.
	const retired = "inside the work already underway"

	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			vars := handoffVarsForType(typ)

			for _, name := range []string{"handoff/" + typ, "handoff/claim"} {
				out, errOut, err := runRenderInProject(t, root, name, vars)
				if err != nil {
					t.Fatalf("render %s: %v\nstderr: %s", name, err, errOut)
				}
				flat := flattenWhitespace(out)

				if strings.Contains(flat, retired) {
					t.Errorf("%s restored the kinship test %q\n--- output ---\n%s",
						name, retired, flat)
				}
				// A brainstorm files its resolved ideas by design and renders
				// its own branch, so the discovery tests are not addressed to
				// it. The negative above still is.
				if typ == "brainstorm" {
					continue
				}
				for _, w := range wants {
					if !strings.Contains(flat, w) {
						t.Errorf("%s missing %q\n--- output ---\n%s", name, w, flat)
					}
				}
			}
		})
	}
}

// flattenWhitespace collapses every run of whitespace to a single space so an
// expectation can be written as the sentence a session reads, independent of
// where a template happens to wrap it.
func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestRender_Handoff_VerifyPartialNamesTheSuiteLocation is E-2093's guard.
//
// The `handoff_verify` partial said how to RUN a suite and never where the
// FILE goes, so sessions writing one from scratch reached for the retired
// `tests/tasks/e-NNNN-verify.sh` path that git history is still full of.
// `.endless/tasks/CLAUDE.md` says otherwise, but a directory-level CLAUDE.md
// only reaches a session already working in that directory — which is exactly
// what these sessions had not done.
//
// The assertions run against RENDERED output for both the per-type wrapper and
// the claim wrapper, so the sentence is verified where a session actually
// meets it. The negative half matters as much: the three findings types render
// `handoff_nonverify` instead, and telling a research session where its verify
// suite goes would contradict the sentence right beside it saying there is
// nothing to verify.
func TestRender_Handoff_VerifyPartialNamesTheSuiteLocation(t *testing.T) {
	wants := []string{
		"The suite goes at `.endless/tasks/e-9999/verify.sh`",
		"sources the shared harness at `.endless/tasks/_harness.sh`",
		"read `.endless/tasks/CLAUDE.md` before you write it",
	}
	// The path E-2023 retired. A handoff must never lead a session back to it.
	const retired = "tests/tasks/"

	verifyLane := map[string]bool{"todo": true, "bugfix": true}

	for _, typ := range handoffTypes {
		t.Run(typ, func(t *testing.T) {
			root := projectFixture(t)
			vars := handoffVarsForType(typ)

			for _, name := range []string{"handoff/" + typ, "handoff/claim"} {
				out, errOut, err := runRenderInProject(t, root, name, vars)
				if err != nil {
					t.Fatalf("render %s: %v\nstderr: %s", name, err, errOut)
				}
				flat := flattenWhitespace(out)

				if strings.Contains(flat, retired) {
					t.Errorf("%s names the retired suite path %q\n--- output ---\n%s",
						name, retired, flat)
				}
				for _, w := range wants {
					got := strings.Contains(flat, w)
					if verifyLane[typ] && !got {
						t.Errorf("%s missing %q\n--- output ---\n%s", name, w, flat)
					}
					if !verifyLane[typ] && got {
						t.Errorf("%s carries %q, but a %s handoff has nothing to verify\n"+
							"--- output ---\n%s", name, w, typ, flat)
					}
				}
			}
		})
	}
}
