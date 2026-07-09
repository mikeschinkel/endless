## Goal

`endless task show` renders its multiline markdown fields (`text`, `outcome`, `description`, `analysis`, `notes`) as colorized ANSI in the terminal, with correct wrapping — replacing the `endless task show <id> --text | glow -p` workflow. No `glow` dependency.

The core fix vs glow/glamour: **do not reflow prose.** glamour inserts hard breaks to hit a target width, which mangles inline code spans and hyphenated words. Instead, colorize and emit one logical line per paragraph and let the terminal soft-wrap. Code fences and tables overflow rather than being mangled.

## Components

### 1. ANSI markdown renderer (Go)

New package (e.g. `internal/mdterm`). Parse markdown with **goldmark** (already a direct dep). Reuse the parser + extension configuration from `internal/web/components/markdown/renderer.go`, but implement a **separate** goldmark `NodeRenderer` that targets ANSI (not HTML) — HTML and ANSI are different output targets by goldmark's design; share the parser, not the renderer.

- Colorize: headings, inline code, fenced code blocks, emphasis/strong, list markers, blockquotes, links.
- Prose: emit each paragraph as **one logical line — no inserted hard breaks.** Terminal soft-wraps.
- Fenced code: emit verbatim, no wrapping; optional dim/background; let the terminal overflow.
- Reset SGR (`ESC[0m`) at the end of every logical line so `less -R`'s "color changed within one line" constraint always holds.
- Pure transform: input markdown → output ANSI, always colorized. The color on/off decision lives in the caller (below), not here.

### 2. `endless-go` subcommand

Add `endless-go markdown render` reading markdown on stdin, writing ANSI to stdout. Register in the `cmd/endless-go/main.go` dispatcher alongside the existing subcommands (`event`/`hook`/`channel`/`sandbox`/`serve`/`tmux`/`session-*`/`template`).

### 3. Python `endless task show` integration (`src/endless/task_cmd.py`)

- Add flags: `-p` / `--paged`, and `--no-color`.
- Color decision: `color = (not --no-color) and (sys.stdout.isatty() or paged)`.
  Note the `or paged`: when we spawn the pager ourselves, stdout is a pipe to `less`, so isatty() is false — we must **force color on** because a real terminal sits on the far side of the pager (the git model). This is exactly the trap glow falls into when piped.
- When color: send each multiline markdown field's content through `endless-go markdown render`.
- When `--paged`: spawn `less -R --mouse -F`, write the fully rendered document to its stdin.
  (`-R` preserves color, `--mouse` enables wheel scroll, `-F` quits immediately if it fits one screen. Do **not** add `-X` — it can suppress mouse-init on some terminals and `less 668` does not need it.)
- When not color: current plain-text behavior, unchanged.

## First step — de-risk spike (do this before wiring Python)

Build the renderer, then run it against **10+ randomly-picked tasks' `text` fields** and eyeball the output: no mid-word or inline-code breaks, code fences intact, headers/code colored, wrapping matches human expectation. Iterate on the renderer until the sample is clean. Only then wire step 3.

## Verify

Deliver `tests/tasks/e-1746-verify.sh` (model it on `tests/tasks/e-1577-verify.sh`:
pass/fail per check, `ALL PASSED` summary, exit 0 all-pass / 1 any-fail / 2 setup).
The verify-handoff to Mike must present **only**:

    esu && ./tests/tasks/e-1746-verify.sh

Fold every automatable check into that script — do NOT hand Mike a manual checklist.
The script must assert:

- On a TTY (drive it headlessly with a pty helper — `script`, or a Python `pty`),
  `endless task show <id> --text` emits ANSI color and does **not** break a known
  long token / inline-code span mid-word.
- Piped (`| cat`), `endless task show <id> --text` emits **zero** ANSI escapes.
- `--no-color` emits zero ANSI escapes even on a TTY.
- `endless-go markdown render` emits ANSI for headings, inline code, and fenced code.

**Manual (only because it cannot be automated — elaborate these in the handoff,
never as unlabeled checklist items):** interactive `-p` paging through `less`, and
mouse-wheel scroll. State them explicitly and briefly so Mike knows the one eyeball
step; everything else is the single command above.

## Out of scope

- `--raw` flag / splitting `task show` into a separate view command (revisit only if a real need appears).
- Replacing `less` with a self-contained pager to drop the external dependency (possible future follow-up).
- Web dashboard markdown rendering — owned by E-445 / E-449.
