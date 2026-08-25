# E-1891 — Consolidate the task status vocabulary behind one accessor

Absorbs E-1888 (obsolete). Both were filed as separate bugs during E-1845; they
are two surviving symptoms of one defect.

## The defect

Status is the only vocabulary in the system that never got first-class
treatment. Task type, session kind, and session↔task relation each have a Go
package that owns the list — `tasktype` even carries `String()` and `Label()`
methods. Status is bare string literals scattered across roughly fifteen sites
in two languages, in four shapes:

1. **Copies of the whole vocabulary** — must equal `cli.TASK_STATUSES`.
   `update_plan`'s `valid` tuple, `_VALID_STATUSES`, `web/handlers.go`'s valid
   map, `statusToDisposition`, `_CHILDREN_STATE_ORDER`.
2. **Policy subsets** — deliberately shorter, each encoding a rule.
   `_SUBMITTABLE_FROM`, `_DESCRIPTION_RESET_FROM`, `_REOPENABLE_TERMINAL_STATUSES`,
   `_REOPEN_TO_REVISIT`, `_TERMINAL_STATUSES`, `_RELATION_TERMINAL_STATUSES`,
   `isPreJudgmentStatus`.
3. **Lists embedded in SQL string literals** — the least visible and most
   rot-prone, since nothing reads inside a SQL string. `next_tasks`'s
   `NOT IN (...)`, `GetActiveTasks`, the claim promotion in `monitor/session.go`,
   `web/queries.go`.
4. **Per-status presentation and ordering** — the glyph maps in `task_cmd.py`
   and `web/pages`, `statusLabel()`, and the epic-derivation precedence ladder.

The failure mode is **omission, not typos**: a status gets added and a site gets
missed. E-1648 added `submitted` and updated four of six category-1 sites.
E-1845 added `untriaged` and had to hand-edit every site in all four categories,
with nothing to catch a miss — and the category-4 miss in `epic_derivation.go`
would have silently stopped epics reacting to freshly filed children.

## Two live symptoms (acceptance criteria)

1. `update_plan`'s `valid` tuple omits `submitted`, so
   `endless task update <id> --status submitted` fails with "Invalid status"
   today, even though `submitted` is real and reachable via `task submit`.
2. `_CHILDREN_STATE_ORDER` omits `submitted`, so a submitted child is counted in
   the epic handoff breakdown's `(N total)` but renders no bucket — the
   breakdown does not reconcile, contradicting the tuple's own docstring.

Both must be fixed, but neither is the point. The point is that the next status
added cannot reintroduce them.

## Design — Go owns everything; Python consumes answers, not inputs

Not two mirrored registries: that leaves two places that must agree. Python
already shells out to `endless-go` for the event bridge, template rendering
(E-1565), and `session-status`, so delegation is the house pattern.

```go
package taskstatus

// Status is the machine slug. A plain string alias: every DB read/write and
// SQL literal is already a string, so this adds no conversions at boundaries.
type Status = string

const (
    Untriaged Status = "untriaged"
    Unplanned Status = "unplanned"
    Submitted Status = "submitted"
    // ...one per status
)

// Group names a curated set. EVERY grouping in the system is defined in the one
// map below, so adding a status forces a decision about each of them.
type Group int

const (
    All Group = iota
    SubmittableFrom
    DescriptionResetFrom
    PreJudgment
    ReopenableTerminal
    ReopenToRevisit
    Terminal
    NotActionable
    ChildrenStateOrder
    DerivationPrecedence   // the epic ladder, as ordered data
    // ...
)

var groups = map[Group][]Status{
    All:                  {Untriaged, Unplanned, Submitted, Ready, /* ... */},
    SubmittableFrom:      {Untriaged, Unplanned, Revisit},
    PreJudgment:          {Untriaged, Unplanned},
    DerivationPrecedence: {Underway, Ready, Submitted, Unplanned, Untriaged},
    // ...
}

func Get(g Group) []Status        // defensive copy — callers must not mutate
func Has(g Group, s Status) bool
func SQLList(g Group) string      // "'untriaged','unplanned'" for IN clauses
func Rank(g Group, s Status) int  // index within an ordered group
func Label(s Status) string       // "Untriaged" — mirrors tasktype.Label()
func Glyph(s Status) string       // "◌" — semantic only, no color
```

**Python side: one CLI subcommand per package function, and pure pass-through
wrappers.** Not a JSON payload — even shipping derived output, Python would have
to know which group keys exist, that `sql_list` is a string, that `rank` is a
map. That is structural knowledge of the registry living in two places, and it
drifts the moment a group is added in Go.

```
endless-go task-status get <group>            → one status per line
endless-go task-status has <group> <status>   → exit 0/1
endless-go task-status sql-list <group>       → 'untriaged','unplanned'
endless-go task-status rank <group> <status>  → integer
endless-go task-status label <status>         → Untriaged
endless-go task-status glyph <status>         → ◌
```

Python knows command names and nothing else: no group registry, no payload
shape, no parsing beyond splitting lines. Adding a group in Go requires no
Python change at all, because `get <group>` already accepts it.

Subprocess cost per call is NOT a design constraint here. Endless is a CLI
driven by LLM sessions; a ~15ms spawn is noise against a multi-second turn. An
earlier draft of this plan batched everything into one cached JSON blob to avoid
those spawns, and that premature optimization is precisely what forced the leaky
schema contract above. Do not reintroduce it.

