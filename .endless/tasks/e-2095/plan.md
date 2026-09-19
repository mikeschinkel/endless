# E-2095 — Plan

Make it visible whether a task's work has reached the project's default branch,
and whether its record is still authoritative. Four parts, one problem: a reader cannot see the facts that
decide whether to trust a row.

## Scope

**In:** parts 1, 2, 4, 5 of the analysis, plus a data-repair pass (§6).

**Out:** part 3 (record supersession automatically when an excision deletes
another task's landed files). It is blocked on a recording change that does not
exist: `task_landings` stores a merge SHA but no base, so a landing's commit
range cannot be reconstructed, and `task_files` stopped recording 2026-07-11.
Its full spec stays in this task's `--analysis`; file a successor when the
recording change is actually wanted rather than parking a speculative task in
the backlog (ED-1550 rule 1).

**Depends on E-2087**, which has landed. It shipped the content-based probe this
task consumes — do not write a second one.

**Blocked by E-2108** (`unverified`). It renames worktree branches to
`task/<id>` and retires `task_landings.branch`. The block is real, not
bookkeeping: this plan says to DERIVE a branch name from the task id, and that
derivation is false until E-2108 lands — today's branches still carry a title
slug. Landing E-2095 first would mean either deriving a name that does not
exist, or reading a column about to be removed. Either way it breaks on the
other side of E-2108.

## Decisions already taken (do not relitigate)

- Landedness is measured by CONTENT, not SHA. E-2087's `unlandedCommits`
  short-circuits on `countRevs` when HEAD is an ancestor of base, and only then
  runs `git range-diff`. Reuse it; never reach for `git cherry`.
- Branch names are DERIVED from the task id, never read from a column. E-2108
  retires `task_landings.branch`; anything here that reads it lands broken.
- The base branch is RESOLVED, never assumed. No surface in this task may write
  the word `main` into a message, a query or a rev-range. Call
  `monitor.DefaultBranch` (Go) or `worktree_cmd._default_base_branch` (Python),
  which E-1940 built for exactly this after two probes hardcoded `main` and
  "exit 128 forever" on any repo that used another name. `unlandedCommits`
  already takes `base` as a parameter, so the probe is clean — the risk is
  reintroducing the bug in the RENDERING, which is where this task lives.
- No date-scoped filter anywhere. An earlier draft proposed excluding tasks
  created before the project's first landing record. That is Endless's own
  biography, not product behaviour, and it would have hidden real unlanded work.
  §2's two sections replace it.
- New verb, not a flag. `task landed --not-on-main` reads as a contradiction.
- Part 4's banner follows E-2097's shipped shape exactly.

---

## 1. `task show` states landedness instead of implying it

Today the `Landed:` line renders only when a `task_landings` row exists, so
"landed before landings were recorded" and "the branch still holds the work"
both render as nothing. Absence is doing work it cannot do.

For a task in a terminal status, always render a landing line, one of:

```
Landed:     2026-08-30 10:16 pm  421726c          (40)
Landed:     never                                 (17)
Landed:     never — 320 unlanded commits          (42)
Landed:     unknown — base branch unresolved      (46)
```

**Hard budget: 60 characters including the label.** Not a guideline. These lines
are read in split panes, and a status field that wraps looks broken in a way a
long TITLE does not — a title is user content and obviously continues, a field
value is not. Every form above is at or under 46. Any future case that cannot
fit sheds detail to a drill-down command rather than wrapping.

This line is a SUMMARY and must never wrap. It carries neither the branch name
nor the base branch, and the reason is NOT length:

- **The branch name is derivable from the id.** E-2108 (`unverified`, implements
  ED-1587) renames worktree branches to `task/<id>` with no title slug, "so a
  branch name is derivable from the id instead of looked up", and RETIRES
  `task_landings.branch`. Once that lands, printing the branch in a task's own
  detail view repeats the `ID:` line three rows above it. Do not read
  `task_landings.branch` and do not render a branch name here; derive it where
  one is needed. The full breakdown — `Branch:`, `Worktree:`, verdict, and the
  commits themselves — already belongs to `task unsettled <id>`.

  (Length reinforces this but does not decide it. Under today's slug branches
  the long form is 103 characters and wraps; under E-2108's convention it is 68
  and fits. The redundancy argument holds either way, which is why it is the one
  stated here.)
- **The base branch is named once per report, never per row.** `task unsettled`
  already sets the precedent with its `Unlanded — N commit(s) not on <base>`
  section header. §2's report does the same in its header, which is what keeps
  the claim falsifiable without paying for the name on every line.
- **`unlanded` is the load-bearing word** and it is a defined term as of E-2087:
  a commit with no counterpart on the resolved base. It carries "not on the base
  branch" without spelling it, which is why the short form loses no meaning.

`never` stays in BOTH negative cases on purpose, rather than being dropped from
one. It is the VALUE of the field; the clause after the dash is a qualifier on
it. A reader scanning a column of these matches one word to learn the verdict
and only then reads the detail — and the two cases must not look like different
verdicts when they are the same one.

Cost: the third case needs the probe, the first two do not. E-2087's
short-circuit makes the common case a `rev-list --count`; only a branch that
genuinely differs pays for `range-diff`. Do NOT add an opt-in flag to request
the probe — an opt-in probe is one nobody remembers to pass.

Edge cases that must render distinctly rather than collapsing into "clean":

- **No branch** (reaped, or never created). The probe cannot run. Say "no
  landing recorded", never "nothing outstanding".
- **Base branch unresolved.** `DefaultBranch` returns
  `ErrDefaultBranchUnresolved` rather than guessing, because "substituting
  `main` here is exactly the bug this resolver exists to remove". Render that as
  its own state — the landedness of this task is unknown, not clean — and name
  the remedy: set `default_branch` in the project's `.endless/config.json`.
- **Probe failure.** `unlandedCommits` returns a `gitProbeError` naming the git
  command. Surface that, do not swallow it into a clean reading.
- **Uncommitted changes are a different axis** and belong to `task unsettled`.
  This line reports commits only.

## 2. `endless task unlanded`

A sibling verb to `task landed`, sharing one implementation.

**Wiring.** Put the query in `task_cmd.py` beside `landed_list`, and register
two Click commands that differ only in the mode they pass. Bundle the shared
options with a decorator the way `rowcap.limit_options_for` already does — its
own docstring gives the reason: "One factory so the two flags cannot appear on
one command and not another, and so their help text is written once." E-967's
`task_cmd.add_command(task_show, name="detail")` is the precedent for a second
name over one callback. Nothing is duplicated; there is no Click obstacle.

**Two sections, because they need different actions.** Measured 2026-09-06:
109 shipped-terminal tasks have no landing row, 3 have a branch at all, and 1
holds anything main lacks.

- **Unlanded work** — a branch exists and the probe reports commits the base
  branch lacks. Actionable: land it, or reset the branch in place. This is the
  list that should be driven to zero.
- **No landing record** — no branch, or the branch holds nothing. Nothing to
  land; the record is missing. Not actionable by landing.

Rank the first section above the second and label both. Collapsing them into one
count would report ~109 outstanding items when 1 is actionable.

**Help text must contrast with its neighbour.** `task unlanded` and
`task unsettled` sit adjacent and look alike. Each opens by naming the
difference: `unsettled` asks whether a WORKTREE is modified or unlanded;
`unlanded` asks whether a TASK claims to be done while its work has not reached
the base branch.

**Reuse `--project`, `--all`, `--llm`, `--json` and the row cap.** Match
`task landed`'s option set exactly — that is the point of the shared decorator.

## 3. `project status` claim

One attention claim, counting the **first** section only. The second section is
a standing historical count and would become permanent furniture.

```
 ⊘ 3 unlanded (Run: task unlanded)
```

Glyph and one-word label join the legend beside the existing claims. Suppress
the row entirely at zero.

Keep this coupling THIN. `project status` is heading for major revisions in
another session; specify the claim (count, glyph, drill-down verb) and leave the
rendering to that work rather than designing a row about to be replaced.

## 4. The authority banner

`task show`, `decision show` and their siblings lead with a banner when the
record is not straightforwardly authoritative. The status line is already on
screen and is demonstrably not enough — it is read as provenance, not caveat.

**Triggers.** Task: `obsolete`, `declined`, or carrying a `replaced_by` or
`duplicated_by` relation. Decision: `rejected`, `superseded`, or `proposed`.
Current volume: of 1391 tasks, 270 are obsolete/declined, 84 replaced, 3
duplicated; of 179 decisions, 124 accepted, 36 proposed, 18 rejected, 1
superseded. Roughly one record in four triggers, so three in four stay clean and
the banner keeps meaning something.

**Two kinds, named as such.** "Not ever" (obsolete, declined, rejected,
replaced, superseded) and "not yet" (proposed). The wording must distinguish
them; a proposed decision is not a retired one.

**Shape follows E-2097, which has shipped.** Its rule: emit ONE dense line as
both the FIRST and LAST line, identical, not split, so whichever end survives a
`head -N` or `tail -N` is sufficient alone.

**Audience: agent-facing only.** Gate the whole banner on
`agent_help.agent_facing()`, which is `agent_env.present() or _AGENT_VIEW` — an
agent is reading, or a human passed `--agent-view` to see what one sees. Use
that function; do not re-spell the question. E-1966, E-2006 and E-2097 each
folded a separate spelling of it back into one place, and a fourth would undo
that.

A human running `task show` sees NOTHING new. The banner is not a caveat a human
needs — the status line and the `Replaced by:` link are already on screen and a
person reads them as the qualifiers they are. It exists because an agent does
not: it quotes the row as authoritative. So the banner is agent output, and a
human reaches it only by asking, with `--agent-view`, for debugging.

This is stricter than E-2097's split, and deliberately. There, the underlying
message is a REFUSAL that a human must see or the command fails silently, so
only the bracketing is agent-gated. Here there is no underlying message a human
is owed — the banner is additive — so the whole thing is gated.

Machine formats: `--json` carries the fact as a key rather than a repeated line,
since nothing truncates JSON by lines. `--llm` gets the bracketed form.

## 5. Guide

One paragraph next to §1's rendering, in the tasks section: a task status
answers "what did we decide about this work", never "where is the code". Point
at `task unlanded` for the latter, and at `task unsettled` for worktree state.

## 6. Data repair, same session

Fold this in rather than filing it (ED-1550 rule 2). Of the 109 shipped-terminal
tasks with no landing row, **20 have a commit on main attributed to them** and
should carry a landing record; 89 have no identifiable SHA and stay as they are.

Record each with `worktree land <id> --record-only --sha <sha>`.

**Verify every SHA before recording it.** The attribution came from grepping
commit messages, which over-credits: E-1048 was credited to a commit that merely
mentions it in the body while implementing E-1047. The bar is the one E-1150
met — its own description named the commit, and that commit is on main. A SHA
that cannot be verified to that standard is left unrecorded. Writing a
heuristic SHA into the table this report reads is the failure the report exists
to prevent.

This shrinks §2's second section. It does not affect the first.

## Verification

- `task show` on a terminal task with a landing row, without one and without a
  branch, and without one but with a branch holding commits — three distinct
  lines, no case rendering as blank.
- `task show` on a task whose branch is gone, and one whose probe errors —
  neither reads as clean.
- `task unlanded` sections ranked correctly, and `task unlanded --json` /
  `--llm` carrying the same split.
- `task unlanded --help` and `task unsettled --help` each name the other.
- On a repo whose default branch is NOT `main`, every rendering names that
  branch and none reports a false all-clear. `git grep -n '"main"'` over the
  code this task touches returns nothing.
- Banner appears on an obsolete task, a replaced task, a proposed decision, and
  a rejected decision; is absent on an accepted decision and a confirmed task;
  survives both `| head -3` and `| tail -3`.
- After §6, the 20 tasks appear under `task landed` and have left `task
  unlanded`'s second section.



---

## As landed — three departures from the plan above

**The E-2108 block dissolved rather than being worked around.** E-2108 landed
while this session was reading, so branches really are `task/<id>` and the
derivation this plan rests on is true. `worktree_cmd.task_branch(id)` is the one
construction site; nothing here reads a branch from a column or from git.

**§3 is SPECIFIED, not built** (Mike's call, asked and answered mid-session).
`project status`'s board deliberately carries no landedness marker today, and
computing the claim means a git probe per candidate task on a live-refreshing
view. The claim — count semantics (first section only), `⊘`, `task unlanded` as
the drill-down, suppressed at zero, and the measured cost — is recorded in
`internal/projectstatuscmd/board.go` beside the comment that currently explains
the marker's absence, which is where the session revising that board will read
it. No task was filed.

**§6 recorded ONE landing, not twenty.** The plan's figure came from grepping
commit messages, and the plan warned in the same breath that the grep
over-credits. Held to the bar the plan set — the commit's own subject names the
task, and the commit is on the base branch — exactly one of 129 candidates
qualifies: E-1186 at `e03b74e2`, whose subject is "Update E-971 plan: … (E-1186)"
and whose diff is the plan edit E-1186's own text describes. It is recorded.
Nothing else clears the bar: two candidates name a SHA in their own fields and
both point at another task's commit (E-1048 → E-787's, the over-credit the plan
names by example), and zero appear in a `Merge branch 'task/<id>'` subject. The
second section of `task unlanded` therefore stands at 122, not ~102.

**Also folded in, cheaper than the row describing it would be:** a
`shipped-terminal` group in the Go status registry (the population both surfaces
ask about, which no existing group named), and a `.endless/`-confined-commit
filter in the probe — without it the report lists one row per task branch in the
project (measured: 61 branches versus 3 holding source).
