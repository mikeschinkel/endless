## The defect

`sessions.state` carries four values — `working`, `idle`, `needs_input`, `ended`
— and nothing owns them. They are spelled as string literals across roughly 70
non-test sites in three languages: Go (`internal/monitor`, `internal/hookcmd`,
`internal/projectstatuscmd`), Python (`src/endless/cli.py`,
`src/endless/session_cmd.py`), and SQL embedded in both.

This is precisely the defect E-1891 fixed for task status, left unfixed here.
`src/endless/statuses.py` records what it cost there: status lived as bare
literals across ~20 sites in two languages, and twice — E-1648's `submitted`,
E-1845's `untriaged` — a status was added and a site was missed.

Measured distribution of the SQL predicates:

- `state != 'ended'` — 29 sites. By an order of magnitude the most common thing
  anyone asks about a session.
- `state = 'ended'` — 21 sites.
- single-state reads and writes — the remainder.

## What made it urgent

E-1976 added a project attention board whose loudest rank is "a session is
waiting on you". Building its follow-up (E-2091) I proposed routing Claude
Code's permission-prompt Notification to `needs_input`, on the evidence that no
live transition ever writes that state.

That was wrong in a way a grep does not reveal. `needs_input` is enforced:
`hookcmd.sessionMayWrite` refuses file writes from a session in that state under
`enforce` tracking, with the message "you asked your user something and the
answer has not arrived. There is no command for you to run." Routing permission
prompts there would have refused the session's next write after the user
approved.

The failure is structural, not a lapse of care. The gate's admission set is a Go
`switch` with a silent `default: return false`. A new state joins the refused set
by default and says nothing. Nothing in the code makes adding a state a decision
about whether it may write.

## Why groups, and why these five

Each of the five is a predicate already in the tree, not a speculation. `Live`
retires the 29-site majority. `MayWrite` is the one that pays for the exercise:
once the gate reads a named group, a state cannot be added without someone
classifying it, which is the forcing function that would have caught the
collision above. `AwaitsHuman` is the group the board's attention rank expresses,
and drawing it now is what keeps E-2091 to one row. `DisplayOrder` and `All`
carry `session list`'s sort and the vocabulary itself.

This follows `internal/taskstatus`'s governing property: adding a Group is one
row; adding a Status means revisiting every row.

## Why a transition table but no generated diagram

Session state has been stable where task status has not. Twelve session-related
schema change files exist and not one touches `state`'s value set — sessions have
churned through short ids, nullable session_id, active epic, kind, gates added
and dropped, transcript path dropped, recap columns dropped, and the four values
held throughout. The live database has exactly four distinct values ever written.
Task status gained three members over a comparable stretch.

The two are different kinds of vocabulary. Task status models a human approval
workflow, which keeps acquiring distinctions people find worth drawing; session
state models a process lifecycle written by hooks, bounded by what a harness can
report. Realistic growth is one member (E-2091's fifth), plausibly a second for
stall detection.

So `taskstatus`'s generated-mermaid machinery — the artifact, the regeneration
recipe, the drift check, the three-copy sync test — is not justified here. It
earns its keep against thirteen states with per-task-type edges people
misremember. Four hook-written states do not have that problem.

The transition TABLE is justified on different grounds: its `trigger` column
names which code performs each write. "What actually writes `needs_input`?" is
the question that produced the wrong proposal above, and it stays expensive at
four states or six, because it is a question about writers scattered across two
languages rather than about how many values exist.

## Decisions taken with Mike (2026-09-01)

1. Pure refactor. No state is added here; E-2091 adds the fifth. The verification
   gate is behaviour byte-identical before and after, which is the only cheap way
   to prove a 70-site conversion broke nothing.
2. Five groups: All, Live, MayWrite, AwaitsHuman, DisplayOrder.
3. Transition table as data plus a test. No generated artifact.
4. Python converts now, not at the all-Go port. If Python keeps its own copy,
   E-2091's fifth state is rejected by `--state`'s Click choice and renders as
   the unknown glyph — the exact missed-site incident this pattern exists to
   prevent, and it would defeat the point of doing this task first.
