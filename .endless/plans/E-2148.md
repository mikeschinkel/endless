# E-2148 — Rework the errors surface

Seven problems, one surface. Six passes, each landing on its own under this id.
Ordered so no pass rewrites what a later one is about to move.

## Decided with Mike, 2026-09-18

**Codes carry severity.** `WARN-NNNN` for warning codes, `ERR-NNNN` for error
codes. Numbers do NOT change: `ERR-0001` becomes `WARN-0001`, `ERR-0002` stays
`ERR-0002`. Six of the twelve change prefix. Numbers stay spent, and every
incident already recorded in a user's database keeps its number.

**Compartmentalized by default.** A listing shows the project you are standing
in. Mike: "generally when I am in a project I don't want other project's
concerns leaking in." The fix for the contradiction that surfaced this task is
therefore NOT to widen the listing and NOT to narrow the fault row — it is to
stop the listing saying "no errors" when it means "none here".

**An id you type is honoured.** `errors show <id>` ignores scope, as `--id`
does today: refusing to show a row because it belongs to another project would
make `show 7` fail immediately after a listing displayed row 7.

**Both verbs name the project.** `list` as a metadata header above the table,
not a column repeating one value; `show <id>` as a field of the incident.

**No SEVERITY column.** Once the code says `WARN-`, a severity column and a
severity icon beside it both repeat it. The column goes; the icons are spent on
the fault row instead, which is what prompted them.

**No ASCII fallback.** 382 source files already emit non-ASCII UI glyphs, so a
fallback in one column buys nothing while the rest of the UI assumes UTF-8. The
real hazard is WIDTH, handled in Pass 5.

## Not in scope

The per-code remedy TEXT already exists in docs/errors.md and is asserted
complete by the faults package's own tests. This task surfaces it; it does not
rewrite it.

---

## Pass 1 — rename `faultbadge` to `faultrow`

Pure rename, no behaviour change, FIRST so every later pass writes the new name
instead of churning the same files twice.

- `internal/faultbadge` → `internal/faultrow`; `badgeLine`, `badgeText`,
  `badgeCounts`, `badgeReset`, `rowStyle`/`chipStyle` follow.
- Prose becomes "fault row" throughout: `docs/errors.md`,
  `docs/guide/reference.md`, and every comment on the surface.
- `Hint` and the `eeh` helper are unchanged — the command is not being renamed.

Verifies: `go build ./...` and the existing faultrow tests pass unchanged; no
tracked file outside `.endless/` still contains "faultbadge" or, in docs, the
word "badge".

## Pass 2 — severity-keyed codes

- `internal/faults/codes.go`: re-prefix the six warning codes. `LookupCode`
  keeps resolving by exact id.
- `internal/schema/changes/e-2148-severity-keyed-fault-codes.sql`: rewrite
  `errors.code` for the six, so incidents recorded before the change still
  resolve to a catalog entry. Idempotent — six UPDATEs keyed on the old id.
- `docs/errors.md`: retitle the six sections and update every cross-reference.
- The detail log (`errors.jsonl`) is NOT rewritten. It is an append-only
  capture, machine-local, never replayed; a historical line saying `ERR-0001` is
  a record of what the code was called then.

Verifies: every `Code.ID` prefix matches its `Severity`, asserted in a test over
`faults.Codes()` so a future code cannot be added with the wrong one. The change
script run twice leaves the same rows.

## Pass 3 — `list` vs `show`

- `errors list` becomes the listing verb. `errors show <id>` becomes the detail
  view, taking a positional id; `--id N` stays as a hidden alias so existing
  muscle memory and any scripted caller keep working.
- `errors show` with no id is a usage error naming `errors list`, not a listing.
- The detail view renders one incident whole: code, severity, project, source,
  counts, first/last seen, the FULL summary (the listing's truncation is what
  makes this view necessary), and its occurrences under `--detail`.
- Python CLI parity: `tests/test_go_cli_parity.py` already requires every Go
  verb to be reachable from Click, so `list` must be added there in the same
  pass.

Verifies: `errors list` and `errors show <id>` both reachable from the Python
CLI; a summary long enough to truncate in the listing appears whole in `show`.

## Pass 4 — scope that cannot contradict itself

- A listing prints the project it is scoped to as a header line above the
  table, and drops the PROJECT column while scoped. `--all-projects` restores
  the column and says so in the header.
- The empty case never prints a bare "no errors". Scoped and empty, with
  incidents open elsewhere, it names the count and the way to see them:
  `no errors in endless — 2 elsewhere (endless errors list --all-projects)`.
  Scoped and empty with nothing anywhere: `no errors in endless`.
- `errors show <id>` names the incident's project as a field.

