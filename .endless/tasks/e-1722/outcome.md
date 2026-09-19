# E-1722 — Triage: MEMORY.md lessons → endless feature / guide-doc fit

**Findings only.** This classifies which of the endless project's agent-memory
entries look like conversion candidates. It does NOT decide dispositions, design
fixes, or file follow-ups — that is E-1723, which consumes this table.

## Method & coverage

All **140 topic files** in `~/.claude/projects/-…-endless/memory/` were read in
full (body, not just the MEMORY.md one-line index) and classified via a 4-way
fan-out, then normalized in one pass by re-checking every (a)/(d) call against the
live code surfaces (`internal/hookcmd/claude.go`, `src/endless/task_cmd.py`,
`docs/guide/`). Categories:

- **(a) endless-feature fit** — could be gated/enforced by the product (hook / CLI
  check / schema-enum) so every user gets it, not just Mike's memory.
- **(b) guide-doc fit** — durable guidance for `endless guide`.
- **(c) stays memory** — pure agent-behavior / generic-tool / writing-style
  feedback with no endless product surface.
- **(d) stale/obsolete** — no longer true; pruning candidate.

**Normalization overrides applied** (subagent first-pass adjusted by me):
- *Plan seeds:* the ship-in-product cluster (`endless-fixes-ship-in-product-…`,
  `ship-in-product-not-machine`) + `gates-not-guardrails` are the **meta-principle
  behind the whole (a) column** → filed (b), high, flagged META.
- *Already-shipped-as-feature* (verified in code — conversion value is low because
  the gate/validator already exists; memory is now reinforcement): `long-form-…`
  & `task-title-shape` (validate_title/validate_description already enforce
  length+verb-first+no-newline), `record-all-actions` (claim-before-write gate),
  `one-task-per-worktree` (worktree gate E-971/E-986), `no-guessed-sqlite3-paths`
  (sqlite-block gate — residual is only extending it), `no-unauthorized-task-type`
  (`task_types` FK + research-gate already validate `--type`).
- `status-unplanned-not-needs-plan`: code no longer emits `needs_plan` and
  `tasks.status` is intentionally free-text (no CHECK, per `no-db-check-constraints`)
  → moved a→**b** (document the vocabulary; a status-enum conversion would fight the
  deliberate free-text design).

Counts: **(a) 31 · (b) 56 · (c) 52 · (d) 1** = 140.

---

## (a) Feature-fit candidates — 31

