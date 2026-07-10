# E-1723 — Synthesis: dispositions for the MEMORY.md high-value candidates

Working brainstorm over E-1722's triage table. Decided the disposition + rough
shape for the **high-value (a) feature** shortlist one candidate at a time, filed
(or folded) the follow-ups, and set up the structure to finish the med-(a) and
(b)-guide tails in separate sessions. A cross-cutting memory-hygiene principle also
emerged and reshaped what "conversion" means here.

## Governing principle (emerged this session)
Once a **deterministic gate** (a hook/validator the agent cannot bypass) ships a
lesson, the paired agent-memory is **redundant and gets pruned** — belt & suspenders
is explicitly not wanted. Memory earns its keep only while the product does *not* yet
enforce the lesson (stopgap until the fix ships) or for lessons with no product
surface at all. Recorded in `feedback_endless_fixes_ship_in_product_not_my_memory`
(corrected — it previously said "keep as reinforcement on top"). Consequence: every
conversion task carries a "prune the memory on land" note, and lessons already fully
gated were pruned now.

## High-value (a) dispositions (6 clusters)

1. **`never-discard-auto-record-commits`** → filed **E-1734** (next).
   Surgical preToolUse Bash gate: block destructive git ops (reset --hard,
   force-push, rebase --onto/-i, branch -D) in agent sessions *only* when the
   discarded range contains a ledger/auto-record commit. Hard block, agent-scoped;
   revisit-to-warn if noisy. Needs a short impl plan (range-resolution across verbs).

2. **internal-ID bans** (`no-internal-task-ids-in-shipped-docs` + `-user-facing`)
   → filed **E-1735** (later). Config-driven docs linter (explicit path list, no
   auto-discovery, no `.md` assumption) flagging `E-NNNN` citations. Recurrence guard
   over the confirmed one-time strips E-1269 (CLI/hook strings) and E-1270 (guide
   docs); linked `cleans_up` both. Build after the full Go conversion. Runtime source
   strings out of scope (E-1269 covers them). Users do hit this; deferred on priority.

3. **`commit-before-verify-handoff`** → **already shipped; no task.** All three
   worktree-removal paths refuse uncommitted work: `worktree drop` refuses
   dirty/unmerged/foreign without --force; `worktree land` guards unmanaged dirt
   (E-1416, assumed); the reaper skips dirty/unmerged/active (E-1549, assumed). The
   E-1415 loss predates all three. Memory obsolete — Mike pruned it.
   Agent-drop-block idea (converting `remove-worktrees-after-merge`) considered and
   **left** — loss is already prevented regardless of who removes.

4. **plan-path gate** (`no-plan-files-in-main` + `plan-files-via-task-update`)
   → **folded into E-1202** and broadened. One preToolUse Write/Edit gate on
   `.endless/plans/E-NNN.md` (main *and* worktree mirror), routing to
   `task update --text`. Verified in code: `_materialize_plan_file` /
   `_commit_plan_file` (worktree_cmd.py) — the CLI writes/commits the mirror, not the
   agent's Write tool, so the gate leaves the land-produces-plan-markdown flow intact.
   Linked `cleans_up E-1721`.

5. **status state-machine** (`plan-attach-promotes-to-ready` +
   `proactive-spawn-readiness`) → **reuse E-1648** (planned, under epic E-1726).
   E-1648 is both lessons verbatim: unplanned → submitted → (approve) ready; remove
   auto-promote; submit/approve verbs; background sessions barred from approve and
   non-ready pickup. Linked `cleans_up E-1721`. (Live-observed the very auto-promote
   bug E-1648 fixes while editing its text; reset to unplanned.)

6. **`search-before-filing`** → filed **E-1738** (sqlite-vec semantic-search epic,
   next) + **E-1739** (dup-deny gate, blocked_by E-1738). Dup detection is semantic,
   not deterministic — it stays Claude's judgment; the product's job is a fast
   candidate surface. `task add` denies on semantic-dup matches (unimplemented tasks
   only) and lists them; override is `--ignore-dups=<count>` where the value must
   equal the number of dups found, so a bare boolean can't become a reflex bypass.
   Requires fast *local* embeddings (no `claude -p` file-time tax). Broader than dups:
   the vector index also powers `endless search` and relates_to suggestions.

## Already-gated → pruned now (4)
Verified each gate in code, then deleted the memory file + its MEMORY.md index line:
- `task-title-shape` → `validate_title` (proven live)
- `long-form-goes-in-plan-file` → `validate_description` (proven live — it rejected
  an over-long description mid-session)
- `record-all-actions` → claim-before-write gate (`handlePreToolUse`, claude.go)
- `one-task-per-worktree` → `enforceWorktreeGate` (claude.go)

## Partially gated → keep until residual ships (2)
Rolled into E-1741's candidate list; prune when their residual lands:
- `no-guessed-sqlite3-paths` — sqlite block gate exists (E-1226); residual = extend
  to stray-file creation.
- `no-unauthorized-task-type` — `task_types` FK + research-gate validate `--type`;
  residual = policy layer for which types need Mike's OK.

## Continuation brainstorms spawned
- **E-1741** — medium-priority (a) feature candidates (~15 + the 2 partials);
  one-at-a-time interview. `parent E-1721`, `relates_to E-1723`.
- **E-1740** — the 56 (b) guide-doc candidates; batch passes (~5 at a time), anchored
  by the META section (ship-in-product ×2 + gates-not-guardrails). `parent E-1721`,
  `relates_to E-1723`.

## Follow-ups filed (cleans_up / relates E-1721)
E-1734, E-1735, E-1738, E-1739, E-1741, E-1740; E-1202 broadened; E-1648 reused.
All conversion tasks carry a prune-the-memory-on-land note.
