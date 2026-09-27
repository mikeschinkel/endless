# Add task_content for typed content on tasks

Strand 1 of E-1991. Storage only.

## What ships, and what deliberately does not

**Ships:** the `task_content` table, the write paths that fill it, a migration
that lifts today's four content columns into rows, and the read paths switched
over. Behaviour is unchanged end to end — the same content, under the same
names, rendered the same way.

**Does not ship:** the vocabulary. Whether names vary by task type, what a
brainstorm calls its plan, which new kinds get added. That question is real and
large; it is also separable, and separating it is what makes this task
shippable. In a row store a new name is an INSERT — so deferring the policy
costs nothing, while deferring the table costs a column per content need.

This split was settled 2026-09-13 and must not be undone on the way through.

## 1. The table

```sql
CREATE TABLE IF NOT EXISTS task_content (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
    UNIQUE(task_id, name)
);
```

`UNIQUE(task_id, name)` is the load-bearing choice: **one row per content kind
per task**, not an append log. It is what keeps the gate in E-1993 a single
question ("is there a `plan` row?") with nothing to disambiguate, and what makes
a write an UPSERT rather than a decision about which row is current.

The E-2155 import (see §7) is the one thing that pushes back on it. Resolve that
there, not by loosening this.

`name` is one lowercase single-token word — the convention this task already
settled, and the same token used by the CLI flag and the mirror filename. The
convention is what is settled; the list is not.

A new migration `internal/schema/migrations/00004_task_content.sql`, plus the
same shape in `internal/schema/schema.sql` — both, because `schema.sql` is exec'd
directly by tests and by anything building from the declared shape.

## 2. Names at rest: today's four, with `outcome` split into two

The columns being retired are `tasks.plan`, `tasks.outcome`, `tasks.analysis`,
`tasks.notes`. `tasks.description` is NOT one of them — it is short metadata and
stays a column, which is what E-1993 caps and E-2109 migrates.

That gives four names carried over unchanged — except `outcome`, which is
carrying two unrelated things and must not be lifted into one row:

- **The deliverable.** On a research or brainstorm task the outcome IS the work
  product: long, authored before the status flips, read by whoever consumes the
  findings.
- **The closing reason.** Why a task ended — the reason `declined`, `obsolete`
  and `superseded` demand. Short, written at the moment of the decision, and
  *about* the work rather than being it.

Different authors, different moments, different readers, and a reader of the
stored row cannot tell which of the two it is holding. That is what the split
fixes: after it, a `reason` row is a closing reason and an `outcome` row is a
deliverable, and neither has to be inspected to find out which.

**Superseded 2026-09-27, and this is the correction that matters here.** An
earlier draft of this section argued the split would let the abandonment guard
HONOUR a stored closing reason instead of re-demanding the flag, and named that
as the payoff. Mike has ruled the opposite: the reason is given again on every
closing move, and a reason already stored on a task never satisfies the guard.
So the guard is unchanged by this task. The split stands on the storage
argument alone — the migration has to decide which name each existing `outcome`
value takes, and one name cannot hold both meanings without the next reader
having to guess.

Which token the deliverable keeps is an implementation call under the
convention. `outcome` + `reason` is the pair. Tokens can be wordsmithed by a
later task if anyone objects — nothing here waits on that.

**This is the only semantic change in the task, and it is deliberate** — the
epic names it as the one thing to get right on the way through, because lifting
the overloading into the new storage would carry it forward permanently. It
belongs here and not in a follow-up: the migration has to decide which name each
existing `outcome` value takes, so the split gets decided in this task whether or
not it is implemented here, and deferring it means a second migration over the
same rows.

## 3. Write paths

Three places write content, and they must agree:

- **`internal/events/executor.go`** — the live path. Its `allowedFields` map,
  in the `task.fields_updated` handler, currently maps each content key to a
  column. It maps them to rows.
- **`internal/events/projector.go`** — the rebuild path. Its own `allowedFields`
  map does the same, plus the `task.created` INSERT and the standalone outcome
  UPDATE. `grep -n allowedFields internal/events/*.go` finds both maps; the two
  other sites are the only places `projector.go` names a content column in SQL.
