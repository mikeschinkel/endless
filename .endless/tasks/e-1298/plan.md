# E-1298 — Add `just land` recipe for "endless worktree land + refresh binaries"

## Context

After E-1206 landed, the symlinked binary `/usr/local/bin/endless-event` stayed stale because nothing rebuilt the Go binaries after the merge. The result: 5 hours of ledger writes silently bypassed the new commit step.

Root cause restated through the correct lens (per Mike's PRODUCT trigger word): this is a **dev-workflow ergonomics gap, not a product concern**. Mike has to remember two commands today — `endless worktree land E-NNNN` followed by `just build` — and the cost of forgetting is invisible regression of the running CLI against the source on disk. The fix belongs in the developer-tooling layer (Justfile), NOT in the shipped product code (`src/endless/`, `cmd/`, `internal/`).

Beta-tester users will never experience this: they install endless once, they don't rebuild it, and they don't have endless's Justfile. Putting an auto-rebuild step into `worktree_cmd.land_worktree` would bake dev workflow into product code.

## What's actually being changed

Add a new Justfile recipe `land` that wraps `endless worktree land` and follows it with `just build` on success. The recipe also derives the task ID from cwd when called without arguments, so the common case is just `just land` from inside the active worktree.

**Files to modify:**
- `Justfile` — add the `land` recipe.
- `Justfile` help block (line 4-26) — add a one-line entry under "Development" or "Git".

**No changes to:**
- `src/endless/worktree_cmd.py` — product code stays untouched.
- `cmd/`, `internal/` — same.
- `endless` CLI verbs — same.

## Recipe design

```just
# Land a task's worktree (calls `endless worktree land`), then rebuild
# binaries so /usr/local/bin/endless-* picks up any new Go code. If no
# task ID is given, derives it from cwd via `endless worktree current`.
land task_id="":
    #!/usr/bin/env bash
    set -euo pipefail
    tid="{{ "{{task_id}}" }}"
    if [ -z "$tid" ]; then
        tid=$(endless worktree current --json 2>/dev/null \
              | jq -r '.companion.task_id // empty')
        if [ -z "$tid" ]; then
            echo "just land: not inside a task worktree and no task ID given." >&2
            echo "  Usage: just land [E-NNNN]" >&2
            exit 1
        fi
        echo "→ Derived task ID from cwd: $tid"
    fi
    endless worktree land "$tid"
    echo "→ Refreshing binaries (just build)"
    just build
```

### Key details

- `task_id=""` makes the argument optional. Just exposes empty-default args via `{{ "{{task_id}}" }}` template syntax.
- `set -euo pipefail` ensures any sub-step failure aborts the recipe with a non-zero exit. Mike sees the actual error from `endless worktree land` or `just build`.
- The cwd-based derivation uses `endless worktree current --json | jq -r '.companion.task_id // empty'`. jq is already installed (`/opt/homebrew/bin/jq`).
- When called from a worktree without a task companion (foreign worktree), or from outside any worktree, the recipe errors clearly with a usage hint.
- After a successful `endless worktree land`, the cwd's worktree is gone — but `just build` resolves recipes from the Justfile's project root, not cwd, so the build runs against main's checkout (which has just received the merge). Mike's shell may report "dir removed" if he was inside the worktree; that's expected and harmless.
- `just build` is run unconditionally. We could detect "did the just-landed commits touch Go?" but the bash for that adds complexity vs. paying ~5s of mostly-cached build time per land. Optimize later if it bites.

## Help-text addition

Line ~21 of `Justfile`'s `help` recipe, under "Git" or a new "Workflow" section:

```
just land [E-NNNN]  Land a task (derives ID from cwd if omitted), then refresh binaries
```

## Verification

1. **No-arg case**: from inside `.endless/worktrees/e-1298/`, run `just land`. Expect: derives `E-1298` from cwd, lands, prints "→ Refreshing binaries", `just build` completes successfully. `bin/endless-*` modification times advance.
2. **Explicit-arg case**: from main checkout, run `just land E-NNNN` (for some other landable task). Expect: passes through to `endless worktree land E-NNNN`, then rebuilds.
3. **Outside worktree, no arg**: from main checkout (not in a worktree), run `just land`. Expect: error message "not inside a task worktree and no task ID given" with usage hint; exit non-zero.
4. **Land failure**: if `endless worktree land` fails (e.g., dirty user-work on main), the recipe exits before the build runs. Mike sees the actual land error.
5. **Build failure post-land**: if `just build` fails after a successful land, Mike sees the build error; the land's git state is already in place.

## Out of scope

- Changes to `endless` CLI verbs or any product code under `src/endless/` or `cmd/` or `internal/`.
- A generic post-land hook mechanism (sibling of E-1086). Possibly a future task if multiple projects ever need this pattern.
- The architectural question "should Go binaries be live-editable somehow?" That's a real product-shaped concern but explicitly out of scope for this task (Mike's PRODUCT trigger word ruled it out).

## What this does NOT do

- Does not auto-trigger on the bare `endless worktree land` command. If Mike (or a future agent) runs the endless verb directly, no rebuild happens — same as today. The recipe is opt-in via the Justfile alias.
- Does not add detection / conditional logic. Unconditional `just build` after every land.
