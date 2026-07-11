# E-1607 — Fold a "Verification" subsection into the orchestration guide + wire final-message discipline into all six spawn templates

## Context

The verify-suite convention (the E-1596 tree) has no guide coverage, and the spawn
handoff templates state "how to test" as ad-hoc narrative that invites a
multi-command checklist. Two things must happen: (1) document the settled
convention in the endless guide; (2) give **every** spawn template an explicit
final-message discipline so a handoff is lean and predictable.

**Placement (confirmed with Mike):** verification is NOT a peer pillar on par with
tasks/orchestration/decisions/sessions/reference — it is a mechanism inside the
session→handoff→land lifecycle. It folds into **`orchestration`**, which already
owns the worktree lifecycle, the commit-to-main policy, and "the handoff (generated,
not authored)." No new guide slug; `endless guide --list` stays at 5.

Since the plan was written, reality moved:
- **E-1759 landed.** It refactored the six templates' closing block into a shared
  `internal/templatecmd/templates/handoff/_close.tmpl` partial (`handoff_close`),
  and already changed `respawn.md.tmpl` from `--status verify` to `unverified`. So
  the plan's stale-status backstop is already a no-op (kept as a regression assert).
- **E-1603 landed.** The Tier-0 runner exists (`internal/verifycmd`, surfaced today
  as top-level `endless verify <id>` at `src/endless/cli.py:486`). **Naming resolved
  (Mike):** the runner moves to **`endless task verify <id>`** (a `task` subcommand).
  That move rides the E-1625 runner line, which is **effectively done, just not landed
  — and must not be modified**. E-1607 documents the settled `task verify` name in
  prose only and **stays entirely out of command coverage**: it adds no `verify.md` /
  `task-verify.md` map file. `just verify`, the runnability modes, and the tier ladder
  remain Forthcoming.
