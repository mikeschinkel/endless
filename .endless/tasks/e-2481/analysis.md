Why named sandboxes go rather than only the dead reap hook (user, Q1: A):
nothing in the repo calls run, enter, list, prune or destroy, and inside an
`enter` subshell the Python CLI refuses every Endless command but shell-init,
so the feature cannot exercise Endless. ReapSandboxForWorktree looks for
<cache>/sandboxes/<worktree name>, where worktree sandboxes lived before
E-1964 moved them inside the worktree; it finds nothing now.

Existing directories are left alone and not reported (user, Q2: C): no other
machine has one. This machine's directory holds two leftovers a test run
wrote on 2026-09-18 (e-1 and repo/land-conflict).

Removing Sandbox.Env removes the last place Endless sets XDG_CONFIG_HOME, which
E-2186 had kept only for `enter`.
