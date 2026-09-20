# Plan — session status renders the blocking graph, so "what next" is readable

## The question it answers

"Of the tasks on my session status, which should I tackle next?" — without
opening each one to find its blockers, and without asking an agent. Ordering the
tasks that have NO blocking relation is deliberately out of scope: the blocking
graph is useful on its own, and the rest is a separate problem to be understood
by living with this first (Mike, 2026-09-19).

## What already exists, and what is wrong with it

`session status --tree` already derives implementation order from the blocks
DAG (`assignByDAG`), so this is a rendering problem, not a new mechanism. Two
faults make that rendering lossy:

- it keeps ONE parent per task — the deepest blocker, ties to the lowest id — so
  a task with two blockers silently loses one;
- `SessionStatusBlockerEdges` requires BOTH endpoints to be in the candidate
  set, so a blocker that is not itself on session status disappears, which is
  precisely the blocker you most need to see.

`session_tasks.do_order` and the `session order` verb that writes it are being
removed by E-2142, which leaves `--tree` DAG-derived only. Nothing here depends
on that landing first, and `--tree` itself is not changed by this task.

## Which tasks appear (decided 2026-09-19; `--tree`'s existing rule is NOT a precedent)

Start from the session status rows, then:

- **Exclude** phase `later`; the viewing session's own task; the spawning task;
  any task in flight; parent rows; hidden rows.
- **Then add** every task that blocks one of the survivors, even when that
  blocker is phase `later` or in flight. An in-flight blocker renders with its
  id **dim** — it is already being handled, and the line exists to say the
  dependent is not yet available.
- A task earns a place by having ANY edge the graph draws: a blocking relation,
  an advisory `precedes`, or a conflict. Conflicts count — a task whose only
  edge is `<>` still appears, or the warning not to run it alongside another
  task silently disappears, which is the opposite of what conflicts are for.
- A task with no edge of any kind does not appear. An empty graph renders
  nothing — no header, no blank line.
- Blockers in a terminal status are already filtered out by the edge query and
  stay filtered: they impose no order. `unverified` and `unreviewed` blockers DO
  appear, because they still block (the blocking semantics in the guide).

This needs a second edge query, or a parameter on the existing one: in-set
target, ANY open blocker, returning the blocker's id, status, phase and
in-flight flag so the renderer can dim correctly.

## Notation

```
E-1813 => E-1814 | E-1994
E-1815 => E-1814
E-1992 => E-1993 => E-1994 => E-1995
E-2142 -> E-2159
```

- `=>` reads "blocks": left MUST finish before right. The heavier arrow carries
  the harder constraint.
- `->` reads "should precede": advisory order, recorded as a relation (below).
  It does not block and does not gate spawnability.
- Task ids bright, punctuation dim.
- `|` separates tasks that share what is on their left and do not block each
  other. **`|` binds tighter than either arrow** (as `*` binds tighter than `+`),
  so `E-1 => E-2 | E-3 => E-4` reads `E-1 => (E-2 | E-3) => E-4` with no
  parentheses and no ambiguity. Making an arrow the tighter operator would
  instead read that line as two separate chains, which is not the intent.
- A `|` group asserts that EVERY member stands in that relation to everything on
  its left and on its right. The renderer may only emit a group when that is
  true — the members share both sets. Where it does not hold, the tasks go on
  separate lines; a group that over-states either side invents an ordering the
  data does not have.
- One line per chain. A task that joins two chains appears on both, and every
  occurrence after its first renders **dim**, so a reader sees a repeat rather
  than a second task.
- `E-2159 <> E-2161` marks a conflict: no ordering between them, but they must
  not run at the same time (below). Deliberately not an arrow — an arrow in both
  directions reads as a cycle, which is the one thing this graph must never show.
- **`<>` is symmetric but NOT transitive, and is therefore never chained.**
  `A <> B` may come from an overlap on one file and `B <> C` from an entirely
  different one, leaving A and C sharing nothing. Writing `A <> B <> C` would be
  read as transitive — because the arrow chains genuinely are, so a reader who
  learned chains from them would infer `A <> C` and be wrong.
- **A mutual-exclusion set is one line**, marked by a leading `<>`:

      <> E-2142 | E-2159 | E-2164

  Read as "no two of these may run concurrently" — every member exclusive with
  every other. This is the shape that actually occurs: several tasks editing one
  file conflict mutually, and pairwise lines cost n(n-1)/2 to say it — 3 lines
  for three tasks, 6 for four, 15 for six — which a reader then has to reassemble
  into the set that was meant.
  Emission: decompose the conflict graph into maximal cliques; render each clique
  of 3 or more as a set line, and any remaining edge pairwise. A non-clique
  (the a.go / b.go case above) therefore renders as pairwise lines, which
  correctly look different from a set. Cliques are ordered by size descending,
  then by lowest member id, for stable output.
- **Line order is derived, not authored**: a topological order over blocks and
  advisory edges together, ties broken by longest chain then lowest id. So the
  first line leads with the task to start on, and that ordering comes from
  recorded relations rather than from a renderer's opinion. Stable between
  refreshes and diffable in tests.
