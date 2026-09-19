go.mod has 'replace github.com/mikeschinkel/go-cfgstore => ../go-pkgs/go-cfgstore' (and similar for go-doterr, go-dt, go-cliutil, go-logutil). The relative '../' resolves correctly from main repo (~/Projects/endless → ~/Projects/go-pkgs/) but not from inside a worktree (~/Projects/endless/.endless/worktrees/<id> → ~/Projects/endless/.endless/worktrees/go-pkgs/ which doesn't exist).

Reproducer: cd .endless/worktrees/<id> && go build ./cmd/endless-hook

Options:
1. Symlink as worktree-bootstrap step: create .endless/worktrees/go-pkgs -> ~/Projects/go-pkgs (would let all sibling worktrees share via the parent dir).
2. Update go.mod replace paths to use a more portable scheme.
3. Document a manual symlink convention until E-971 automates worktree setup.

Likely (1) baked into E-971's worktree-creation hook. Until then, manual symlink works.