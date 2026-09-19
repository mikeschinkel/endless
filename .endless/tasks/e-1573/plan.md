# E-1573 — Document spawn/coordinator/bg-dispatch patterns in endless guide orchestration section

Lands LAST in the epic — after every code child is in `verify`/`confirmed`/`assumed` so the docs describe the actual shipped behavior, not aspirational behavior.

## Decisions

### Scope of edits
- **Replace** the "Spawning another Claude session" section (lines ~142–198 of `docs/guide/orchestration.md`) with a per-type structure.
- **Add** "Background-agent dispatch", "Attach verbs", "Coordinator pattern for epics", and "Throttle warning" subsections.
- **Add** a short "Session lifecycle (bg agents)" subsection covering what survives and what kills bg agents. Do NOT reference recovery commands — recovery work is filed but not shipped (per house rule: no internal task IDs in shipped docs, and the verbs themselves do not yet exist).
- **Update** every reference to `docs/templates/handoff.md` (single file) to point at `templates/handoff/{task,bug,research,epic}.md.tmpl` (four files).

### New section layout (replacing 142–198)

1. **Spawning another Claude session** — overview paragraph: spawn dispatches a new session on a target task, foreground (new tmux window) or background (Anthropic supervisor process). Both auto-claim + pre-flight.

2. **Foreground vs background** — table:
   | | Foreground (`spawn`) | Background (`spawn --bg`) |
   |---|---|---|
   | Where it runs | new tmux window | Anthropic supervisor (no terminal) |
   | When to use | the work needs eyes; pairs well with `/plan` mode | dispatched child of an epic; you'll review later |
   | Survives | terminal close (tmux server) | terminal close, machine sleep, tmux server crash |
   | Dies on | tmux server kill, machine shutdown | machine shutdown, `claude stop`, 1h idle (unpinned) |
   | Promote to focus | (already focused) | `endless task spawn --attach` / `endless task attach` |

3. **Per-type handoff variants** — short subsection explaining the four variants (task/bug/research/epic), what frames each (verify end-state vs reproduce-first vs findings-as-deliverable vs coordinator role), and the fallback for unknown types.

4. **The handoff is generated, not authored** (retained text, paths updated to `templates/handoff/<type>.md.tmpl`).

5. **`endless task spawn`** — flag table (`--bg`, `--attach`, `--no-plan`, `--worktree`, `--force`, `--reopen`), example commands.

6. **What `--bg` does** — flow: pre-claim → render handoff → `claude --bg --name "E-N" "<handoff>"` → capture short id → write `sessions` row with `kind_id=2` (background; FK to `session_kinds`). Returns immediately; bg agent runs detached.

7. **Attach verbs** — covers both `endless task spawn --attach <id>` (new window) and `endless task attach <id>` (current pane exec). Includes the safeguard: `task attach` from inside an active Claude session refuses without `--force`. Detaching (`←` / `Ctrl+Z` / `/exit`) leaves the bg agent running.

8. **Coordinator pattern for epics** — explains: epic spawn opens a fg tmux window for a coordinator; the coordinator does NOT implement directly; the coordinator's job is to drive children through `needs_plan` → `ready` → `in_progress` → `verify`. References the children-state breakdown injected into the epic handoff and lists the six operational modes (zero / all-needs_plan / all-ready / all-in_progress / all-terminal / mixed) with the action each implies.

9. **Throttle warning** — soft stderr warning when N bg agents already active for the project; default threshold 3 via `bg_throttle_warn` in `.endless/config.json`. Does not block.

10. **Session lifecycle (bg agents)** — short bullet list: survives terminal-close / tmux-server-kill / machine-sleep (v2.1.142+); dies on machine-shutdown / `claude stop` / 1h idle (unpinned). No "to recover, run X" text — recovery commands don't ship in this epic.

11. **Customizing handoff templates** — covers the override mechanism shipped by E-1565:
    - Templates ship embedded in `endless-go` and materialize per-file into `<project_root>/.endless/templates/<name>.tmpl` on first render.
    - Edit the materialized file to customize project-wide (committed).
    - Delete the materialized file to restore the embedded version on next render.
    - For per-developer overrides that should NOT enter git, create `<project_root>/.endless/templates/<name>.local.tmpl` — takes precedence over the committed `.tmpl`. **Recommendation:** add `.endless/templates/*.local.tmpl` to your `.gitignore` so personal overrides don't get committed by accident. (Endless does not auto-modify `.gitignore`.)
    - `endless internal template render <name> [--project <name>]` debug-renders any template from JSON vars on stdin.

### Cross-reference cleanup
- `docs/guide/orchestration.md`'s `index.md` table-of-contents lines (if any reference current subsection slugs) get updated.
- The TOC/cross-reference table in `endless guide` (the auto-generated one) regenerated via `/regenerate-guide` AFTER this file's edits — same slash command Mike uses. Plan calls it out so the verifier doesn't forget.

### What does NOT go in the guide
- E-NNNN task references (per house rule).
- Anthropic CLI command syntax that may shift (`claude --bg`, `claude attach`) — we link to `claude --help` rather than restate the flag set verbatim. Brief example only.
- Implementation internals (regex for short-id parse, hook bg-context detection) — those are code, not user docs.
- Override-template path (deferred — not shipped).

## Files

### Changed
- `docs/guide/orchestration.md` — section rewrite per the layout above.
- `docs/guide/index.md` — if it has a "Spawning" anchor or a section list, update to match the new subsection slugs.
- The agent-facing cross-reference table at the bottom of `endless guide`'s output — regenerated via `/regenerate-guide`.

### Tests
- `tests/test_guide_map.py` (existing) — extend if it asserts specific slugs from the cross-reference; re-run to confirm regeneration kept it green.
- No new test file required — guide content is prose. Spot-check links resolve.

## Sequence note

Lands LAST. Depends on every other child being implemented (and ideally verified) so the docs describe what ships, not what was planned.

## Verification

1. `just test` passes (guide-map tests still green after regeneration).
2. `endless guide orchestration` renders the new content end-to-end without broken `[[link]]`s.
3. `endless guide` (no args) shows the orchestration section in the TOC.
4. `endless guide --list` shows the same slug set as before (no rename intended).
5. Manual read-through: the section reads cleanly cold; an agent unfamiliar with the epic can follow it.