- **`internal/docmirror`** — the committed `.md` mirrors.

The two field maps are near-duplicates that already disagree (§6). Lifting
content out of both is the moment to make the content half of them one thing.

The legacy `text` → `plan` aliasing (E-1000, 1128 historical events) survives
unchanged: it now resolves to the `plan` *name* instead of the `plan` column.
Both maps carry the same both-keys-present tiebreak and both must keep it.

## 4. docmirror is already name-keyed, and one thing in it is not

`docmirror.TaskKinds` is already a list of `{Column, Stem, Label, LegacyDir}`,
and its own comment says adding a kind there "is the whole change." Retarget
`Column` to `Name` and that stays true.

Except for two regexes — `TaskDocRe` in `docmirror.go` and `taskDocCapture` in
`resolve.go` — which hardcode the alternation `(plan|outcome|analysis)`. **This
is the one place where "a new name is just an INSERT" is false**, because a
source-level alternation cannot know a name that arrived as a row.

They cannot simply widen to `[a-z]+`: the closed alternation is load-bearing,
and the comment says why — `.endless/tasks/e-N/` also holds `verify.sh` and
`verify.toml`, and a pattern matching the whole directory would refuse a
session's own verification suite. Build the alternation from `TaskKinds` at
init instead. That keeps the list closed against `verify.*` while making the one
list that defines it the same list everything else reads.

`notes` joins `TaskKinds` and gains a mirror it never had. Call that out in the
change — it makes a previously invisible field visible in the repo.

## 5. Read paths

Go reads switch to a join. Python reads switch with them — **six files touch
SQLite today (`db.py`, `db_restore.py`, `resolve_name.py`, `task_cmd.py`,
`triage.py`, `cli.py`) and this task must not make it seven**. Every query
selecting `plan`/`outcome`/`analysis`/`notes` off `tasks` is inside those six;
the new join goes in the same place, not in a new module.

`task show --all-fields` renders the same headings in the same order. A reader
should not be able to tell this shipped.

## 6. A rebuild bug this work must fix, not inherit

The two field maps disagree today: the executor accepts `notes`, the projector's
`allowedFields` omits it. `task.created` does not carry it either. So `notes` is
written live and **silently dropped on rebuild** — 73 payloads in the ledger
carry the key, including every research justification E-1544 writes under its
`## Justification` heading. There is no test asserting the two maps agree.

Rebuild is the disaster-recovery path rather than the deploy path, so this has
never been felt. It is squarely in this task's way: §3 rewrites both maps, and
porting the omission across would make it permanent under a name instead of a
column.

Fix it here, and add the parity assertion that would have caught it: the two
maps' content halves are one list, and a test says so.

## 7. E-2155's TSV is the first import, and a stress case

E-2155's refusal inventory is 1136 rows, committed as a TSV under `docs/`
because nothing in Endless stores tabular research. That file is the exception
this table removes.

Do **not** import it in this task. Do check the schema against it while
designing, because it is the case that tests §1's `UNIQUE(task_id, name)`: 1136
records is one content artifact if the rows are the artifact's body, and 1136
content rows if each record is its own. The first fits the unique constraint and
is almost certainly right — the artifact is the inventory, not each line of it.
If designing against it says otherwise, that is a finding worth reporting before
the constraint ships, not a reason to widen it speculatively.

## 8. Explicitly not blocked by E-894

An earlier framing claimed this needed a working ledger rebuild and therefore
the Go port first. Withdrawn. What it needs is the projector taught to write
rows plus an ordinary SQLite migration. The only residual is that rebuild —
already broken, on hold behind E-894 — gains one more thing to learn whenever it
is fixed. §6 is that, and it is a one-line map entry, not a prerequisite.

## 9. Migration

One forward pass: for each of the four columns, insert a row where the column is
non-empty, then drop the column. `outcome` splits per §2 — a row on a research
or brainstorm task becomes the deliverable name; a row on `declined`/`obsolete`/
`superseded` becomes `reason`. Any row that is neither goes to the deliverable
name and is listed in the change, not guessed at silently.