- A cycle in the data is reported as a line naming the tasks involved, never
  silently truncated. The existing DAG walk already guards against
  non-termination.

## The advisory ordering relation

Recommending an order is useless if it lives in prose: a later reader cannot see
it, and neither can this graph. `blocks` is the wrong home for it — "do E-2142
before E-2159" is not "E-2159 cannot start", since landing E-2159 first breaks
nothing, it only converts refusal messages in code E-2142 then deletes. So add
one relation for advisory order.

- Stored type `precedes`, with the inverse view `preceded_by`, in the relation
  table in `task_cmd` alongside the existing nine. Flags `--precedes` /
  `--preceded-by` on `task add`, and the type accepted by `task link`.
- Labels read **"Should precede"** and **"Should follow"** in `task show`, never
  "Blocks", and it sorts below the blocking rows in the display order.
- The guide row states the semantics plainly: advisory only; does not block, does
  not gate spawnability, and a background session may still pick up a task whose
  `preceded_by` has not landed.
- **Why a relation and not a stored sequence**: an integer order rots on every
  land — positions shift for tasks nobody touched — while a pairwise edge rots
  only when one of its two tasks changes. It is also visible on both tasks in
  `task show`, where prose in a plan is not. Which gives the working rule for
  the guide row: when a task changes materially, reconsider its ordering edges.
- Record them sparingly and for durable reasons — wasted work, deleted code —
  not as general opinion.

## Conflicts (`<>`)

Two tasks with no blocking relation can still be unsafe to run at once when they
edit the same files — E-2159 and E-2161 both rewrite parts of the task CLI, with
no relation between them.

- **Derived, never declared.** The source is each task's own worktree: the paths
  changed against its base branch, plus uncommitted changes. A declared list of
  files would be a guess made at plan time and would rot; the worktree already
  knows. Tasks with no worktree contribute nothing and simply have no `<>` line.
- A pair earns `<>` when their changed-path sets intersect AND neither blocks nor
  precedes the other — if either arrow already relates them, the ordering line
  says it.
- **Never shell out to git on the render path.** `session status` runs in the
  monitor's refresh loop; a `git diff` per worktree per refresh is not
  acceptable. Follow the unlanded-verdict cache pattern in `internal/monitor`:
  compute in the jobs runner, cache the path set keyed by the worktree's HEAD
  plus a dirty marker, and have the renderer read only the cache. A missing or
  stale entry means no `<>` for that task — the graph degrades to ordering only,
  which is still correct.

## Where it renders

- Inline in `session status` and `session monitor`, **after the hidden-rows
  footer and before the fault/notification row**.
- Behind `--graph`, which renders the graph alone for the same session.
- In `--json`, as structure — nodes with their dim/in-flight flags, edges, and
  conflict pairs — so an agent reads the data rather than parsing the ASCII.
- The `--agent` rendering carries the graph too: it is information, not chrome.
- Wrapping: a chain longer than the available width wraps at a `->` boundary
  with the continuation indented under the first id; `|` groups are never split
  across lines. Respect the existing column resolution (`--cols`, `$COLUMNS`,
  and the no-tty default).
- The notation is explained in the sessions guide section, and the legend is
  reachable from `--help`: a reader who has never seen `=>`, `->`, `|`, `<>`, a
  leading-`<>` set line, or a dim id must not have to infer them.

## Sequencing

Lands after E-2159 (Mike, 2026-09-19): the sweep converts every refusal site,
and landing it first means this task's new output is born classified rather than
converted afterwards. Advisory, not blocking — E-2164 could land first and
nothing would break. It is recorded here in prose only because `precedes`, the
relation that would hold it, is what this task adds; that edge is the feature's
first real use.

## Verification

- A session whose status has a task blocked by an off-list task renders that
  blocker; before this change `--tree` omitted it entirely.
- A `precedes` edge renders `->`, sorts the line carrying its source first, shows
  as "Should precede"/"Should follow" in `task show`, and does NOT stop
  `task next` offering the successor or a background session claiming it.
- A task with two blockers shows both, on two lines, with the repeat dimmed.
- An in-flight blocker renders dim; a `later` blocker renders normally; a
  terminal blocker does not render.
- A task with no edge of any kind never appears; a session with no edges at all
  renders no graph line and no blank.
- Two tasks related ONLY by a conflict both appear, and the `<>` line renders —
  the case that is invisible if conflicts do not qualify a task for inclusion.
- Two tasks whose worktrees touch a common path render `<>`; adding a blocking
  relation between them replaces the `<>` with an ordering line.
- Three tasks that mutually conflict render as ONE set line, not three pairwise
  lines; a chain-shaped conflict graph (A-B, B-C, no A-C) renders as two pairwise
  lines and never as a set or a chain.
- The render path issues no git subprocess: exercised by rendering with the
  cache empty (no `<>`, no stall) and with it populated.
- `--json` carries the same graph as the text, asserted against one fixture.
- Output is byte-stable across two consecutive renders of unchanged data.
