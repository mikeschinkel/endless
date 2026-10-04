## What this task is

The Agent Folio Format writer, plus the first two commands rendered through it:
`task show` (detail-shaped) and `session status` (list-shaped). This analysis
and the plan carry the design; they are self-contained and cite nothing a reader
cannot open.

The motivating failure is observed, not theoretical: sessions call `task show`,
see that an analysis exists behind a pointer, and proceed without fetching it —
confirmed by asking a session directly. The design invariant that follows is
**every wrong guess must cost tokens, never silent incorrectness**, so the agent
view inlines content rather than pointing at it.

Scope settled with Mike 2026-10-03: the folio writer, `task show`, and
`session status`. Nothing else.

## Three reversals, recorded because earlier sections asserted the opposite

1. **This task does not block E-1504.** It was filed as E-1504's blocker so the
   31 renderings would not be written twice. E-1504 was then narrowed to the
   flag surface alone — rename, `--format`, the two `--json` gaps — and has
   LANDED (`assumed`). The renderings it shed are this task's and E-2141's.

2. **Go, not Python.** An earlier section claimed "only the Python
   implementation is on the path", and the E-2190 fold-in built on it to decide
   that Python renders `session status` and Go grows no agent renderer. Reversed
   2026-10-03. What forced it: children are a TOON part inside a folio, so a
   Go-written folio needs TOON — and a Python-rendered `session status` needs
   TOON too. The literal reading of the two earlier decisions implements TOON
   twice. Both formats now live in Go; Python shells for both views.

   Secondary, and NOT the deciding reason: E-1063 (port the CLI entirely to Go)
   is `urgent`/`underway` and E-894 (move task display reads to Go) is
   `urgent`/`ready`, so a Python renderer would be rewritten shortly. Mike's
   point stands that Endless's porting state must not drive the FORMAT's design;
   it is allowed to drive where Endless writes the code.

3. **Lists are not all in scope.** An earlier section split 31 commands into
   list- and detail-shaped and claimed both halves here. `session status` is in
   scope as the reference list; the remaining list-shaped commands are E-2141's.

## Why these two commands

`task show` is where the observed failure lives, so it is where inlining has to
be proven. `session status` is the hardest list — glyph-heavy, every row
carrying per-viewer state — so the tabular rules get tested where they are most
likely to break rather than on `verb list`.

## Formats: which, where, why

- **Container: Agent Folio Format.** Each part is a header block, a blank line,
  then exactly `Length` bytes. Content parts carry raw bytes, so prose is never
  escaped or indented into a structured envelope. That is the property that
  makes it worth a bespoke format: YAML block scalars solve escaping by
  indenting, and the dedent then has to be correct at every downstream use —
  written to a file, quoted into a handoff, diffed, matched against a fixture —
  where a silent off-by-two breaks a code block rather than erroring.
- **Structured parts: YAML.** `go.yaml.in/yaml/v4`. An earlier draft of this
  plan proposed dropping YAML in favour of TOON everywhere; reversed, because
  TOON's scalar syntax and YAML's are effectively identical for a flat map, so
  the swap saved no bytes and gave up the familiarity of the front-matter idiom
  every agent has already seen. The brief's Decision 5 was right.
- **Tabular parts: TOON.** A TOON library, pinned to an exact version rather
  than hand-rolled: a title containing a comma needs quoting, and three titles
  written in the session that planned this have one.
- **Content parts: raw bytes**, normally `text/markdown`.
- **`--json` is untouched** and remains the exact, lossless, script-facing
  contract.

## Children are their own part, not nested

A `children` part typed `text/toon`, one row per child, columns
`id,type,phase,status,title`, titles untruncated. Relations are not repeated
here — the edges section carries them.

Its own part rather than a nested sequence for a reason Mike raised: a part
declares its own `Type`, so the children format can become `application/json`
or `application/yaml` later with no change to anything that reads the container.
Nested inside the structured part there is no `Type` to switch, and changing it
would change the whole record.

TOON is right here rather than YAML because this is the one place in a detail
record where the tabular saving applies: five column names declared once instead
of five keys repeated per child.

## A record with no content parts still gets a folio

Always the container, never a bare document. A task carrying only a description
pays the `@meta` part and one header block for it; the alternative makes the
command's output shape vary with its data, so one command produces two different
things and a reader has to branch on which arrived.

## Field vocabulary, decided 2026-10-03

