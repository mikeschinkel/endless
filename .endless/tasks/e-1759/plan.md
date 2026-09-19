# E-1759 — Handoff final-message: exception rule + `worktree check`, de-duplicated

## Context

The handoff templates' closing instruction enumerates report categories — "git/
worktree side-effects (uncommitted changes, dangling tags, stray artifacts), the
landed-vs-worktree delta, and state outside endless." Naming empty categories makes
agents fill them with "none," so a clean handoff produces ceremony. Two of the named
categories aren't even real: endless never creates git **tags**, and a pre-land
branch normally has **commits ahead of main** (the work), so both are non-anomalies.

E-1758 delivers `endless worktree check` (silent when clean). E-1759 rewrites the
closing block to **invoke it** instead of enumerating categories, framed as a single
exception rule: report a git delta only if `worktree check` prints one. Because the
block is copy-pasted across 6 templates (~11 bg/non-bg variants) + respawn's inline
form + the orchestration guide, the rewrite also **extracts the identical tail into
one shared partial** so the next change touches one place, not eleven.

**Depends on E-1758** (references `endless worktree check`): land E-1759 after
E-1758, else the templates name a command that doesn't exist. Set `blocked_by`.

## Surface

- `internal/templatecmd/templates/handoff/{task,bug,epic,research,brainstorm}.md.tmpl`
  — each has a `{{if .bg}}`/`{{else}}` pair of final-message blocks.
- `internal/templatecmd/templates/handoff/respawn.md.tmpl` — the same instruction,
  inlined in a bullet (different shape; rewrite in place).
- `docs/guide/orchestration.md` — carries the same "report ONLY … side-effects"
  language; update for consistency.
- `internal/templatecmd/template.go` `renderTemplate` (`template.New(name).Parse`)
  — parse a shared partial into each template's set so `{{template …}}` resolves.

## Approach

1. **Shared partial.** Add `handoff/_close.tmpl` defining `{{define "handoff_close"}}`
   — everything from the `worktree check` instruction through the bg-vs-return-line
   split (parameterized by `.bg`, `.return_anchor`). Update `renderTemplate` to parse
   this partial into the set before the named template (associated templates), so all
   six can `{{template "handoff_close" .}}`.

2. **New wording (the tail, in `handoff_close`).** Replace the category enumeration
   with the exception rule invoking the command. Canonical non-bg tail:

   > Run `endless worktree check` and relay anything it prints — it stays silent when
   > the worktree is clean, so a clean handoff says nothing about git. Flag external
   > state (CI, services) only if it's actually in play. Do NOT recap task status,
   > phase, or relationships, and do NOT confirm the negative ("no stray files",
   > "nothing to report"). Then the how-to-test and this return line — make the return
   > line prominent:
   > ```
   > tmux move-window -t archive: && tmux switch-client -t {{.return_anchor}}
   > ```

   bg tail: identical up to the return line, then "Then the how-to-test. You're a
   background agent — just stop when done; the user reads this with `claude attach`."
   (no tmux return line).

3. **Keep the task-specific prefixes inline.** The legitimate signal — "where the
   synthesis lives and the follow-ups spawned" (brainstorm), "the state of the
   children" (epic), "where the findings live" (research) — stays in each template,
   immediately before `{{template "handoff_close" .}}`. Only the duplicated generic
   tail moves into the partial.

4. **respawn + guide.** Apply the same exception-rule wording to respawn's inline
   bullet and to `docs/guide/orchestration.md` (these keep their own phrasing; they
   just drop the enumeration and reference `worktree check`).

## Deliverable — verify script

Generate `tests/tasks/e-1759-verify.sh`. Render each handoff type in both bg and
non-bg modes (via the existing template render path — the `endless-go template`
command / whatever `task spawn` uses) and assert:
- the rendered text contains `endless worktree check` and the "do NOT confirm the
  negative" clause;
- it no longer contains "dangling tags" or "landed-vs-worktree delta";
- the tmux return line appears in non-bg output and is absent in bg output;
- the task-specific prefixes (children / findings / synthesis) still appear.
Add/adjust any Go template-render unit tests in `internal/templatecmd`.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1759-verify.sh`, and eyeball one
rendered handoff (`endless task spawn <id> --print-decision`-style dump, or the
template command) to confirm it reads cleanly.

## Notes

- Out of scope, observed while reading: respawn.md.tmpl says `--status verify`, but
  the canonical lifecycle status is `unverified`. Flagging for a separate decision —
  not touched here.
- Complements E-1758 (the command) and E-1760 (the lint gate that catches
  negative-confirmation ceremony regardless of template wording).
