# Requirements captured (inputs to the design)

## Land / pre-merge lifecycle hook (surfaced by E-1664, 2026-06-26)

E-1664 made a self_dev land use the worktree's endless-go binary as an invariant,
but deliberately left a related fragility for this model to solve: `just land`'s
schema `backup` + `apply-change` loop lives in the Justfile as separate
`endless db apply-change` process invocations, each PATH-pointed at the worktree
binary (E-1510). Being separate processes, no per-invocation mechanism can be
"set once" across them — the only real de-fragilization is consolidating land's
steps into one process that invokes a project-configured script at a defined point.

So the extension model should include a LAND (pre-merge) lifecycle hook point — a
sibling to the shipped `.endless/extensions/use.sh` (E-1014) and the proposed
worktree-init hook (E-1086) — fired before the ff-merge advances main (so a failed
hook aborts cleanly, preserving today's "abort before main moves" safety). Endless
itself would configure it to run its own schema backup + apply-change against its
DB; downstream projects configure their own or none. This keeps the self-dev
schema concern out of shipped `endless worktree land` and collapses `just land`
toward a thin build+call+rebuild wrapper.

Not built under E-1664 (it belongs to this model's scope); recorded here so the
three-tier design accounts for a pre-merge hook point and its failure semantics.
