# Sandboxes for every project — brainstorm synthesis

## The reframing that did the work

The seed described one word doing two jobs: per-worktree storage (universal) and
`endless.db` routing (endless-only), conflated because the second was implemented
as the delivery mechanism for the first and the first inherited its `self_dev`
gate. That framing is right about the defect but understates the fix.

Mike's correction: the split is not universal-storage vs. endless-only-routing.
It is that a sandbox's *contents* are the project's business. Endless declares it
needs `endless.db`; a hypothetical WhackyWidgets declares it needs `ww.duckdb`.
There is then no Job A and Job B — there is one job, isolated per-worktree state,
and endless is simply one of its clients.

That dissolves the gate rather than relocating it. A downstream project's endless
commands reach the real ledger not because a flag permits it, but because that
project never asked for `endless.db` to be sandboxed. `self_dev` does not gate
sandbox provisioning and does not need a replacement meaning: it keeps the jobs
it already had (pinning the land's binary, applying endless's own schema changes,
suppressing the Go jobs runner, validating `--db`) and stops being consulted in
one place.

## What was decided

Recorded as ED-1554; implementation is E-1964.

**Location.** `<worktree>/.endless/sandbox/`. The XDG cache root was wrong on its
own terms — cache is defined as deletable at any moment without loss, which is
false of files a task needs in order to be verified and hostile if a project puts
credentials there. In-tree wins over any other out-of-tree location because these
files get opened and edited in the IDE that already has the worktree open.

**Lifetime equals the worktree's.** Deliberately accepted, not a side effect. It
makes orphans structurally impossible instead of managed: worktrees are already
reaped after a grace period, so no prune, no orphan scan, no manual destroy.
Anything that must outlive the worktree does not belong in a sandbox — commit it.

**Contents: empty dir, project's hook fills it, no seeding.** Endless cannot know
which of a main checkout's files a task needs, and copying is the exact failure
sandboxes exist to prevent — a worktree quietly pointed at the real database or a
live account. The `post-worktree-create` hook is the declaration; no config
manifest in this increment.

**Resolution by composition, not injection.** In-tree placement makes the path a
fixed relative segment from the worktree root, so no environment variable is
needed and none is introduced. Behind one resolver function, so a project-declared
override can be added later as a branch inside it rather than a new mechanism.
That seam earns its keep on day one for the rare project whose task-exercising
files are themselves a git repo.

**The `XDG_CONFIG_HOME` injection is deleted**, and `sandbox bind` with it.

**Out-of-tree override, per project.** For strict build hermeticity, `git clean`
in CI, or monorepo tooling that walks everything. Project level, never worktree
level.

**Migration by rename.** 126 existing sandboxes, 102 MB, attached to 137 live
worktrees. Both paths under `$HOME`, so this is a rename per worktree, plus
provision-on-miss in the resolver as backstop.

## Corrections made during the discussion, worth preserving

**"Hijack" was imprecise, then earned.** `XDG_CONFIG_HOME` is a standardized
affordance used as intended — per-process, a process finds its own config. What
endless does is write it into Claude Code's session-wide `env` block, so every
child of the session inherits it for the session's life. The affordance is
per-invocation configuration; it was used as ambient policy. Defensible while
endless owned the only repo it applied to; short-sighted as a design that would
have to generalize, and chosen because Python already read that variable.

**The injection may already be vestigial.** `apply_db_choice("sandbox")` computes
the config dir directly and never reads `XDG_CONFIG_HOME`, and E-1429's gate
refuses to run in a self-dev worktree without an explicit `--db`, so the
XDG-derived default is unreachable there. If that holds, this task deletes the
injection rather than teaching Python anything new. One case still needs checking
before deletion: a binary invoked with a cwd outside the worktree.

**"Don't migrate" was wrong.** Proposed on the grounds that sandbox contents are
throwaway. They are throwaway relative to the real ledger, not to a session
mid-task, and skipping migration would have knee-capped 126 sandboxes at once.

**A rejected framing: naming.** Argued "sandbox" describes isolation-from-real and
therefore misdescribes the storage job. Wrong — isolation-from-real describes all
of it. The distinction did not exist.

**Two lines of speculation abandoned.** Sandboxes holding throwaway git checkouts
(over-read from an agreement that "sandbox" meant more than config); and seeding a
new sandbox from the main checkout.

## Deliberately out of scope

- **No sandbox for the main checkout.** Sandboxes stay 1-to-1 with worktrees and
  worktrees 1-to-1 with tasks. Collapsing the two is what keeps this simple, and
  the complexity of separating them was already judged YAGNI.
- **No declaration manifest.** The hook is the declaration.
- **The `just verify` asymmetry.** The recipe runs main's Python against the
  worktree's sandbox, so a Python-side change is verified against landed code
  while a Go-side change is verified against candidate. Real, and ruled WONTFIX:
  the fix is complex and Python is slated for a port to Go, so fixing it in
  Python only delays the port.
- **The E-1958 blocks E-1957 relation.** Left in place; determining whether it is
  real costs more than waiting for this to land.

## Follow-ups spawned

- **E-1964** — the implementation, with ordering, the pre-deletion check, and
  migration scale in its analysis.
- **ED-1554** — the placement and lifetime decision.
- **E-1963** — sandbox tooling rot found while looking: every error string names
  an `endless-sandbox` binary that does not exist, the ephemeral non-worktree
  sandbox path (`run`, `enter`, `prune`) is live code despite having been judged
  YAGNI and dropped from the documented surface, and CLAUDE.md instructs a
  command that cannot be run. Includes the observation that deleting `run`/`enter`
  strands the E-1162 refusal gate, since nothing else sets `ENDLESS_SANDBOX`.
- **E-1859, reopened to `revisit`** — surfaced by a tangent that turned out to be
  a real defect. Automatic triage never fires from a self-dev worktree, which is
  where nearly every task is filed; the inline path takes no lease and races the
  sweep once that is fixed; child failures go to DEVNULL; the 15-minute interval
  was never specified; and the reset message offers `--keep-status` as an undo
  rather than naming the model call it would skip. Five fixes, ordered, in its
  plan.
