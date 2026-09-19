## What we landed on

Build a native colorized terminal markdown renderer into `endless-go` (using **goldmark**, already a direct dep — no `glow`, no `glamour`). `endless task show` renders all its multiline markdown fields as ANSI when writing to a TTY, with optional `-p` paging through `less -R --mouse -F`. This replaces the `endless task show <id> --text | glow -p` workflow and removes the `glow` dependency.

Implementation filed as **E-1746** (implements / cleans_up this brainstorm).

## Key findings & decisions

1. **glow's real flaw is that it reflows prose at all.** glamour (glow's engine) inserts hard line breaks to hit a target width, which is what mangles inline code spans (`E-`/`NNN.md`) and hyphenated words. The fix is to *not reflow*: colorize, emit one logical line per paragraph, and let the terminal soft-wrap. Code blocks and tables overflow rather than being mangled. This dissolves the "wrap where a human expects" problem instead of solving it.

2. **`less -R` preserves color — it does not strip it.** `-R` is the raw-color flag (proved from `less 668`'s own man page). The `... | glow | less -R` output looked colorless because **glow strips its own color when its stdout is a pipe** (proved empirically: 0 escape sequences through the pipe), not because `less` removed anything. Design lesson: auto-detect the TTY for color, and when endless spawns the pager itself (`-p`), **force color on** (the git model) because a real terminal sits on the far side of the pager.

3. **Paging = `less -R --mouse -F`** — color + mouse-wheel scroll (a feature glow's pager lacked) + quit-if-one-screen. Dropped `-X`: it can suppress mouse-init on some terminals, and `less 668` (installed locally, with `--mouse`) doesn't need it.

4. **goldmark is already a direct dep; glamour is not** (and is heavy). Use goldmark's parser (share the config from the web dashboard's `internal/web/components/markdown/renderer.go`) with a **separate** ANSI `NodeRenderer` — HTML and ANSI are different targets by goldmark's design; share the parser, keep the renderers separate.

5. **Color toggle:** default is *auto* (TTY → color, pipe/redirect → plain); `--no-color` suppresses; the force-on case is handled internally by `-p`. Tri-state `--color=always|auto|never` deferred as YAGNI.

6. **AutoResearch loop: dropped.** It was only attractive for an intractable problem; correct wrapping turned out to be tractable (stop reflowing). Not filed; may be "never."

7. **De-risk first:** E-1746's first step is a spike rendering 10+ randomly-picked tasks' `text` fields and eyeballing wrapping + color before wiring the Python integration.

## Scope

- **In:** colorize all multiline markdown fields in `endless task show`; `-p`/`--paged`; `--no-color`.
- **Out (not filed):** `--raw` flag / splitting `task show` into a separate view command.
- **Deferred (not filed — say the word to capture it):** replace `less` with a self-contained pager (e.g. Bubble Tea viewport) to drop the external dependency; smaller scope now to lean on `less`.

## Spawned

- **E-1746** — Render colorized markdown for terminal display in `endless task show` (implements + cleans_up E-1743; relates_to E-445, the web markdown renderer that shares the goldmark parser).
