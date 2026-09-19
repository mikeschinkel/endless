# Render markdown tables in `mdterm`

Make `endless-go markdown render` (behind `endless task show`) render GFM tables as
aligned, width-aware, judiciously-wrapped columns instead of a mangled single line.

## Root cause

`internal/mdterm/mdterm.go:62` parses with `goldmark.DefaultParser()`, which does **not**
register the GFM table extension. A markdown table is therefore never recognized as a
table — its rows parse as one paragraph, and soft line breaks join with spaces
(`mdterm.go:218`), collapsing the whole table into a single mangled line. Two fixes are
needed: (1) enable the table extension so table nodes exist, (2) add a `renderTable` case
in `renderBlock` (today it hits `default` and recurses into garbage).

## 1. Parser — enable GFM tables

In `mdterm.RenderString`, replace `goldmark.DefaultParser()` with a parser that includes
the table extension:

```go
import "github.com/yuin/goldmark/extension"
p := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser()
```

Table nodes come from `github.com/yuin/goldmark/extension/ast` (aliased `xast`):
`xast.KindTable`, `xast.KindTableHeader`, `xast.KindTableRow`, `xast.KindTableCell`.
Per-column alignment is `(*xast.Table).Alignments []xast.Alignment`
(`AlignLeft`=1, `AlignRight`, `AlignCenter`, `AlignNone`).

Add cases in `renderBlock` (mdterm.go:92) dispatching `xast.KindTable` to `renderTable`.
The header/row/cell kinds are walked from inside `renderTable`, not dispatched top-level.

## 2. Terminal width input (caller-supplied)

`endless-go markdown render` runs as a separate process whose stdout is a pipe to `less`,
so it cannot probe the width itself. The Python caller supplies it:

- `src/endless/task_cmd.py`: compute `shutil.get_terminal_size()` (honors `COLUMNS` and the
  controlling tty even when stdout is piped to `less`) and pass `--width <cols>` on every
  `endless-go markdown render` invocation (the colorize helper around task_cmd.py:3714).
- `internal/markdowncmd`: parse a `--width N` flag in `Run` (currently `Run`→`runRender`
  at markdowncmd:18/25/44) and thread it into the renderer.
- `mdterm`: `RenderString` gains a width parameter (via an options struct or
  `RenderStringWidth(src string, width int)`; keep the old signature as a thin wrapper
  defaulting to 80). Width is only consulted by table rendering; prose/code paths are
  unchanged.

Default width `80` when the flag is absent.

## 3. Column-width allocator

New file `internal/mdterm/table.go`. Pure function: given per-column measurements + width,
return final column content widths. No `lipgloss` dependency (keeps mdterm's "no
third-party style deps" surface). Borrows lipgloss/table's *ideas* (min/max/median,
shrink-biggest, shrink-to-median) but fixes its two flaws (header truncation; sub-word
shrink).

### Tunable constants (named, top of file — settled empirically, see §6)

- `minColDivisor  = 2.5`   → `minCol = floor(W / (N * minColDivisor))`
- `maxHeaderRows  = 3`
- `targetMaxRows  = 6`
- `maxColFraction = 0.65`  → soft width cap `maxColFraction * W`
- `minVisibleCols = 2`

### Measurement (per column j)

- `natural_j` = widest cell **visible** width, including the header cell.
- `header_j`  = header cell visible width.
- Visible width via `github.com/mattn/go-runewidth` (already in go.mod) on the **plain**
  cell text (pre-styling), so width math never has to strip ANSI — styling is applied
  after widths are fixed.
- `minCol = floor(W / (N * minColDivisor))`, clamped `>= 1`. A column whose `natural_j <=
  minCol` is pinned at `natural_j` (never wrapped, never grown).
- `chrome` = per-column padding (1 space each side) + `│` separators between columns.

### Phases

1. **Trivial fit** — `sum(natural) + chrome <= W` → assign `natural`. Do **not** expand to
   fill W (stays compact; unlike glow). Done.
2. **Compress toward W** — shrink over-wide columns to the `minCol` floor, choosing the
   column to shave by: biggest-first, then most-over-its-own-median (lipgloss's two-pass
   idea), until `total <= W` or every non-pinned column is at `minCol`.
