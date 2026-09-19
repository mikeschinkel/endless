# Plan — E-1822: deliver the type-handoff on claim-into-a-live-session

Design captured in the analysis; this is the implementation. Two axes, both now
unblocked (E-1803 landed — its `PostToolUse` `additionalContext` mechanism is the
delivery lever, and it is proven reliable for context *delivery*, which is all
this task needs; the "strong nudge, not a hard gate" caveat was about enforcing
behavior, not delivering context).

## Axis 1 — Content: a shared type-mechanics partial + a claim wrapper

The claim wrapper is the *second* consumer of each type's handoff mechanics
(spawn is the first). Per the analysis's discipline, this second consumer is what
justifies extracting the seam — extract ONLY the lines whose drift between the
spawn and claim renderings would be a bug; do not pre-factor the rest.

1. New shared partial `internal/templatecmd/templates/handoff/_mechanics.tmpl`,
   parsed into every handoff set exactly as `_close` is:
   - In `template.go`, mirror `closePartialName`/`mechanicsPartialName` and the
     `strings.HasPrefix(name, "handoff/")` block that parses the partial so
     `{{template "handoff_mechanics" .}}` resolves.
   - Content = only the genuinely invariant, drift-sensitive mechanics: worktree-
     bound; `--db main` routing inside the worktree; one-session-one-task; and the
     per-type terminal-status/artifact rule. Where a type differs (e.g. research/
     brainstorm end `completed` with a landable ledger artifact; todo/bugfix end
     `unverified`), branch on `.task_type` inside the partial rather than forking
     into five near-identical partials.
2. Wire the existing per-type spawn templates
   (`handoff/{todo,bugfix,epic,research,brainstorm}.md.tmpl`) to include
   `{{template "handoff_mechanics" .}}` in place of the mechanics lines they carry
   inline today. Spawn output must not change in meaning — only its source of the
   invariant lines moves, so spawn and claim can no longer drift on them.
3. New claim wrapper `internal/templatecmd/templates/handoff/claim.md.tmpl` — the
   claim-only arrival framing, then the mechanics partial, then the `_close` tail.
   Arrival block (from the analysis — focused, not thin):
   - you are NOT freshly spawned — you have been running and just claimed this; no
     blank-slate assumption;
   - relocate: `/cd` your working directory into the task's worktree (a spawned
     session is born there; a claimed-in one is not) — backstopped by the existing
     cwd gate that already refuses tools when cwd drifts outside the claimed
     worktree (E-1586);
   - routing: the worktree routes DB access to the sandbox, so real-ledger updates
     need `--db main`;
   - fold the planning you already did into the task's `text` field, not chat.

## Axis 2 — Delivery: `PostToolUse` additionalContext on the live-session claim

4. In `internal/hookcmd/claude.go`, the claim action is already detected in
   `handlePostToolUseSession` (`actionStart`/`scopeTask` → `StartWorkSession`).
   Extend that branch: after `StartWorkSession` succeeds, render the claim handoff
   and return it as `additionalContext` via the `postToolUseResponse` struct
   E-1803 added.
   - Retrofit vs spawn needs no flag: `endless task spawn` pre-claims the task
     BEFORE the target session starts, so its claim never runs as a `PostToolUse`
     inside that session; a `PostToolUse` `endless task claim` is definitionally
     the live-session retrofit. Confirm against `trySpawnBind` (spawn sets
     `@endless_spawned_by` + pre-flips status) that spawn cannot also emit this
     event in the spawned session — guard on the spawn marker if it can.
   - Render in-process: the hook IS the `endless-go` binary, so call the
     `templatecmd` render path (or a thin `monitor` helper) directly rather than
     shelling. Build the same vars the spawn path assembles in
     `render_handoff` (id, title, `task_type`, `worktree_path`, `branch`,
     `children_state`) with `template_name = handoff/claim`.
   - Reuse E-1803's response shape:
     `postToolUseResponse{HookSpecificOutput:{HookEventName:"PostToolUse",
     AdditionalContext: <rendered claim handoff>}}`.

## Verification — tests/tasks/e-1822-verify.sh (fully scripted)

- Go template: `template render handoff/claim` yields the arrival block +
  mechanics + close; `handoff_mechanics` resolves in BOTH a spawn per-type render
  and the claim render (the anti-drift guarantee).
- Go hook unit: a `PostToolUse` payload for `endless task claim <id>` returns
  `additionalContext` carrying the claim handoff; a spawn-marked session does not
  double-deliver.
- Script: build + the two Go suites (fail-fast) + a claim-into-a-live-session
  e2e against the sandbox asserting the handoff text is emitted, plus a guard that
  spawn output is unchanged in meaning.

## Boundary

- Content: extract ONLY drift-sensitive invariants into `_mechanics`; not a
  wholesale template rewrite.
- Delivery does not alter spawn (spawn still renders per-type templates; they now
  include the shared partial).
- Enforcing that the agent actually relocates/uses `--db main` is not this task's
  job — the cwd gate (E-1586) already backstops relocation; this task delivers the
  context so the agent knows to.
