## What the column says today

`session status` renders one column between the task-type letter and the task
id (`unsettledMark`, internal/sessionstatuscmd/session_status.go:1010, rendered
at :679). Two states: ◆ when the row's worktree is unsettled, blank otherwise.

## What it says after this change

Three states. The column answers "is there work product here, and where is
it?":

| Glyph | Meaning | Example |
|---|---|---|
| ◆ | work product, still outstanding | anything unsettled |
| ⊙ | **no work product yet** | never spawned (no worktree), **or** `underway` with a settled worktree |
| blank | work product, all of it landed | `unverified` + settled |

The two cases sharing ⊙ are deliberate and are the point of the task: a task
nobody has picked up and a task a session is sitting on that has produced
nothing are the same fact about the work. The reader does not need this column
to tell those apart — the action icon and the status already do — so it must
not try to.

## The rule, decided

**⊙ when the row's task status is `underway` or earlier and the worktree is not
unsettled.** Blank when the status is past `underway` (`unverified`,
`unreviewed`, `confirmed`, `assumed`, `completed`) and the worktree is not
unsettled. ◆ whenever the worktree is unsettled, unchanged and taking
precedence over both.

Status is the discriminator, not landing history. `task_landings` is not a
reliable record of what reached main — E-2087 measured branches whose content
is demonstrably on main with no landing row at all — and a git-side answer
(`git log <base> --grep "E-<id>:"`) would put a new probe on a per-row hot
path. Status is already on `SessionStatusRow`, costs nothing, and reproduces
every case above.

Its one known mis-signal, accepted: a task that lands mid-flight and keeps
working is `underway` with real landed work and wears ⊙. It is still true that
nothing is outstanding. Label the glyph in the legend by what it MEANS, not by
what it is derived from, so the derivation can be sharpened later without the
vocabulary changing.

## The glyph, decided

**⊙ (U+2299 CIRCLED DOT OPERATOR).**

- It shares the unsettledMark column with only ◆ and blank, so the sole
  within-column distinction is circle vs diamond — different silhouettes, which
  ◇ (U+25C7) would not have given.
- The other circled-operator glyphs — ⊗ blocked, ⏸ blocks, ⊘ hidden, ⊕ queued —
  live in `blockField`/`hiddenField`/`relationField`, rendered after the id and
  phase char (:679-682). Position disambiguates them from this column.
- Width: U+2299 is East Asian Ambiguous, the same class as the already-shipped
  ⊘. Add a width assertion beside `TestHiddenGlyphWidth` rather than assuming.
- Known co-occurrence, accepted: an untriaged row carries ◌ (`actTriage`) in
  column 1 and will usually also carry ⊙ here, so `◌ T⊙E-2107` puts two round
  glyphs on one line. They mean compatible things and sit in different columns.

## Constraints the column imposes

- Display width 1. The prefix is a fixed 13 columns and the alignment
  guarantee ⏚/⁇/◆ carry (E-1765) holds only if every state in the slot matches.
- `buildLegend` shows a decoration only when a row bears it; ⊙ follows that
  rule and is labelled there — suggested label: "not started".
- ◆ vetoes dimming (E-1707) because it means "still something to do". ⊙ means
  the opposite and must NOT veto dimming — a never-started `later` row should
  still read dim.
- The "no worktree" half of the signal is already computed:
  `monitor.UnsettledDetail.HasWorktree`. `SessionStatusRow` carries `Unsettled`
  today and needs the second flag filled by the same
  `AnnotateSessionStatusUnsettled` pass.