| entry | fit note | feasibility surface | priority |
|---|---|---|---|
| never-discard-auto-record-commits | destructive git wipes ledger/snapshots/verbs.json | preToolUse Bash gate on `git reset/rebase/checkout` touching ledger | **high** |
| no-internal-task-ids-in-shipped-docs | ban `E-NNNN` in shipped doc strings; grep-checkable | CLI/CI lint gate over shipped strings | **high** |
| no-internal-task-ids-user-facing | same ban in CLI/hook messages | CLI/CI lint gate over user-facing strings | **high** |
| commit-before-verify-handoff | uncommitted worktree lost on drop; flip is gateable | status-guard on flip→verify (task_cmd.py); or preToolUse | **high** |
| no-plan-files-in-main | block plan-file writes/commits to main (hook only *syncs* today, doesn't block) | preToolUse Write gate on main `.endless/plans/` | **high** |
| plan-files-via-task-update | block direct Write/Edit of `.endless/plans/E-NNN.md` | preToolUse Write/Edit gate on plan-file paths | **high** |
| plan-attach-promotes-to-ready | need `unapproved` status + `approve` verb; guard bg-pickup | status state-machine + approve verb (CLI + tool gate) | **high** |
| proactive-spawn-readiness | `submitted` status carries spawn-ready signal (E-1648) | status enum value + CLI at file-time | **high** |
| search-before-filing | dup-scan before `task add`; ledger has 1400+ items, no check today | CLI similar-title warn in task_cmd.py | **high** |
| endless-schema-conventions | schema conventions mechanically checkable | schema/enum + Go `internal/<kind>/` validator; 2nd: guide | med |
| global-config-exception | commit-on-main gate must except verbs.json class | `blockCommitOnMainIfApplicable` exception list | med |
| recovery-commits-to-main-ok | recovery/hygiene commits are a carve-out in same gate | `blockCommitOnMainIfApplicable` exception logic | med |
| go-enum-pattern | int-const enum convention; reviewable/checkable | schema/enum + Go validator; 2nd: guide | med |
| no-db-check-constraints | FK-to-values instead of CHECK; convention | schema pattern; 2nd: guide/contributor doc | med |
| lead-with-remaining | status output should lead with remaining work | `endless session status` output ordering | med |
| no-end-of-session-status-recap | recap redundant with `session status` | make `session status` complete; 2nd: handoff template (E-1690) | med |
| append-only-files-check-line-count | ledger jsonl is append-only; loss detectable by count | CLI integrity check on ledger during land; 2nd: guide | med |
| companion-after-rebase | rebase drops `.endless/worktree.json`; breaks land | `worktree land` auto-recreate/warn on missing companion | med |
| drive-spawned-sessions-to-closure | closure guidance belongs in the generated prompt | `task spawn` prompt template; 2nd: guide | med |
| no-just-install-from-worktree | `just install` from worktree hijacks global tool | preToolUse Bash gate keyed on worktree cwd | med |
| no-mega-epic-parent | warn when parent child-count exceeds threshold (no guard today) | CLI check in `task add --parent` | med |
| per-task-verify-script | `tests/tasks/e-NNNN-verify.sh` scaffold (E-1596) | CLI generator + land-readiness gate | med |
| remove-worktrees-after-merge | defer worktree/sandbox reaping post-land (E-1332) | timer/on-demand reaper | med |
| session-id-not-interchangeable | gate shouldn't suggest an arbitrary session id | gate error copy + register-first/auto-register path | med |
| test-instructions-on-verify | require how-to-test when flipping to verify | status-guard requiring verify-instructions field; 2nd: guide | med |
| no-unauthorized-task-type | *partly shipped:* `task_types` FK + research-gate validate `--type`; residual = which types need Mike's OK | policy layer over existing enum; likely guide | med |
| no-guessed-sqlite3-paths | *partly shipped:* sqlite-block gate exists; residual = extend to stray-file-create | extend `blockSqliteAgainstEndlessIfApplicable` | med |
| task-title-shape | *already shipped:* `validate_title` enforces verb-first+length; residual = full `<verb> <subject>'s <symptom>` shape only | validate_title heuristic nudge | low |
| long-form-goes-in-plan-file | *already shipped:* `validate_description` enforces length+no-newline | (existing) validate_description | low |
| record-all-actions | *already shipped:* claim-before-write preToolUse gate | (existing) claim-before-write gate | low |
| one-task-per-worktree | *already shipped:* worktree gate (E-971/E-986) | (existing) worktree gate | low |

## (b) Guide-doc fit candidates — 56

| entry | fit note | priority |
|---|---|---|
| **endless-fixes-ship-in-product-not-my-memory** | META: the epic's thesis — fixes reach users only via product | **high** |
| **ship-in-product-not-machine** | META: never settings.json/CLAUDE.md/"user runs X" | **high** |
| **gates-not-guardrails** | META: bypasses get taken → design-review lens for every (a) | **high** |
| endless-errors (STOP on errors) | agent error-handling workflow; partly gateable | **high** |
| endless-is-the-filesystem | artifacts belong in task fields, not loose files | **high** |
| project-jsonl-events-are-db-wal | ledger jsonl is the DB WAL; corruption is costly; 2nd: (a) integrity gate | **high** |
| endless-vs-endless-go-flags | two binaries, distinct flag surfaces; agent conflates | med |
| extend-task-over-proliferation | when to extend vs file new | med |
| flagging-means-filing | "flagging" a concern means filing a task | med |
| isolate-hook-verification | hook smoke-tests write real DB; verify in isolation | med |
| just-is-dev-only | user-facing verbs must not shell out to `just` | med |
| just-land-for-landing | landing workflow (`just land E-NNNN`) | med |
| ledger-hygiene-direct-to-main | ledger-rewrite procedure + recovery-commit exception | med |
| loud-failure-on-invalid-state | validation/error posture | med |
| maybe-phase-bakes-decision | phase/title semantics | med |
| naming-conventions | snake/kebab/Pascal rules; partly checkable | med |
| agent-docs-few-broad-sections | how to structure `endless guide` sections | med |
| audit-task-delivers-findings | audit/research task-verb semantics | med |
| cleans-up-is-canonical-followup | relation-type vocabulary | med |
| confirm-symptom-filed-bugs | confirm diagnosis vs code before fixing | med |
| decisions-are-higher-level | when to `decision add` vs task detail | med |
| decisions-at-file-time | resolve design Qs in filing chat, not at claim | med |
| dont-scope-out-prototype-features | implement to spec; defer via task, don't silently drop | med |
| no-legacy | ask before back-compat aliases/shims | med |
| no-migration-for-unshipped | no migration/back-compat for unshipped SW | med |
| no-python-sqlite-reads | Go-only DB access (E-1486); 2nd: (a) lint gate | med |
| paths-in-output | cwd-relative/`~` paths via `displayPath`; 2nd: (a) shared helper | med |
| phase-semantics | maybe/later/blocked meanings; enum already seeded | med |
| plans-implementation-only | plan = implementation; rationale elsewhere | med |
| prompt-is-handoff-not-plan | spawn handoffs generated; anchor by symbol not line# | med |
| prompts-and-plans-option-free | resolve options in chat; single path | med |
| record-user-answers | write answers into plan + flip status immediately | med |
| run-my-own-verification | self-run E2E in a scratch worktree | med |
| task-prompts-address-user-directly | spawn-prompt authoring (name Mike, DO NOT CHOOSE) | med |
| task-relations-self-explanatory | when relates_to is load-bearing vs noise | med |
| titles-vs-prose | drop Layer/Phase labels from standalone titles | med |
| user-config-costs-adoption | prefer auto-injection over user config | med |
| verify-instructions-use-esu | verify blocks open with `esu` | med |
| web-ui-contrast | dark-theme contrast/label rules for web UI | med |
| status-unplanned-not-needs-plan | vocab now canonical in code; document status vocabulary | med |
| research-task-files-the-do-it-followup | research done only when action task filed+planned; 2nd: (a) completion gate | med |
| project-assumed-promotes-to-confirmed | assumed→confirmed lifecycle; findings need other terminal | med |
| project-endless-multidev-product-goal | design ledger/upcasting for concurrent authorship | med |
| project-endless-user-scale | ~40 projects; design project-agnostic | med |
| project-plan-hierarchy-design | plan-tree/enforcement design | med |
| reference-rebuild-reopened-worktree | rebuild before test after reopening worktree | med |
| flag-for-verb-subset | `land --record-only` over sibling verb | low |
| flag-segment-count-smell | 4+ segment flag names smell | low |
| flags-over-env-vars | explicit flag over env var | low |
| verb-vs-flag | only subcommands are verbs | low |
| resolve-concerns-before-verify | verify-handoff etiquette | low |
| sql-files-must-be-tool-openable | SQL in `.sql` not string literals; could lint | low |
| unblock-by-extracting-small-slice | self-unblock a blocked task | low |
| worktree-provision-copies-binary | copies binary, not builds (settled) | low |
| reference-endless-go-tmux-subcommand | `endless-go tmux active-id` fact | low |
| reference-text-file-flag-convention | `--xxx-file` inline/path split (E-1001) | low |

## (c) Stays-memory — 52 (no product surface; priority low)

Generic tool / git hygiene, writing style, or interaction-with-Mike behavior:

- **Bash/git/shell hygiene:** bash-cwd-persists, bash-param-expansion-quoting,
  dont-use-stash, stash-pop-kept-means-incomplete, zsh-quote-brackets,
  copypaste-no-line-wrap, no-blanket-kill, no-guessed… *(→ (a))*.
- **Writing / notation / commit style:** meta-value-notation, no-cutesy-shorthand,
  no-em-dash, plain-english, no-route-shorthand, no-name-proliferation,
  no-never-no-always, no-sunk-cost, show-inspection-commands, no-product-promotion,
  no-coauthored-by-trailer.
- **Interaction with Mike / epistemic hygiene:** answer-the-asked-question,
  no-assumptions-in-question-framing, discuss-open-questions-first,
  discuss-before-updating, dont-assert-unmade-decisions, dont-lecture-project-owner,
  dont-lock-soft-preferences, dont-restate-session-status, dont-self-cite-as-convention,
  own-my-proposals, retract-dont-rejustify, when-in-doubt-ask,
  no-expected-state-as-loose-end, no-redundant-signals, requery-live-session-state,
  use-returned-task-id-literally, record-all-tasks-and-decisions (ask-first judgment),
  question-workaround-induced-bugs.
- **Generic Go/eng practice:** no-maps-in-go-json, compute-dont-cache,
  decouple-before-ship, drop-abstractions-with-their-system,
  verify-behavior-in-code-not-docstring, css-overrides.
- **Dev-process judgment / project attitude (not agent-runtime, not enforceable):**
  cli-naming-agent-friction, cli-one-positional, product-trigger-word,
  dont-conflate-mike-with-users, no-autonomous-remediation, move-fast,
  env-vars-visible-latency-invisible, endless-install-command (self-dev step),
  reference-apfel-cli (unrelated external CLI), reference-docs-private-git-excluded.

*(Some of the above carry a weak secondary guide angle but are primarily agent
behavior — listed here to keep the (a)/(b) shortlist focused for E-1723.)*

## (d) Stale / pruning candidate — 1

| entry | why stale |
|---|---|
| companion-file-terminology | the terminology prohibition was reversed and the self-check explicitly retired; low residual value — prune candidate |

---

## Rollup for E-1723

**Highest-value (a) feature candidates (build these first):**
never-discard-auto-record-commits · no-internal-task-ids-in-shipped-docs /
-user-facing (one gate, two callers) · commit-before-verify-handoff ·
no-plan-files-in-main / plan-files-via-task-update (one plan-path gate) ·
plan-attach-promotes-to-ready · proactive-spawn-readiness (both part of one
status state-machine: unapproved/submitted/approve) · search-before-filing.

**Highest-value (b) guide candidates:** the three META principles
(ship-in-product ×2 + gates-not-guardrails) should anchor a "why endless behavior
ships in the product" guide section; plus endless-errors, endless-is-the-filesystem,
project-jsonl-events-are-db-wal.

**Already-shipped — do NOT re-build, at most reinforce/extend:** task-title-shape,
long-form-goes-in-plan-file, record-all-actions, one-task-per-worktree,
no-guessed-sqlite3-paths (extend-only), no-unauthorized-task-type (policy layer only).

**Natural groupings (combine in E-1723 rather than 1 task per lesson):**
(1) status state-machine — plan-attach-promotes-to-ready + proactive-spawn-readiness +
status-unplanned vocab + assumed→confirmed lifecycle; (2) plan-path write gate —
no-plan-files-in-main + plan-files-via-task-update; (3) internal-ID lint —
both no-internal-task-ids-* entries; (4) commit-on-main exceptions —
global-config-exception + recovery-commits-to-main-ok; (5) META guide section —
the three ship-in-product/gates principles.

**Deferred to E-1724 (not converted here):** there is no dedicated memory *file*
for the index-size / partial-load lesson — it lives in the E-1721 epic description
(MEMORY.md exceeding the 24.4KB read limit). Flagging per plan: memory-store
scale/maintenance is E-1724's model, not a per-lesson conversion.

**Plan seed with no backing memory file:** the "epic status is auto-derived; agents
shouldn't flag its changes" lesson (plan-named (b) guide seed; display half already
E-1725) has no standalone entry in the store — noted so E-1723 doesn't hunt for it.

**Data note:** MEMORY.md indexes 139 one-liners but the store holds 140 topic files
(one file lacks an index line); all 140 were triaged.
