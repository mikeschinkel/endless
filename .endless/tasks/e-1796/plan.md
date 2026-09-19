# Plan: Document two Endless workflow practices in the session guide

## Scope

Guide-only (`docs/guide/*`). No `README.md` change — the README restructure
(What / Install / Start, how-to-use moves to the guide) is a separate session's
work, and the README's user-facing bridge sentence + guide link is that
session's job. No `link`-command / over-blocking gate work — that is a separate
task. No internal task-ID (`E-NNNN`) citations in any shipped-doc text.

## Practice A — a session closes the discovery loop (not just implementation)

Home: `docs/guide/index.md`. This is the anti-over-index guardrail: the happy
path today ends at "implement → unverified → land," so an agent reads
"one worktree per task" as "one session, one task" and stops. Practice A extends
the happy path and reconciles it with worktree isolation.

1. In `## The happy path`, immediately after the closing land paragraph
   ("When implementation is verified, land the work with `endless worktree land`
   …"), add a short subsection `### After the task: close the discovery loop`
   stating:
   - The steps above cover your **bound task**; a session's job does not end at
     land.
   - Before winding down, walk `endless session status`, follow blocker chains
     upstream, do the discovery work (re-scope blockers, work out an actual
     slice rather than parking it), and **file + plan** the follow-ups that
     surfaced.
   - Reconcile with worktree isolation explicitly: one-worktree-per-task governs
     **implementation isolation**, not session scope. Each follow-up's
     *implementation* still gets its own worktree/task (`task claim` /
     `task spawn`); doc/code follow-ups need their own worktree because a landed
     worktree can't carry new work.
2. In `## Important notes (always relevant)`, add one bullet reinforcing it:
   a session drives its task's follow-ups, not just its implementation — after
   landing, walk `session status`, chase blockers, file + plan follow-ups;
   one-worktree-per-task is implementation isolation, not "one session, one
   task."

## Practice B — self-unblock by extracting the slice

Home: `docs/guide/tasks.md`, `## Relations between tasks` →
`### When to use each relation type` (canonical relation guidance). Written as a
manual discipline/judgment so it stays compatible with any future tool-side
gating.

3. After the decision bullets (…"They're related, no firm dependency" →
   `relates_to`), add a short "Before you accept a block" paragraph:
   - When A would be `blocked_by` B, first work out *exactly* what A needs from
     B — often a small, load-bearing slice with no dependency on B's own
     transitive blockers.
   - If that slice is independent, fold it into A (or extract it as a tiny
     prerequisite) so A can move now; keep the surface compatible so B later
     *generalizes* rather than rewrites. Link A `relates_to` B, not
     `blocked_by`.
   - Prefer unblocking within the task over waiting for the blocker's whole
     scope.
4. Reinforce in `index.md` `## Blocking semantics` with one closing line pointing
   at the tasks.md guidance (check for a small independent slice before
   accepting a block; extract and relate instead of block — see
   `endless guide tasks`).

## Must not touch

- The canonical mermaid block in `index.md` (between the
  `BEGIN/END canonical:docs/status-lifecycle.mmd` markers) — must stay
  byte-identical; `tests/tasks/e-1648-verify.sh` asserts the three copies match.
- `README.md`, `CLAUDE.md`.

## Verification — `tests/tasks/e-1796-verify.sh`

Model on the `e-1577` / `e-1648` harness (pass/fail per check, summary, exit
0/1/2). Fold every check into this one script so the single verify command is
`esu && ./tests/tasks/e-1796-verify.sh`.

- `index.md` happy path contains the discovery-loop reconciliation prose
  (key phrases: "discovery loop", "one-worktree-per-task" / "implementation
  isolation", "session status").
- `index.md` Important-notes has the follow-ups bullet.
- `tasks.md` relations section contains the extract-the-slice guidance
  (phrases: "slice", "`relates_to`", "`blocked_by`").
- The added practice prose contains no `E-1708` / `E-1796` citation
  (grep the specific added lines).
- `endless guide` (index) and `endless guide tasks` render non-empty, exit 0.
- `endless guide --list` slug set is unchanged (no new section introduced).
- Fail-fast regression: run `tests/tasks/e-1648-verify.sh` (the mermaid
  single-source stays byte-identical after the `index.md` prose edits).

## Cross-reference map

No new section slugs or commands are introduced, so the generated
"Where to look" map is unaffected. Run `/regenerate-guide` only to confirm no
drift, and add the two topic rows (drive-follow-ups-in-session,
extract-a-slice-to-self-unblock) if the generator supports topic entries.
