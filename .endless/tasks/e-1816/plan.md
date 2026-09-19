# Rewrite the CLAUDE.md lifecycle clause that misreads as a spawn/claim gate

## Problem

The project `CLAUDE.md` "Task status lifecycle" section ends with:

> An agent sets `submitted` …; a human runs `endless task approve <id>` to reach
> `ready`. `ready` therefore provably means *approved-to-implement*, not merely
> *planned*, so background sessions may pick up (claim) only `ready` work and may
> not run `approve`.

The clause "background sessions may pick up (claim) only `ready` work" states a
policy directly beside the mechanism, with nothing marking it as policy. A reader
(human or model) infers that `spawn`/`claim` refuse a non-`ready` task. They do
not: the only status force-gate is `_CLAIM_REQUIRES_FORCE` in
`src/endless/task_cmd.py` = {unverified, confirmed, declined, obsolete, assumed,
completed}. `submitted` (and `unplanned`, `underway`) spawn/claim freely — the
owner deliberately lets spawn accept `submitted` to avoid busy-work status flips.

## Deliverable

Reword the clause so `ready` reads as a state signal + soft policy, never an
enforced gate. Recommended replacement (implementer may refine):

> An agent sets `submitted` …; a human runs `endless task approve <id>` to reach
> `ready`. `ready` provably means *approved-to-implement*, not merely *planned* —
> worth setting so it shows in `session status`. It is a signal, not a spawn
> gate: `spawn`/`claim` accept `submitted` and only force-gate terminal
> statuses. Autonomous background sessions should still prefer `ready` work and
> leave `approve` to a human.

Constraints:
- Do NOT assert enforcement the code doesn't have. Before using words like
  "must"/"cannot" for the approve-is-human rule, verify against the code whether
  `approve` is actually gated for sessions; if it is only a convention, phrase it
  as a convention ("leave to a human"), not a hard block. (This task exists
  precisely because policy was mistaken for mechanism.)
- Keep the canonical status-lifecycle mermaid block untouched — this is prose
  only. If identical prose is mirrored elsewhere (README, docs/guide), grep for
  it and fix every copy so they don't drift.

## Verification

`tests/tasks/e-1816-verify.sh` as the single command (`esu && ./tests/tasks/e-1816-verify.sh`):
- assert the misleading phrasing ("only `ready` work", or the specific removed
  wording) is gone from `CLAUDE.md`;
- assert the clarifying phrasing is present (that `spawn`/`claim` accept
  `submitted` / `ready` is a signal not a gate);
- if the prose was mirrored elsewhere, assert each mirror matches.

## Relation

E-1817 audits CLAUDE.md for exactly this class of unenforced/misleading rule and
proposes a minimized rewrite. If E-1817's pass lands first, fold this fix into
it rather than shipping twice.
