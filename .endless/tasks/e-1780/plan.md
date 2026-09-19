# E-1780 — Document the session commands: agent-facing in sessions.md, user-facing in a new appendix

## Context

`docs/guide/sessions.md` covers status snapshots, "who am I" discovery, and the DB
internals of the status subsystem, but none of the navigation / observation /
inspection command family that has since grown on the `session` group. The guide-map
prepends "▸ AGENT — read this: `endless guide sessions`" to every `session`
subcommand, so that pointer currently resolves to a section that never explains the
commands. This task makes it honest — and applies the guide-audience convention we just
recorded.

## Convention this applies (ED-1533)

The endless guide is **agent-first**: sections are written for the AI agent to minimize
context load. Commands intended only for human users are documented in a separate
**appendix**, referenced from agent-facing sections with a single minimal pointer —
never inlined. The appendix may appear in the section list; naming it an "appendix"
signals agents to deprioritize it (they pull it in only to inform a user). No
`guide`/`guide_map` code change is needed to realize this.

## Command classification (settled with Mike)

- **Agent-facing → stay in `sessions.md`:** `status` (cheap self-orientation), `show`
  and `list` (an agent coordinating inspects sibling/child sessions), plus the existing
  `snapshot add`, who-am-I, and snapshot-reading content.
- **User-facing → new appendix:** `goto`, `back`, `trail`, `monitor`, `history`,
  `search`, `recap`, `hide`, `unhide`.
- **Cross-referenced, not re-documented:** `use` / `cd` / `forget` are the backends of
  the `esu`/`esp`/`esf` shell helpers already in `endless guide orchestration`.
- **Deferred, not documented:** `activity` (slated for removal, E-1781); `order` (part
  of an open, undecided curated-session-todo-list initiative — distinct from the
  algorithmic `status --tree`; document if/when that initiative lands).

## Deliverables

### 1. `docs/guide/sessions.md` — agent-facing edits

- Add a terse entry for **`status`** (one-shot view of the focal task + spawning task +
  sibling sessions + cross-session in-flight work; `--tree` = do/plan backlog in impl
  order) and for **`show`** / **`list`** (inspecting sibling/child sessions when
  coordinating).
- Retitle `## Reading status (forward-looking — E-1319)` → `## Reading snapshots` (its
  content is `session snapshot` reads, which still haven't shipped; drop the bare
  `E-1319` from the heading per the no-task-ids-in-shipped-docs rule).
- Add **one minimal pointer** (not a command roll-call): interactive, user-run session
  commands (navigation, live-watch, history/search/recap, hide/unhide) live in
  `endless guide appendix-a`.
- Add no new `E-NNNN` citations; inline any that fall on lines being edited. The
  file's other legacy citations are left for a separate hygiene pass (out of scope).

### 2. New appendix — `docs/guide/appendix-a.md` ("Appendix A — User-focused commands")

- Lead line: these are commands a human runs; an agent reads this only to point a user
  at one.
- Seed with the user-facing session commands, one terse line each: `goto`, `back`,
  `trail`, `monitor`, `history`, `search`, `recap`, `hide`, `unhide`.
- Global appendix — future user-only commands from other groups append here.

### 3. Cross-reference wiring

- Add `appendix-a` to the `## Sections` list in `docs/guide/index.md`, clearly labeled
  as a user-facing appendix (so it's listed but visibly deprioritized). Regenerate the
  generated command/topic block only if a row actually changes (none expected — no new
  command→section mappings), then confirm `just guide-check` is green (the prior
  top-level-`verify` gap is closed now that `task verify` shipped).
- Leave the `session` subcommands mapped to the `sessions` section (they inherit the
  group; the sessions pointer routes users onward). No leaf map files, no code change.
  Optional future polish (not this task): remap the user-only commands to `appendix-a`
  via leaf map files for a more direct `--help` pointer.

### 4. Verification — `tests/tasks/e-1780-verify.sh`

Lightweight, modeled on `tests/tasks/e-1607-verify.sh`; asserts against the worktree's
own files (global `endless guide` reads the main checkout):

- `sessions.md` documents `status`, `show`, `list`; carries the retitled
  `## Reading snapshots` heading and the single `appendix-a` pointer; and does **not**
  inline the user-facing commands (e.g. no `monitor` how-to in `sessions.md`).
- `appendix-a.md` exists and documents each of the nine user-facing commands plus the
  "a human runs these" framing line.
- `index.md`'s `## Sections` lists `appendix-a`.
- `docs/guide/*.md` still yields the 5 pillar sections + the appendix (no accidental
  extra section); `just guide-check` passes.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1780-verify.sh`.
