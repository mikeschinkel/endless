Worktrees under .endless/worktrees/<id>/ break Go builds because go.mod uses 'replace github.com/mikeschinkel/X => ../go-pkgs/X', which is relative to the go.mod's location. From a worktree the path resolves to .endless/worktrees/go-pkgs/X, which doesn't exist.

Workaround in use today: a one-shot symlink at .endless/worktrees/go-pkgs -> /Users/mikeschinkel/Projects/go-pkgs makes 'just build' and 'go test' succeed inside any e-NNN worktree. This was created during E-989 implementation. The symlink is outside any worktree's tree (it sits in .endless/worktrees/ alongside the e-NNN dirs) so no .gitignore change is needed and it is invisible to all worktrees.

Decide a durable answer:

Options:
1. Keep the symlink permanently. Document in worktree setup notes (when E-971 lands, that doc should call it out). Lowest cost; assumes Mike's go-pkgs lives at a stable path.
2. Use Go workspace files (go.work) with absolute paths or an env-driven path. Requires every dev to maintain go.work but doesn't depend on directory layout.
3. Vendor go-pkgs into the endless repo (vendor/ or a submodule). Heaviest but eliminates the path coupling entirely.
4. Replace 'replace ../go-pkgs/X' with module-proxy versions (publish go-pkgs releases). Cleanest for shared codebases; requires release discipline upstream.

Recommendation pending: depends on whether go-pkgs is intended to stay co-developed (favors option 2) or stabilize as published deps (favors option 4).

Verification (whichever option lands): from .endless/worktrees/<id>, 'just build' and 'go test ./...' both succeed without the manual symlink.

Out of scope: the symlink itself is fine for now. This task is about replacing it.