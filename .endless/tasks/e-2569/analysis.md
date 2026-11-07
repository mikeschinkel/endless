## Decisions made with Mike while planning (2026-11-07)

**The check is `go mod tidy -diff`, not a build.** Mike's call: being stricter than main's build is a good thing. An untidy go.mod is the very thing that broke the land, so the check holds go.mod and go.sum tidy, not merely buildable. The one-time cost is that the first Go task to verify after this lands fails until someone runs `go mod tidy` and commits it; that task can fold the tidy in. `go mod tidy -diff` needs Go 1.23 or later, exits 1 when go.mod or go.sum would change, and prints the change as a unified diff. It operates on the single module and ignores go.work, so the check sets GOWORK=off for certainty and documents that it is belt and braces.

**Relative replace paths are re-rooted, not worked around.** Mike's proposal: rewrite each relative replace path so it resolves where main's go.mod would resolve it, and check the result is a real directory. Go's `-modfile` flag makes this clean: the check writes a copy of go.mod into a temporary directory, rewrites every `=> ./x` or `=> ../x` replace path to the absolute path it would have from the module's counterpart directory in the main checkout, copies go.sum beside it (tidy reads the go.sum next to the modfile), and runs `go mod tidy -diff -modfile=<copy>` with the module directory as the working directory. Verified from the E-2357 worktree against the stale go.mod: exit 1 with exactly the one-line go-dt diff; against the fixed go.mod: exit 0. A replace for a module that is not in the require list is left to Go, which tolerates it, so endless's own `go-gitutil` replace, whose path is written for a worktree's depth and does not resolve from main, does not fail the check.

**The gate lives in the verify runner.** A built-in first check in every Go worktree's run, so the agent meets it on its own verify, before the handoff, and the user sees it in the report and the CTRF like any other check. The land needs the user's passing verify at the branch's current commit, so it is protected through the same row. With a module-graph check rather than a compile, the cost on a docs-only task is small.

**Skipped when the main checkout has its own go.work.** Some repos (go-cliutil and go-cfgstore among them) keep a go.work in the main checkout. There main builds in workspace mode too, so nothing is masked relative to it, and a strict check would refuse a go.mod that main accepts. Mike accepted this without fully weighing it and will revisit if it bites.

## Decided without asking

- A Go module is detected as E-2563's writer detects it: a go.mod outside `.endless/`, `vendor/`, `testdata/` and `.git/`. `projects.language` is not consulted. The rule is duplicated in Go because the runner is Go and the writer is Python; it is four names.
- One check row per module found, root and nested, since main builds each on its own.
- The failure is a check row, first in the report and fail-fast, not a refusal before the suite, so it lands where the user reads.
- A `go` older than 1.23 fails the check naming the version needed rather than skipping, because skipping would reinstate the mask silently.

## Rejected alternatives

- `GOWORK=off go build ./...`: main's exact verdict, but no stricter than it, and a compile per run. Rejected for the tidy check at Mike's direction.
- A temporary checkout of the branch beside the main checkout, at its depth: reproduces main exactly, including relative replaces, but costs a checkout and a build per run and leaves a directory beside the user's repositories if a run dies. The -modfile rewrite gets the same resolution for free.
- Dropping the relative replace directives from endless's go.mod: would make the plain check work here, but changes the go-pkgs workflow (main would build against published versions), and is repo work, not product work.
- Wiring the check into the land gate or `worktree land --check` (E-2222): later than the agent's own verify, and E-2222 is underway in another session. The land is already protected by needing a verify at the current commit.
- A line in a project-level `.endless/verify.toml`: no core code, but off by default, so a product user gets the mask from the go.work Endless wrote and nothing to warn them.
