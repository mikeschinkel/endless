# Plan — E-1769: `--ascii` flag for `session status`

## Problem

`endless session status` decorates output with multibyte glyphs — the
`actionMeta` icons (●↑↩⟳▶⚑✎☑◷⏚⁇), the tree-drawing constants (├── └── │), the
`…` truncation suffix, and the tree view's done/blocks/dirty markers. Piped to
plain text (e.g. `| pbcopy` then edited), they garble and mislead (they caused a
task-id mis-edit). Add an opt-in `--ascii` that renders single-byte equivalents;
default output stays exactly as today.

## Approach

Thread an `ascii` mode through the renderer and select glyphs **at render time**
(single source of truth) — NOT a post-render string replace, which would
misalign the width-aware table: the icons are single-width, and `…`→`...` changes
width, so the suffix must be fed into `runewidth.Truncate` for the title budget
to stay correct.

- Extend the `actionMeta` table (`session_status.go`) with an ascii icon per
  action; the `icon()` accessor picks by mode.
- Add ascii variants of the tree constants (`tree.go`).
- Pass the ascii truncation suffix (`...`) into `runewidth.Truncate`.
- Transliterate the legend/header builder, and sweep the package for any
  remaining decorative glyph literals (tree view's done/blocks/dirty markers).

## ASCII mapping (single-width, mutually distinct; the legend disambiguates)

| action | glyph | ascii | action | glyph | ascii |
|--------|-------|-------|--------|-------|-------|
| this   | ●     | `*`   | verify | ☑     | `v`   |
| parent | ↑     | `^`   | orphan | ◷     | `o`   |
| from   | ↩     | `<`   | landed | ⏚     | `=`   |
| doing  | ⟳     | `@`   | unknown| ⁇     | `?`   |
| do     | ▶     | `>`   | tree   | ├──/└──/│ | `\|-- ` / `` `-- `` / `\|   ` |
| review | ⚑     | `!`   | ellipsis | …   | `...` |
| plan   | ✎     | `~`   |        |       |       |

Dirty/blocks/done markers found during the sweep get single-width ASCII
stand-ins in the same spirit, kept distinct from the above.

## Flag surface

- `endless-go session-status --ascii` (Go flag, alongside `--monitor`/`--task`),
  threaded to the renderer.
- Python `endless session status --ascii` passthrough.
- Composes with `--monitor` (orthogonal).

## Verify — `tests/tasks/e-1769-verify.sh` (esu header; exit 0/1/2)

Drive the headless path (`session-status --task <id> --ascii`) so output is
deterministic, and assert:
- `--ascii` output contains **zero** bytes outside `0x09` / `0x0A` / `0x20–0x7E`.
- default output (no flag) is byte-identical to before this change.
- the ascii legend shows all markers distinctly and columns stay aligned on a
  truncated-title row.