- **`guide-check` is already failing on this branch** (pre-existing, not E-1607's
  doing): top-level `endless verify` has no map file, so
  `FAIL Commands with no map file at all: endless verify`. It clears when E-1625 lands
  (`task verify` inherits the `task` group's row; top-level `verify` disappears).
  E-1607 neither causes nor fixes it, and does not gate its verify script on global
  `guide-check`. Flag it in the handoff.

**Contract scope (confirmed with Mike):** the one-command *verification* contract
fits the three verify-handoffs (task/bug/respawn, ending `unverified`). The other
three (research/brainstorm/epic, ending `completed`) get the **complementary**
instruction — there is nothing to verify; do NOT invent a how-to-test or checklist —
so the agent never fills the silence with fatiguing low-value verification prose.
All six templates get explicit discipline.

## Deliverables

### 1. Verification subsection in `docs/guide/orchestration.md`

Add a `## Verification suites & the one-command handoff` section (model tone/depth on
the existing orchestration/decisions prose). **No internal E-NNN ids in the prose**
(inline the information). Content:

- **The per-task verify-suite convention.** One verification suite per task. Today
  realized as `tests/tasks/e-NNNN-verify.sh` — a self-contained bash script that
  builds an isolated env, prints pass/fail per check, ends in `ALL PASSED`, and exits
  0 clean / 1 on any failure. Point readers at `tests/tasks/e-1577-verify.sh` as the
  shape to copy.
- **The `verify.toml` manifest** — the settled form the runner executes. Real field
  names (read from `internal/verify/manifest.go`, `check.go`, `project.go`):
  top-level `schema`, `task`, `setup`, `teardown`, `tiers`, `seed`, `needs`; a
  `[[check]]` list whose entries carry `runner` (first-class `gotest`/`pytest`, or a
  raw runner), `tests`, `paths`, `command`, `format` (`gotest-json`/`pytest-json`/
  `tap`); and the project-level `.endless/verify.toml` layer whose
  `setup`/`teardown`/`seed`/`needs` compose beneath each per-task manifest. Discovery:
  `.endless/tasks/<id>/verify.toml` per task, plus project-root `.endless/verify.toml`.
  Preconditions run provision(`needs`) → `setup` → `seed` → checks → `teardown`.
- **The runner** (single line): `endless task verify <id>` executes a task's
  `verify.toml` suite under an isolated temp working dir + temp HOME/XDG, normalizes
  to a CTRF report, exits 0 on all-pass. Endless's own suites are still bash scripts
  pending migration to a manifest, so for those, run the script directly. (The `task
  verify` subcommand lands via the sibling runner-wrapper task; the guide names the
  settled form.)
- **The one-command handoff contract** (the load-bearing policy): at verify-handoff
  the session (a) runs project-wide regression itself (tests, lint, build) and REPORTS
  the outcome in prose, never as commands for the user; (b) hands the user exactly ONE
  verification command; (c) folds the task's own unit tests INTO the per-task suite as
  a fail-fast check; (d) never enumerates a manual checklist.
- **Forthcoming note** (clearly marked): the `just verify` wrapper, the runnability
  modes, and the sandbox/mise tier ladder are in progress; until they ship, realize
  the convention via the per-task script.

### 2. Cross-reference the new content (regenerate-guide skill)

E-1607 adds **only a topic** entry to the cross-reference (topics are
command-independent — zero orphan risk, and they don't touch the failing command
coverage):
- `docs/guide/help/_topics.md` entry, e.g. "per-task verification suite" →
  orchestration (optionally "one-command handoff contract" → orchestration).

Then `just guide-index` regenerates the `docs/guide/index.md` block so it includes the
new topic row and stays in sync. **Do NOT add or modify any command→section map file**
(the `task verify` mapping rides the E-1625 runner line; top-level `verify` coverage is
that line's pre-existing gap, not E-1607's). `guide_map.py` needs no edit (slugs are
filesystem-discovered). The regenerate-guide skill may be used for the topic, but its
command-map suggestions for verify must be ignored per the above.

### 3. Final-message discipline in all six templates

Add two shared partials to `_close.tmpl` (consistent with E-1759's `handoff_close`):

- `handoff_verify` — the one-command contract. Wording: hand the user exactly ONE
  command to verify (`esu && ./tests/tasks/e-{{.spawned_id}}-verify.sh`, folding the
  task's own tests into that one suite); do NOT enumerate a manual checklist; run the
  project-wide regression yourself and state the result in one line, never as commands
  for the user; see `endless guide orchestration`.
- `handoff_nonverify` — the anti-variorum prohibition. Wording: this task delivers
  information, not testable behavior — there is nothing to verify; do NOT invent a
  how-to-test, verification steps, or a checklist; point to the deliverable and stop.

Wire them (anchor on the existing "how-to-test" / "say where … lives" final-message
lines, not line numbers):
- `task.md.tmpl`, `bug.md.tmpl`: replace "lead with the how-to-test" with
  `{{template "handoff_verify" .}}`; keep `{{template "handoff_close" .}}` after it.
  Soften step 6's "then tell me how to test" to "then hand me the one verify command."
- `respawn.md.tmpl` (inline close, doesn't use `handoff_close`): inject
  `{{template "handoff_verify" .}}` in place of its "then tell me how to test" phrase.
- `research.md.tmpl`, `brainstorm.md.tmpl`, `epic.md.tmpl`: keep their bespoke "say
  where the findings/synthesis live" / "state of the children" opener and add
  `{{template "handoff_nonverify" .}}` before `{{template "handoff_close" .}}`.

Backstop (already satisfied by E-1759): no template uses `--status verify`; the verify
script asserts this as a regression guard.

### 4. Verification — `tests/tasks/e-1607-verify.sh`

Self-contained, modeled on `tests/tasks/e-1603-verify.sh` (same section/report/summary
helpers, isolated, exit 0/1). This is the single verification command. Assert:
- `endless guide orchestration` renders and now contains `verify.toml`, `[[check]]`,
  and the one-command language ("exactly ONE").
- `endless guide --list` still yields exactly the 5 existing slugs (no `verification`
  slug added) — guards against reflexively minting a new section.
- task/bug/respawn each contain the one-command contract language (`handoff_verify` —
  e.g. "exactly ONE" + "endless guide orchestration").
- research/brainstorm/epic each contain the anti-variorum language (`handoff_nonverify`
  — e.g. "nothing to verify" / "Do NOT invent").
- No handoff template contains `--status verify`.
- The generated `docs/guide/index.md` block contains a verification topic row pointing
  at `orchestration`, and the index block is in sync (regenerate leaves no diff).
- Fold-in regression: `go test ./internal/templatecmd/... ./internal/verify/...` runs
  green as a fail-fast check.

The script does NOT invoke the runner (`endless task verify` lands with E-1625) and does
NOT gate on global `just guide-check` (pre-existing top-level-`verify` failure is outside
this task). It asserts guide text + topic wiring + template wording only.

## Critical files

- `docs/guide/orchestration.md` (new subsection)
- `docs/guide/help/_topics.md` + `docs/guide/index.md` (topic only, via `just guide-index`)
- `internal/templatecmd/templates/handoff/_close.tmpl` (+ the six `*.md.tmpl`)
- `tests/tasks/e-1607-verify.sh` (new)
- Read-only refs: `internal/verify/{manifest,check,project,discover}.go`,
  `internal/verifycmd/`, `src/endless/{cli.py,guide_map.py}`

## Sequencing

E-1759 has landed — no concurrent-edit risk. D1 (subsection) precedes D2 (the topic
points at orchestration). D3 is independent. D4 last. E-1607 stays entirely within
topic + prose + templates; command coverage and the pre-existing `guide-check` failure
belong to the E-1603/E-1625 runner line and are only flagged in the handoff.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1607-verify.sh`.