Verifies: with incidents in another project only, a scoped listing names the
count elsewhere rather than reporting nothing — the exact contradiction that
surfaced this task.

## Pass 5 — the fault row

- The severity word leaves the row. The code carries it, and the reversed
  background still colours by severity. That reclaims the nine columns
  `" WARNING "` cost.
- One open incident: code, then summary. As today, minus the chip.
- More than one: a severity tally in icons, then the distinct codes, most
  severe first — `✕2 ⚠1  ERR-0002 ERR-0011 WARN-0004`. Duplicate codes
  collapse; the tally already carries the count. No summary: with several open,
  naming one summary reads as the whole story when it is a fraction of it.
- Degradation as the row narrows: hint first (reserved, as today), then codes
  truncate to `+N more` — never an ellipsis mid-code — then the tally alone,
  then nothing.
- Glyphs must be single-width. `internal/errorscmd` renders through
  `text/tabwriter`, which measures cells in RUNES, so a double-width glyph
  counts as one and shifts every column after it. The fault row already
  measures with `runewidth`; the listing must too.

Verifies: the existing width sweep (every width 1..240, colour and not, wide
runes included) extended to the multi-incident and icon cases — printed width
never exceeds cols-1, never wraps. A row with two incidents naming the same
code shows it once.

## Pass 6 — remedies

- Each catalog entry gains its remedy, sourced from the "What to do" section
  `docs/errors.md` already carries per code.
- `errors show <id>` prints it. The listing does not — it has no width for it,
  and Pass 3 is what gives a reader somewhere to go.
- The footer keeps naming `errors clear`, and keeps saying dismissing is not
  retrying.

Verifies: every code in `faults.Codes()` has a non-empty remedy, asserted over
the catalog so a new code cannot ship without one.

---

## Sequencing note

Pass 2 must precede Pass 5: the row can only drop the severity word once the
code carries it. Pass 3 must precede Passes 4 and 6: both need a detail view to
point at. Pass 1 is first because a rename is cheapest against a tree nobody is
mid-edit on.

---

## As landed, 2026-09-19 — where the work differed from the plan above

All six passes landed, one commit each. What follows is what the plan did not
say, recorded so a reviewer is not reading the diff against a stale spec.

**Seven codes changed prefix, not six of twelve.** The catalog had grown to
fourteen entries by the time the passes ran (E-2145 added ERR-0013/0014). Seven
are severity `warning`. Numbers still did not move.

**Where "(cleared)" went.** The plan dropped the SEVERITY column without saying
where the `(cleared)` suffix it carried should go. It is now a STATUS column
reading `open` / `cleared`, present only under `--all` — the only mode that can
produce a cleared row. This was a gap in the plan, decided rather than asked
about, because it is a small and reversible presentation detail and the
alternative was blocking the whole pass on it. Worth a look.

**The listing's layout is a column-shedding renderer, not a tweak.** Pass 5's
one line — "the listing must [measure with runewidth] too" — turned out to
require replacing `text/tabwriter` outright: it pads to the widest cell, knows
nothing about the terminal, and measures in runes. The listing now fits itself
to the terminal, gives the summary every column left over, and sheds whole
columns in a fixed order (SOURCE, LAST SEEN, PROJECT, STATUS, COUNT) as the
window narrows. ID, CODE and SUMMARY never shed. Piped output is not fitted at
all, and `$COLUMNS` is deliberately not consulted.

**The remedy is the docs' own text, verbatim.** The plan asked only that every
code have a non-empty remedy. Each `Code.Remedy` is instead the first paragraph
of that code's "What to do" section in `docs/errors.md`, copied with its line
breaks collapsed, and held byte-identical by
`TestCatalog_RemediesMatchTheDocs` — so the copy cannot drift. This is what
"surfaces it; does not rewrite it" turned out to mean in practice.

**The listing footer leads with `show`.** It named only `errors clear`, an
action its own last paragraph is careful to say changes nothing. `errors show
<id>` now comes first.

**`internal/errorscmd` had no tests.** It has a width sweep now (every width
1..240, scoped and machine-wide, with and without history, wide runes
included), because coverage that lives only in a verify suite is unprotected
the moment the task lands.

**Three test files were fixed, not because this task was about them.** They
described the pre-split Python API or the old `errors show` string, and were
failing the project-wide suite: `tests/test_db_gate.py`,
`tests/test_errors_project_scope.py`, `tests/test_task_unsettled.py`.

**`endless-go` takes `--db-dir <dir>`, not `--config-dir`.** Noted because
E-2151's landed verify suite uses `--config-dir` against `endless-go` and would
setup_error today. Not touched — a landed suite is its owner's to run.