The one shape that looked worth measuring — per-row rendering in
`task list --tree`, a hundred-row tree meaning a hundred spawns — is moot:
that command is slated for removal as unused (E-1899), and its status glyph map
exists only to serve it. Nothing else looks up per row. If some future surface
does, the fix is a bulk variant of that single command (`label --all` emitting
`status<TAB>label` lines, cached in the wrapper), which still mirrors one
function rather than exposing the registry's internals.

## Context: Python is being retired

Endless is to be ported to all-Go, no-Python, as soon as reasonably possible.
That has two consequences for this task:

- **The Go package is permanent; the Python half is transitional.** Build the
  registry properly — it becomes the sole source once Python is gone. Keep the
  Python wrappers deliberately trivial: pure pass-through, no caching, no
  cleverness. That is what makes them both cheap to write now and cheap to
  delete later, and it is a second reason not to reintroduce the JSON-blob
  design, which would be elaborate throwaway work.
- **Doing the Python half is not purely throwaway.** Once a Python call site
  delegates to `endless-go`, porting that function to Go means the status logic
  is already Go-side with nothing to translate. The pass-through reduces port
  work rather than adding to it.

**Decided: convert the Python sites.** The port is wanted as soon as possible
but realistically waits on landing the bulk of pending worktrees, so the interim
is long enough that another status could plausibly be added first — and omission
has already bitten twice in exactly this area. Converting also means each
converted site has nothing left to translate when its turn to be ported comes.
Fix the two live bugs as part of that conversion, not separately.

Verify during implementation that no Python path needs status data when
`endless-go` cannot be resolved; if one exists, decide explicitly whether it
fails closed rather than letting it fail by accident.

## What moves in

- All four categories, including the SQL lists. `SQLList(NotActionable)`
  replacing a hardcoded `NOT IN (...)` is the highest-value single change:
  those literals are invisible to every tool and rot longest.
- `_TERMINAL_STATUSES` and `_RELATION_TERMINAL_STATUSES` collapse into one
  group — byte-identical five-status lists under two names.
- Glyph and label, as `Glyph()`/`Label()` methods, mirroring `tasktype`.
- The epic-derivation ladder as `DerivationPrecedence`, an ordered group. The
  algorithm stays in `epic_derivation.go` and walks the group, returning the
  first status present among the children; the *ordering data* — the part that
  must change when a status is added — lives with every other grouping.

## What stays out, and why

- **Medium-specific color values.** `click.style(fg="yellow")` and
  `bg-slate-400` are not shareable. The registry owns the semantic glyph; each
  surface maps its own palette.
- **Algorithms.** Only their ordering data moves in (see derivation above).
- **The membership rules themselves.** Relocating a subset must not change which
  statuses it contains. Any behavior change is a separate decision.

## Enforcement — none needed beyond the design

No lint rule, no bare-literal grep test. The user path is already input-validated
(`click.Choice`, the `valid` tuple). The code path has one author working under
review. And the failure that actually occurred twice was omission, not a typo —
which the one-map design fixes structurally, because a status cannot be added
without reading every grouping it might belong to.

## Sequencing — one task, two commits

Not split into separate tasks: the Go registry and the Python delegation are a
dependency chain, not independent pieces, so a second task could not start until
the first landed and would have no standalone value. The real concern is blast
radius while worktrees are in flight, which is a commit-sequencing matter:

1. Go package + subcommand + convert all Go call sites. Complete and useful
   alone; Python still duplicates at this point.
2. Python client + convert all Python call sites; delete the duplicates.

## Verification

Split by durability, per E-1889's convention: permanent behavior goes in the
real suites where it lives forever; the verify script covers point-in-time
acceptance only.

**Real suites — permanent invariants:**

- **Go, `taskstatus` package.** Each accessor: `Get` returns the right members
  and a defensive copy (mutating the result must not corrupt the map);
  `Has`; `SQLList` quotes and joins correctly; `Rank` returns the index within
  an ordered group and a sentinel outside it; `Label`/`Glyph` for every status.
  Plus the structural invariant: every group's members are a subset of `All`, so
  a typo'd constant in any grouping fails the build's tests.
- **Go, the subcommands.** Each `endless-go task-status <verb>` emits the
  documented shape and exits non-zero on an unknown group or status.
- **pytest, the wrappers.** Each Python wrapper returns what the Go command
  emitted, holds no group list of its own, and surfaces a clear error when
  `endless-go` cannot be resolved (per the decision recorded above).
- **pytest, the two live bugs, as regressions:**
  `endless task update <id> --status submitted` succeeds; and an epic whose
  children include a `submitted` one produces a children-state breakdown whose
  buckets reconcile with the `(N total)` suffix.

**`tests/tasks/e-1891-verify.sh` — acceptance.** Author it; hand back exactly
one command, `esu && ./tests/tasks/e-1891-verify.sh`. Harness shape copied from
`tests/tasks/e-1845-verify.sh`. It asserts:

- the two live symptoms are fixed, end-to-end through the CLI;
- every converted site reads from the registry — grep that the removed
  duplicates (`update_plan`'s `valid` tuple, `_CHILDREN_STATE_ORDER`,
  `_VALID_STATUSES`, the `web/handlers.go` map, the hardcoded SQL `IN` lists)
  no longer carry inline status literals;
- `_TERMINAL_STATUSES` and `_RELATION_TERMINAL_STATUSES` have collapsed to one;
- a fail-fast section A running `go test ./...` and `uv run pytest tests/ -q`.

## Explicitly NOT in scope

A `task_statuses` lookup table. The code is the source of truth; a table would
only be a convenience copy so SQL can join and enforce a foreign key, and it
needs a migration and backfill across every in-flight worktree. Deferred
deliberately — not filed as a task, tracked by Mike directly.
