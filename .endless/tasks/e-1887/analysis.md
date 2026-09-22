## A reproduced instance, and what it rules out (ES-1055, 2026-09-21/22)

Pane %151 rendered the `no Endless session` hint (PaneStatusClaudeNoSession) —
not the dim placeholder — continuously for roughly four weeks, while
`endless session id` returned 1055 and that row's task_id was 1929. The bar
looked broken while every other signal said the session was healthy.

**The status line was not the failing component, and this was not DB
unreachability.** GetPaneStatus returned no error at all. It correctly found no
session owning the pane, because `sessions.process_id` was NULL. The bar was
telling the truth; the lie was upstream, in the writer.

**Mechanism.** Inside a self_dev worktree the global hook binary defers to
`<worktree>/bin/endless-go` so the session dogfoods candidate code. That binary
was built 2026-08-26, predating E-1898/E-1969, which replaced `sessions.process`
(a bare pane string) with `sessions.process_id` (FK into `processes`). Every
PreToolUse fire therefore died inside TouchSession with:

    claude: touching session: upsert session:
    SQL logic error: table sessions has no column named process (1)

Hooks exit 0 by contract, so nothing surfaced — no fault, no bar change, no log
the user would look at. `process_id` stayed NULL and `last_activity` froze.

**Why this matters to the fix as currently scoped.** Recording a fault in
runStatusLine would NOT have caught this instance: there was no error to record
on the read side. The failure was a correct read of state that a silently failing
WRITER never wrote. If the goal is that the bar never blanks without leaving a
trace, the fault recorder has to cover the hook's DB write path too — a hook
whose write fails must record a fault rather than exit 0 in silence. Consider
widening scope, or pairing this with a sibling task on the hook side.

**Secondary gap, same incident.** The foreign-build guard that should have
flagged the situation checks only that the worktree binary is PRESENT, not that
it is CURRENT — the same present-vs-current gap the justfile's E-1709 note
already calls out for land. A stale binary passes the guard, so the warning it
prints ("worktree was not fully provisioned") never fired for the case that
actually bit.

**Confirmed by repair.** Copying main's current binary into `<worktree>/bin/`
fixed it on the next hook fire: `process_id` bound to the `processes` row for
(tmux, live server uuid, %151), the session flipped to `working`, and the bar
rendered `[E-1929] · endless · todo · now · assumed`. No code change was involved
in the repair, which is what confirms the diagnosis.

**Note on the disproven trigger already recorded here.** The description notes
that E-698 schema drift was tested and disproven because a pre-E-698 binary
renders correctly against the post-E-698 main DB. That remains true and is not
contradicted — the drift that bit here was on the WRITE path, in a different
binary (the worktree's), against a later pair of renames.
