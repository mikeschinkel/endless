# Concern

The recurring agent confusion (needing --db main to write the real ledger from a worktree) is treated here as a product-behavior defect, not an agent-training problem, per the premise that any knowledge gap is ultimately a sub-optimal product behavior.

# Explore (do not rank)

- Clearer signalling: when a command would write to the sandbox because of cwd, say so plainly and name the one-flag escape.
- Intent detection: recognize when a write is about global or cross-project concerns and route or prompt accordingly.
- Make the real-ledger path obvious at the point of use rather than something to recall.
- Whether the sandbox-by-default rule itself should change for certain command classes.

# Why reinforcement is doubted

Memories reinforcing a flag have not reliably changed behavior; a product affordance that makes the right thing the obvious thing is the higher-leverage fix. See the sibling brainstorm on a reactive-memory mechanism as a general fallback for cases where a product fix is not feasible.

# Output

A recommended product change (or set) that prevents this class of confusion, with enough detail to file the implementation task.