The agent view does not inherit the internal JSON's field names. Decided against
the real field set rather than reactively at the third collision, because
renaming a vocabulary that agents and scripts have learned is the expensive kind
of change.

| Internal JSON | Agent view | Why |
|---|---|---|
| `owned_elsewhere`, `duplicate_work`, `owner`, `also_on` | `ownership: {owner, elsewhere, duplicate, also_on}` | Four fields on one concept |
| frame `viewer_session`, frame `focus` | `viewer: {session, focus}` | `viewer_session` is already a compound, and the frame's `focus` is the viewer's, not the board's; cwd, pane or project land here next |
| frame `focal` vs `focus`; row `is_focal` vs `focused` | frame `focal` + `viewer.focus`; row `is_focal` + `is_session_focus` | Two concepts under four names, two of them one letter apart. Keeps "focus", matching `sessions.focus_task_id` and the guide |
| row `unsettled` + `unsettled_known` | `settled: yes \| no \| unknown` | Two booleans encoding a tri-state, an artifact of how E-2128 carried "not computed yet" |
| row `project_id` | `project` (name) | An integer id is unusable to an agent that knows the project by name |
| row `is_parent`, `is_from` | unchanged, flat | Only two, and NOT mutually exclusive — a task can be both parent and spawner (E-1694) — so neither an enum nor a mapping fits |
| row `blocked_by_n`, `blocks_n` | dropped | The edges section carries them |

**The rule, for fields added later:** underscore for a one-off compound; a
prefix repeated across three or more fields is a mapping trying to exist. Two is
a judgment call, three is the signal.

**Dropped from the detail view entirely:** `children_count`,
`children_by_type`, and the `*_chars` sizes. They are artifacts of a
pointer-based view — with children as a part and content inlined, a count is the
row count and a size is visible. The inventory-with-sizes is what `task meta` is
for, which is not this task.

## `session status` specifics (from E-2190, Mike 2026-10-02, amended)

E-2190 is superseded by this task. Its decisions hold except where the Go
reversal above changes them:

1. ~~Python renders it~~ — **reversed, see above.** Go renders it; Python shells
   `endless-go` for the agent view. Go reads the same data
   `session-status --json` exposes.
2. **Every row, flags explicit.** The agent view emits every row the data
   carries, including those the table omits, with the vocabulary above —
   `hidden`, `ownership`, `is_session_focus`, `relation`, `is_focal`, and the
   frame's `focal` and `viewer`. `--show-hidden` / `--only-hidden` do not apply
   to it: they shape a drawing, not data.
3. **`--monitor --agent` is refused**, and the refusal names
   `session status --agent`. The agent view is a snapshot.
4. **Relations as one edges section, no per-row ids.** A table of
   `{from, kind, to}` among the tasks on the board, `kind` one of `blocks`,
   `precedes`, `conflicts` (E-2164's relations, both declared and detected).
   Nothing is repeated per row.
5. **Name the other session.** `ownership.owner` is the owning session's id when
   another live session owns the row; `ownership.also_on` lists the other live
   sessions behind `ownership.duplicate`. This needs ONE Go data change:
   `monitor.AnnotateSessionStatusOwnership` returns ids rather than booleans
   only, and the internal JSON gains them. That is data, not a renderer.

Also carried over: titles untruncated, and every glyph-encoded signal becomes a
field — claimed, focus, duplicate, owned elsewhere, hidden, relation, blocking.

## Not in scope

- **Caller detection** and the counterpart flag that forces the human view. The
  brief's invariant says no correct agent workflow requires a flag, so this is
  needed — but it is a behavioural change touching every hook and script that
  shells `endless`, and it is unfiled. Without it the agent view happens only
  when something passes `--agent`.
- **`task meta`** — the metadata view with the content inventory.
- **The remaining list-shaped commands** — E-2141.
- **A user-facing folio reader.** Filed as a brainstorm; the internal reader
  here is test-only.
- **The human view**, which is unchanged throughout.

## The open question the briefs call decisive

**Does inlined content actually get read?** Unverified. If an inlined analysis
is skipped too, the problem is handoff framing rather than output shape, and the
format work does not help. The brief's test: put a discriminating fact in the
field — something the implementation cannot be correct without and that a
session could not produce from the title — and run the same task shape with it
inlined versus behind a pointer. `task show` being in scope here is what makes
that measurable.