Column drops cannot be expressed safely in SQLite DDL, so per the migrations
package header the drop half is a `.go` step. Land the additive SQL and the
destructive Go step as separate numbered migrations.

## 10. Downstream, for free

`task meta` reports which content fields exist and how large they are — computed
per field over columns, one query over rows. ED-1509's epic content types
(`design`, and possibly `vision`/`roadmap`) become INSERTs rather than a
revisit. E-1562 migrates the `## Justification` heading out of notes and retires
the `--clear justification` stopgap. None of them are in scope here; all of them
stop being blocked.

## Acceptance

- `task_content` exists in both `schema.sql` and a numbered migration, with
  `UNIQUE(task_id, name)`.
- The four columns are gone from `tasks`; `description` is untouched.
- Every existing plan, outcome, analysis and notes value survives the lift and
  is readable through `task show --all-fields`. Headings are generated from the
  content-name enum labels, so they are NOT required to match the old ones —
  freezing display parity across a deliberate semantic change was the wrong
  criterion.
- A stored closing reason and a stored deliverable are separate names. The
  abandonment guard still demands the reason on every closing move and is never
  satisfied by a stored one (ruled 2026-09-27).
- The executor and the projector write the same names from one shared list, and
  a test asserts they agree.
- `notes` survives a rebuild (§6), covered by a test.
- Mirror paths and recognizers are built from `TaskKinds`; no regex names a
  content kind literally; `verify.sh`/`verify.toml` are still not matched.
- Python still touches SQLite in exactly six files.
- `go build/vet/test ./...`, `just test` and `just test-go` pass.


## As built (recorded at handoff)

Decisions taken during implementation, with Mike where noted:

- **Headings come from the names** (Mike). Content names are the
  `taskcontent.Name` Go enum; `String()` is the label, so `reason` renders as
  "Reason". The earlier "readable at its old heading, no visible change"
  acceptance line is withdrawn: a migrated closing reason now shows under
  Reason, not Outcome.
- **Notices stay one line per edit** (Mike). `task_content_notify_*` triggers
  merge their field into the undelivered notice `tasks_notify_sessions` wrote
  for the same task, actor and second; the executor writes the row before its
  content so the merge target exists.
- **Stale mirrors removed** (Mike). The 194 `outcome.md` files whose values
  became `reason` rows are `git rm`'d on the branch; the sweep writes
  `reason.md` on main after land.
- **Overlap in §9's rule settled by status.** Five research/brainstorm tasks
  were also abandoned; each stored outcome read as a closing reason, so status
  wins in the migration and in the projector's routing of legacy `outcome` keys.
- **`--outcome` with an abandonment status is stored as `reason`**, on every
  route (`task update`, `epic update`, `task replace`); without such a status
  it stays the deliverable. `--reason`/`--reason-file` and `--notes`/
  `--notes-file` added to `task update` and `epic update`; `--reason`/`--notes`
  display flags to `task show` and `epic show`.
- **Empty content deletes the row**; `content` is never ''.
- **Migrations are 00006 (additive SQL) and 00007 (Go lift + drop)**, after two
  renumberings: E-2176 took 00004, then its follow-up took 00005, and that second
  collision is what failed the first land. 00007 sets the content triggers aside
  for the copy so the lift writes no notices.
- **§7 finding:** E-2155's TSV is one artifact (the inventory), so it fits
  `UNIQUE(task_id, name)` as a single content row; nothing argued for widening.

Grown scope, folded in because each was cheaper than a task row describing it:
an `abandoned` status group in `internal/taskstatus`; `endless-go task-content
names` for Python; the hook's mirror-refusal text generated from `TaskKinds`;
the guide (tasks, orchestration, index, sessions) and the shipped suite rules
(`SUITE_RULES` and `.endless/tasks/CLAUDE.md`, with Mike's permission) updated
for five mirrors and the reason split; the unused `MIRROR_PATHSPECS` removed.

**Stored reason does not count** (Mike, overriding §2's payoff). Every closing
move must give its reason in the same command; a reason already stored never
satisfies the guard. The split itself stands — it keeps a research task's
findings from being overwritten by why it was abandoned.