3. **Aspect relaxation (grow into pan territory)** — for any column whose tallest cell
   wraps to `> targetMaxRows`, grow it back out (letting table total exceed W → user pans)
   until its tallest cell is `<= targetMaxRows` or it hits the width cap
   `cap_j = min(natural_j, floor(maxColFraction * W), W - minCol)`.

`cap_j <= W - minCol` guarantees no column fills the viewport, so `>= minVisibleCols`
columns are always at least partially visible at any pan offset; `maxColFraction`
keeps the fattest column well under full width so `less`'s horizontal slide reads as
sliding, not column-replacement.

## 4. Cell + header rendering

New file `internal/mdterm/table.go` (rendering alongside allocation) or split render into
the existing renderer methods.

- **Wrap** each cell to its allocated width: break on whitespace; a single token longer
  than the width is hard-broken at the width (necessary — filepaths, hashes). Row height =
  max wrapped-line count across the row's cells; shorter cells pad with blank lines.
- **Header**: wrap up to `maxHeaderRows` (3) lines. If it still overflows, truncate the
  last line with `…` **and** record `(rendered, fullText)` for the legend (§5). Never
  silently truncate without a legend entry.
- **Alignment**: pad per the column's GFM alignment (left / right / center; `AlignNone`
  → left).
- **Inline markdown inside cells** (code spans, bold, links) is rendered via the existing
  `inlineLine`; padding is computed from plain-text visible width, then the styled string
  is placed and padded, so ANSI escapes never distort column math.
- **Structure emitted**: header row(s) → dim `───` rule spanning the table width → data
  rows. Each **physical** line ends with the SGR reset (`mdterm.go:83` `line` invariant) so
  `less -R` never sees color cross a line boundary.
- Column separator: a dim `│` between columns (minimal border — header + rule + column
  bars, no full grid).

## 5. Truncated-header legend

If any header was truncated in §4, emit a bordered box **below** the table:

```
┌─ columns ──────────────┐
│ Fr… = Frequency        │
└────────────────────────┘
```

One line per truncated header (`<rendered> = <full header text>`), box sized to its widest
line, dim border. Omitted entirely when no header truncated.

## 6. Verification — `tests/tasks/e-1775-verify.sh`

Language-agnostic per-task verify script (model on `tests/tasks/e-1577-*`). Runnable via
`esu && ./tests/tasks/e-1775-verify.sh`.

Automated (Go unit + golden):
- Allocator: trivial-fit; compress-to-minCol; aspect-grow past W; `minCol` floor honored;
  `cap_j <= W - minCol` (>=2 columns always partially visible); pinned narrow columns.
- Header: wrap to <=3 rows; truncate + legend entry beyond 3; legend box formatting.
- Alignment: left / right / center honored from GFM markers.
- Wrapping: whitespace break; over-long single token hard-broken at width; no sub-word
  break when the word fits the column (the "high"→"hig/h" regression).
- Every emitted physical line ends with SGR reset; piped (`| cat`) output has zero ANSI.
- `--width` flag parsed and threaded; Python passes `shutil.get_terminal_size()`.

Eyeball-tuning harness (settles the §3 constants — the "no single equation" reality; reuses
the E-1746 "render 10+ real tasks and eyeball, iterate" loop):
- Render a corpus of real task `text` fields containing tables at W = 80 / 100 / 120 / 160;
  visually confirm no sub-word breaks, no header lost without a legend, reasonable aspect
  ratios, `>=2` columns visible. Tune constants; re-run.

## Out of scope

- **Web HTML renderer** (`internal/web/components/markdown`) has the identical missing-table
  bug — file as a separate linked bug when directed; on hold per this session.
- **`less` invocation** (`task_cmd.py:3903` `less -R --mouse -F`) is unchanged. A short-but-
  wide table can trip `-F` (quit-if-one-screen) before the user can pan — pre-existing edge,
  uncommon for task text, not chased here. Tuning `less`'s pan increment (`-#`) is a possible
  future lever.
