# Spec — as built

## Decisions (resolved by user, 2026-08-08)

1. **Install scope: project-scoped only.** `<project>/.claude/output-styles/Endless.md`.
   A user-global install would reshape every Claude session on the machine, including
   projects that never opted in.
2. **Activation: place by default, `--activate` opts in.** A bare install prints a loud
   stderr WARNING that the style is present but inert, and names both activation routes.
   A silently-inert file is worse than no file: every surface reads "done" while nothing
   changed.
3. **Wiring: both.** `project init` scaffolds it (never activates); `endless setup
   output-style` / `remove-output-style` install and remove it independently, matching
   the setup group's existing `remove-` convention.
4. **Go/Python split:** the Go binary owns the embed and all filesystem/settings logic;
   the Python `setup` and `project init` surfaces shell out to
   `endless-go outputstyle`, preferring the worktree-built binary via
   `config.resolved_worktree_endless_go()`. Compatible with E-1486's port-to-Go
   direction — no new Python-side logic to unwind later.

## Style content (scope "b", as revised in review)

Started as an *exclusion* test ("is this fact discharged?") and was inverted to an
**inclusion** test after user review: that framing fails because "all tests passed" is
a perfectly undischarged fact the user does not want. Silence is now the default.

Something earns a place only if the user must act on it:
- they must take an action
- you departed from the plan (they must review it)
- you found a bug and did **not** fix it
- you found a concern and took **no action** on it
- it contradicts what they or you believed

Discharged (never restate): anything `endless session status` prints; anything a tool
just printed; anything in a file/plan/diff being handed over; their own request; work
that went as expected ("all tests passed", "fixed it and it works", "no stray files").
Tests are reportable only on failure.

Plus:
- **Lead with bare IDs** (`E-1919, ED-1532 — <answer>`), no introducing sentence.
- **Never narrate the disclosure** — "one thing I want to be explicit about", "it's
  worth noting", "rather than bury it", "for transparency". The bar already settled
  whether it qualifies; restating the justification is pure overhead. Added after the
  user supplied a real before/after pair from another session (30 words of framing
  around an 8-word payload).
- **Veracity**: verify at the moment of surfacing; your own earlier message is not a
  source.
- **Form**: concise bullets over prose (user's call; prose only when the connection
  *between* steps is the content).
- **E-1911 carve-out**: the `task report` block is exempt and must be appended
  unchanged. The style must never be readable as license to swallow it.

## Delivered

- `internal/outputstylecmd/` — `install` / `remove` / `path`. `//go:embed templates`.
  settings.json merge preserves all other keys; refuses malformed JSON rather than
  overwriting; deactivates only when *this* style is selected, so a user's own choice
  of a different style is never clobbered.
- `internal/outputstylecmd/templates/Endless.md.tmpl` — the style. Filename, YAML
  `name:`, and the settings.json value are all deliberately `Endless` so no lookup can
  disagree. `keep-coding-instructions: true` is set explicitly (see analysis: the trap).
- `monitor.ProjectRootFromCwd` + `monitor.ErrNoProjectContext` — shared cwd→root
  resolver, added rather than making a third copy of templatecmd's walk-up.
- `endless setup output-style [--activate] [--force] [--project]` and
  `endless setup remove-output-style [--project]`.
- `project init` scaffolds via `_scaffold_output_style`, best-effort: a failure reports
  and does not fail registration.
- `.claude/output-styles/Endless.md` committed — endless dogfoods it, placed not
  activated.
- `tests/tasks/e-1919-verify.sh` — 44 checks.

## Note on the templatecmd self_dev guard (did NOT transfer)

templatecmd skips materialization for self_dev projects because its `loadTemplate`
precedence is `.local.tmpl` → `.tmpl` → embedded: a materialized on-disk copy wins the
lookup forever after, so editing the embedded Go source appears to do nothing, and the
untracked file leaves the worktree UNSETTLED so `land` refuses.

Neither half transfers here. This package has no read-back precedence — it only ever
writes — so nothing can shadow the embed. The untracked-file half is handled by
committing endless's own copy deliberately rather than letting it appear as a side
effect.

## Verification

One command: `esu && ./tests/tasks/e-1919-verify.sh`

Covers: embedded delivery; placement/activation separation; the inert warning naming
both routes; key preservation on activate and remove; refusal to clobber a foreign
style or malformed JSON; `project init` scaffolding without activating; the style body
carrying each contract clause; and a repo-wide grep asserting nothing tells users to
run the removed `/output-style` command.

## Not covered — needs a live session

Whether `keep-coding-instructions: true` actually preserves the default system prompt's
`anti_verbosity` / `action_caution` / `task_continuity` sections is asserted from the
built-in styles all setting it, not observed. It cannot be verified from outside a
running Claude session with the style active.
