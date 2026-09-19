# E-1763 — Refuse to record the current user's name (gate + lint)

## Context
User-facing content should say "the user", never a hardcoded personal name. Endless is
multi-dev (~40 projects), so a name leaking into shipped docs, task text, or decisions is a
product concern, not cosmetic. Detected while landing E-1648 (the mermaid labels said
"<name> approves"). The immediate content fix is a separate `now` task; THIS task is the
systemic prevention. **Phase `maybe`** — build only if the recurrence rate justifies the
machinery and the false-positive problem (below) proves tractable.

## Source of "the user's name"
Deterministic and generic: `git config user.name` (fall back to the OS username / an endless
config field). No hardcoded names — the check adapts to whoever is configured, so it works for
every user, not just this repo's owner.

## Deliverable 1 — CLI-record gate
When an endless write command would persist the current user's name into recorded content —
`task add` / `task update` (title, description, text), `decision add`, `note add` — error out
with a clear message, overridable with `--allow-username`. This covers name leaks into the
ledger, the high-signal path the agent hits most.

## Deliverable 2 — doc-file lint (the additional step)
The CLI gate cannot see doc files edited via the editor directly (the E-1648 mermaid leak went
in that way — the endless CLI never touched it). Add a lint — a `just lint` / pre-commit check —
that scans staged (or repo) files for the configured user's name and fails with `file:line`, so
leaks into `docs/guide/`, README, CLAUDE.md, etc. are caught before commit. Same
`--allow-username`-style escape hatch (an inline allow marker or a flag).

## Open questions (resolve before building — phase is `maybe`)
- **False positives.** Many first names collide with common words ("Mike" ≈ microphone;
  Will / Mark / Bill / Bob). Decide match strictness: full name only? first name with word
  boundaries + case-sensitive? a configurable allowlist?
- **Legitimate uses to NOT flag:** code-comment design attribution ("decided with <name>"),
  authored/personal docs (talk/brief files), CHANGELOG / AUTHORS.
- **Surfaces:** ledger-only, docs-only, or both; lint at pre-commit vs CI vs `just lint`.
- **Escape granularity:** `--allow-username` per-invocation only, or a project-level opt-out for
  projects that legitimately use the owner's name?

## Verification
- `task add "... <configured-name> ..."` is rejected; with `--allow-username` it succeeds.
- The lint fails on a doc containing the configured name and passes once reworded; it does NOT
  flag the excluded legitimate contexts.
