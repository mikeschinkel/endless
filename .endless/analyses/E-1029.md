Mike (2026-04-30, during E-971 foundation work) directed extracting the gitutils package from gomion into a standalone go-pkgs module, adding worktree CRUD (add/remove/list --porcelain), and using it from endless via the existing 'replace' pattern.

Source: ~/Projects/gomion/gommod/gitutils/ (~28 Go files, ~152KB).

External deps (clean — no gomion-internal):
- github.com/mikeschinkel/go-dt + go-dt/dtx
- github.com/mikeschinkel/go-diffutils
- golang.org/x/mod/semver

All already available at ~/Projects/go-pkgs/.

Existing functionality includes:
- run_git.go (GitRunner shell-out)
- repo.go (RevParse, Status, IsDirty, UpstreamState, Tags, Diff, Push, CreateTag, etc.)
- cached_worktree.go (specialized for nextver caching, NOT a general worktree manager)
- diff*.go, status*.go, staging.go, ignored_files.go, etc.

Add new:
- Worktree type
- Worktree.Add(branch, path, base)
- Worktree.Remove(path)
- Worktree.List() — parses 'git worktree list --porcelain'

Then add 'replace github.com/mikeschinkel/go-gitutil => ../go-pkgs/go-gitutil' to endless's go.mod.

Mike-side followup (NOT in this task; he'll do later): update gomion to import from go-pkgs/go-gitutil instead of in-tree. Until then, two copies coexist.