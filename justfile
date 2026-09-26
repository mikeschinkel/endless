# Endless development tasks

# Show available commands
help:
    @echo "Development:"
    @echo "  just build        Build everything — the Go binaries (alias: just go)"
    @echo "  just install      Build + symlink binaries + install Python CLI"
    @echo "  just test         Run Python tests"
    @echo "  just verify [E-NNNN]  Run a task's verify suite in its worktree (derives ID like 'just land')"
    @echo ""
    @echo "Workflow:"
    @echo "  just land [E-NNNN]  Land a task (derives ID from cwd if omitted), then refresh binaries"
    @echo ""
    @echo "Git:"
    @echo "  just git-commit \"msg\"  Export DB + commit"
    @echo "  just git-push \"msg\"    Export DB + commit + push"
    @echo ""
    @echo "Database:"
    @echo "  just db-export    Export project data to .endless/data.sql"
    @echo ""
    @echo "Guide docs:"
    @echo "  just guide-check    Validate command->section map coverage (pre-land gate)"
    @echo "  just guide-index    Rebuild the cross-reference block in docs/guide/index.md"
    @echo "  just guide-scaffold Print the skeleton for /regenerate-guide"
    @echo "  just lifecycle-check  Validate the status-lifecycle diagram is current (pre-land gate)"
    @echo "  just lifecycle-index  Rebuild docs/status-lifecycle.mmd from internal/taskstatus"
    @echo ""
    @echo "Demo:"
    @echo "  cd deploy/machine && just demo-sync     Sync to demo machine"
    @echo "  cd deploy/machine && just demo-prepare  Prepare demo machine"

# E-1939 excised the web dashboard, so no codegen step is left and the
# aggregate build is exactly `just go`. Both names are kept because docs,
# hooks and the land recipe each name one of them.
# Build everything for production (currently just the Go binaries)
build: go

# Build and install everything, overloaded by checkout context (E-1036).
#
#   just install              Context-detected:
#                               - From the MAIN checkout: the global install.
#                                 Go binaries symlinked into /usr/local/bin,
#                                 Python CLI installed via `uv tool install -e .`
#                                 in EDITABLE mode (-e). Editable means the tool's
#                                 site-packages points at this checkout's
#                                 src/endless/ rather than a copy, so subsequent
#                                 Python changes go live without reinstalling.
#                               - From a WORKTREE: the worktree-SCOPED install.
#
#   just install worktree     Force the worktree-SCOPED install explicitly
#                             (must be run from a worktree). `just install
#                             --worktree` is accepted as the same thing.
#
# The scoped install NEVER touches the global symlink or the global uv tool —
# scoping is achieved purely by injection (XDG + the worktree's own bin/ and
# sandbox) via the existing recipes (go-work-init + build + dev-sandbox-init +
# claude-settings-init). This turns the `just install`-from-a-worktree footgun
# (which used to silently repoint the system-default toolchain at transient
# worktree code — live incident 2026-06-15) into the right outcome.
#
# What a scoped install no longer scopes: Claude HOOKS. E-2166 removed the
# per-worktree hook override, so hooks run the installed binary in a worktree
# exactly as they do everywhere else (ED-1596). A worktree's bin/endless-go
# still serves its CLI and sandbox work; it just no longer serves hook events.
#
# Hardening: warns loudly if /usr/local/bin/endless-go already resolves into a
# worktree, surfacing an existing mispointed symlink instead of letting it fester.

# Build and install; global from main, scoped from a worktree (E-1036).
install mode="":
    #!/usr/bin/env bash
    set -euo pipefail
    mode="{{mode}}"
    case "$mode" in
        ""|worktree|--worktree) ;;
        *)
            echo "install: unknown argument '$mode' (expected: worktree)" >&2
            exit 1
            ;;
    esac
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    is_worktree=false
    [ "$git_dir" != "$git_common_dir" ] && is_worktree=true
    # Second-layer hardening (all paths): if the global symlink already points
    # into a worktree, the system-default endless-go is serving transient code.
    if [ -L /usr/local/bin/endless-go ]; then
        target="$(readlink /usr/local/bin/endless-go)"
        case "$target" in
            */.endless/worktrees/*)
                echo "WARNING: /usr/local/bin/endless-go points into a worktree:" >&2
                echo "    $target" >&2
                echo "  The system-default endless-go is serving transient worktree code." >&2
                echo "  Run 'just install' from the main checkout to repoint it at main." >&2
                ;;
        esac
    fi
    # Decide scoped vs global: explicit `worktree` arg, or auto-detected worktree.
    if [ "$mode" = "worktree" ] || [ "$mode" = "--worktree" ]; then
        if [ "$is_worktree" = false ]; then
            echo "install worktree: must run from a worktree, not main." >&2
            exit 1
        fi
    elif [ "$is_worktree" = true ]; then
        echo "→ Worktree checkout detected — performing worktree-scoped install."
        echo "  /usr/local/bin and the global uv tool are left untouched."
    else
        # Main checkout, no explicit arg: the global install.
        just build
        # E-1367 cleanup: remove pre-consolidation per-binary symlinks. Idempotent.
        rm -f /usr/local/bin/endless-serve /usr/local/bin/endless-hook /usr/local/bin/endless-event /usr/local/bin/endless-sandbox /usr/local/bin/endless-tmux /usr/local/bin/endless-session-query
        ln -sfn "$(pwd)/bin/endless-go" /usr/local/bin/endless-go
        uv tool install -e . --force
        exit 0
    fi
    # Worktree-scoped install: bundle the existing worktree recipes and NEVER
    # run `ln -sfn` or `uv tool install`. dev-sandbox-init runs before
    # claude-settings-init so the latter sees the seeded sandbox.
    just go-work-init
    just build
    just dev-sandbox-init
    just claude-settings-init
    echo "install: worktree-scoped setup complete for $(pwd)"
    echo "  Wired: go.work, bin/*, .claude/settings.local.json. Global /usr/local/bin untouched."

# Land a task's worktree (calls `endless worktree land`), then rebuild
# binaries so the symlinked /usr/local/bin/endless-* binaries pick up
# any new Go code committed in the just-landed branch.
#
# Task ID derivation, in order:
#   1. Explicit arg: `just land E-NNNN`
#   2. `endless-go tmux active-id` — DB-backed session→task binding. Same
#      source tmux's status row uses, so it's authoritative wherever
#      tmux is running.
#   3. Path-pattern match on cwd (`.endless/worktrees/e-NNN`). Cheap
#      fallback when outside tmux.
#
# We deliberately do NOT consult `.endless/worktree.json` — that
# companion file can go stale and disagree with the DB (see also a
# follow-up task to audit its other readers).
#
# This recipe is the canonical way to land while developing endless
# itself. Mike-only / dev-workflow ergonomics; product code (Python in
# src/endless/, Go in cmd/ and internal/) does NOT auto-rebuild on
# `endless worktree land` because beta-tester users never rebuild.
land task_id="":
    #!/usr/bin/env bash
    # No `set -e` / `set -o pipefail` (house rule): this recipe must OBSERVE a
    # non-zero exit from the land rather than die at it — main may have advanced
    # before the failure, and the binaries then have to be refreshed to match.
    # `set -u` is kept; it catches genuine bugs without hijacking control flow.
    set -u
    tid="{{task_id}}"
    if [ -z "$tid" ]; then
        # Prefer the DB-backed source. Works from any cwd — main, the
        # worktree, or even outside the project — as long as the shell
        # is running in a tmux pane whose session has an active task.
        if tid=$(endless-go tmux active-id 2>/dev/null) && [ -n "$tid" ]; then
            echo "→ Derived task ID from session: $tid"
        elif [[ "$(pwd)" =~ /\.endless/worktrees/e-([0-9]+)(/|$) ]]; then
            tid="E-${BASH_REMATCH[1]}"
            echo "→ Derived task ID from cwd: $tid"
        else
            echo "just land: no active session task and not inside a task worktree." >&2
            echo "  Usage: just land [E-NNNN]" >&2
            exit 1
        fi
    fi
    # Capture main's checkout path BEFORE the land removes the worktree.
    # If cwd is the worktree being landed, the recipe's directory will
    # vanish mid-execution and any subprocess that consults cwd (just,
    # go, etc.) will fail with "no such file or directory" (os error 2).
    # Computing main_root first and cd'ing there after the land sidesteps
    # that race.
    main_root=$(cd "$(dirname "$(git rev-parse --git-common-dir)")" && pwd)
    if [ -z "${main_root}" ]; then
        echo "just land: could not resolve the main checkout." >&2
        exit 1
    fi
    # Schema changes are NO LONGER applied here. `endless worktree land` applies
    # them itself, between the ff-merge and the record-landing step (E-1941).
    # They used to run at this point — before the irreversible merge — and on
    # 2026-08-10 the apply succeeded, the merge then failed, and the real DB was
    # left migrated to a schema no installed binary could read: session tracking
    # froze machine-wide and recovery needed a hand-rolled restore. Applying
    # after main advances inverts that: the DB merely lags landed code, which a
    # re-run fixes.
    wt="$main_root/.endless/worktrees/e-${tid#[Ee]-}"
    if [ -d "$wt" ]; then
        # E-1709: rebuild the worktree's endless-go up-front, BEFORE the
        # apply-change and record-landing steps below consume it. Both steps
        # run the worktree binary against the REAL DB, and endless-go asserts
        # tasktype.VerifyIntegrity on connect. A worktree rebased onto a newer
        # main (new schema/enum, e.g. the brainstorm task_type) but not rebuilt
        # would land with a STALE binary whose embedded enums no longer match
        # the real DB's task_types rows, failing the integrity check and
        # blocking the land (the E-1664 guard only checks the binary is
        # PRESENT, not CURRENT). Unconditional because the skew fires even
        # when THIS branch adds no schema change, as long as main's DB moved
        # ahead of the worktree binary.
        #
        # E-1941: the behind-base refusal lives in `endless worktree land`, NOT
        # here. A duplicate pre-check once lived at this point and had to be
        # fixed twice for the same bug (it counted ledger auto-commits, which
        # land on main constantly and cannot affect a binary, so it refused
        # nearly every land). It also pre-empted the Python refusal, whose
        # message is the useful one — it names the rewritten-history case and
        # E-1943. One rule, one home. The cost is a wasted `just go` on the rare
        # genuine refusal, which is cheaper than a second copy of the rule.
        echo "→ Rebuilding worktree endless-go before land (just go)"
        ( cd "$wt" && just go )
        go_rc=$?
        if [ "${go_rc}" -ne 0 ]; then
            echo "just land: worktree build failed; aborting before main" >&2
            echo "  advances (nothing has been merged or migrated)." >&2
            exit "${go_rc}"
        fi
    fi
    # E-1664: binary selection for the land's schema-apply and record-landing
    # steps is enforced inside `endless worktree land` itself — for a self_dev
    # land it always uses the worktree's endless-go (whose embedded schema/enums
    # match the rows it is applying), and fails loudly if that build is missing.
    # No PATH-prepend needed here (this supersedes E-1660's per-call PATH hack,
    # which silently fell back to the stale global when unbuilt). The up-front
    # `just go` above guarantees that build is not just present but CURRENT, so
    # the guard's present-check is satisfied by a fresh binary (E-1709).
    #
    # E-1941: MAIN ADVANCING is what obliges a rebuild — not the land's exit
    # code. The land can advance main and still fail afterwards (a schema apply
    # or the record-landing step), and bailing out there would leave the global
    # binaries older than the DB they now have to read: the very skew that took
    # session tracking down. So compare main before and after, refresh whenever
    # it moved, and only then propagate the land's status.
    main_before=$(git -C "$main_root" rev-parse main 2>/dev/null)
    endless worktree land "$tid"
    land_rc=$?
    main_after=$(git -C "$main_root" rev-parse main 2>/dev/null)
    if [ "${main_before}" != "${main_after}" ]; then
        echo "→ Refreshing binaries (just build)"
        ( cd "$main_root" && just build )
        build_rc=$?
        if [ "${build_rc}" -ne 0 ]; then
            echo "just land: main advanced but 'just build' failed; the" >&2
            echo "  installed binaries are older than main. Re-run 'just" >&2
            echo "  build' in ${main_root} before using endless." >&2
            if [ "${land_rc}" -eq 0 ]; then
                exit "${build_rc}"
            fi
        fi
    elif [ "${land_rc}" -eq 0 ]; then
        echo "→ Land reported success but main did not move; skipping rebuild."
    fi
    exit "${land_rc}"

# Generate go.work for the current checkout/worktree (E-996).
#
# go.mod has 'replace ../go-pkgs/X' directives that resolve relative to
# the go.mod's location — works from main, breaks from worktrees. go.work
# overrides those replaces with absolute paths, fixing builds anywhere.
#
# go.work is gitignored (per-developer; absolute paths are local). Run
# this once per fresh clone or worktree. When go.work is present, the
# go.mod replace directives are ignored — but they remain as a fallback
# for anyone without go.work.
go-work-init:
    #!/usr/bin/env bash
    set -euo pipefail
    main_checkout="$(dirname "$(git rev-parse --git-common-dir)")"
    main_checkout="$(cd "$main_checkout" && pwd)"
    go_pkgs_root="$(cd "$main_checkout/.." && pwd)/go-pkgs"
    if [ ! -d "$go_pkgs_root" ]; then
        echo "go-pkgs not found at $go_pkgs_root" >&2
        exit 1
    fi
    rm -f go.work go.work.sum
    go work init
    go work use .
    # Discover go-pkgs sub-modules from go.mod's replace lines (lines with
    # '=>' only — skips the documentation comment that mentions ../go-pkgs)
    # and add each as an explicit go.work replace. Replace (rather than use)
    # avoids 'conflicting replacement' errors with go.mod's relative-path
    # replaces — go.work's replace overrides go.mod's for the same module.
    grep -E '=>\s*\.\./.*go-pkgs/' "$main_checkout/go.mod" \
        | sed -E 's|.*[[:space:]](github\.com/mikeschinkel/[^[:space:]]+)[[:space:]]+=>[[:space:]]*\.\./.*go-pkgs/(.*)$|\1 \2|' \
        | while read -r module sub; do
            sub="${sub%/}"
            target="$go_pkgs_root/$sub"
            if [ -d "$target" ] && [ -f "$target/go.mod" ]; then
                go work edit -replace="${module}=${target}"
            else
                echo "warning: $target not found for $module, skipping" >&2
            fi
        done
    echo "go.work generated at $(pwd)/go.work"

# Generate the per-worktree .claude/settings.local.json — and strip the hook
# override a run before E-2166 wrote into it.
#
# Hooks are NOT written here. ED-1596: every hook invocation runs the INSTALLED
# endless-go. That binary is already the one ~/.claude/settings.json names —
# `endless setup claude-hook` writes it there for every user and every tracked
# project, with no self_dev gate — and Claude Code applies the user scope in
# every directory, a worktree included. A worktree therefore needs no hooks key
# to be hooked. The block this recipe used to write was a copy of those same
# entries with the binary path swapped for "$(pwd)/bin/endless-go"; the swap was
# the only thing it added, and E-2166 removes it.
#
# Why the pin had to go, not just be repointed: under it the candidate binary
# was ALL that ran — the installed one self-skipped on seeing the override — so
# when the candidate broke nothing was alive to report it. The session went
# unregistered and the error went to a stderr nobody reads. A third of the 96
# pinned binaries were stale, and twice a stale one resurrected dropped schema
# objects and broke session writes machine-wide. Candidate hook code is
# exercised by verify suites driving the real hook binary under the runner's
# temp HOME (.endless/tasks/e-1202, e-1347, e-1661, e-1662, e-1714), which is
# deterministic and isolated; a live session delegating to it bought realism at
# the price of candidate code writing the shared ledger.
#
# Removing the key beats leaving it correct-but-duplicated, because it keeps ONE
# authority for what the hooks are. `endless setup claude-hook` repairs the
# user-scope file in place — adding hook events an older install never gained,
# correcting sync/async flags — and a frozen copy in every worktree would
# contradict each such repair until something swept all of them again.
#
# The LOCAL file, not the tracked one (E-1347). Writing the override into
# .claude/settings.json made it a tracked modification, which had to be hidden
# with 'git update-index --skip-worktree' — and that bit makes git refuse to
# check out any commit that CHANGES the file, so a single commit to
# .claude/settings.json on main blocked the rebase in every live worktree at
# once. settings.local.json is git-ignored, so nothing can collide.
#
# Verified against Claude Code 2.1.236 when the two were split: 'hooks' entries
# CONCATENATE across scopes and 'env' merges per-key with the local file
# winning. Neither the committed settings.json nor, now, the local one ships
# hooks, so the effective set is exactly the user scope's; enabledPlugins keeps
# coming from the committed file, read natively rather than copied here.
#
# Writes worktree.bgIsolation:"none" (Claude v2.1.143+) so background agents
# launched in the worktree don't create a nested .claude/worktrees/ under
# endless's tracker worktree. Required by the bg-agent dispatch system; see
# docs/research-2026-06-12-claude-background-agents.md §3.
#
# Idempotent: re-running produces the same file (sorted keys, stable JSON) and
# hand-written keys such as 'permissions' survive. Refuses to run from the main
# checkout, whose .claude/ is the committed one. No longer depends on
# bin/endless-go existing for its OUTPUT — only the legacy skip-worktree de-arm
# below looks for a binary, and does so best-effort.
claude-settings-init:
    #!/usr/bin/env bash
    set -euo pipefail
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    if [ "$git_dir" = "$git_common_dir" ]; then
        echo "claude-settings-init: refusing to run from the main checkout (its .claude/ is the committed one). Run from a worktree." >&2
        exit 1
    fi
    worktree_root="$(pwd)"
    # The worktree's hooks now come entirely from the user scope, so assert they
    # are actually there rather than only that the file is. A missing-file check
    # passed happily for a settings.json with no endless hook in it, which since
    # E-2166 means an unhooked worktree instead of a repointed one.
    user_settings="$HOME/.claude/settings.json"
    if [ ! -f "$user_settings" ]; then
        echo "claude-settings-init: $user_settings not found. Run 'endless setup claude-hook' from main first." >&2
        exit 1
    fi
    # One line because a just recipe body is indentation-delimited: a
    # continuation at column 0 inside the -c string ends the recipe.
    hook_probe='import json,sys; s=json.load(open(sys.argv[1])); sys.exit(0 if any("endless-go" in h.get("command","") for es in (s.get("hooks") or {}).values() for e in es for h in e.get("hooks",[])) else 1)'
    if ! python3 -c "$hook_probe" "$user_settings"; then
        echo "claude-settings-init: $user_settings defines no endless-go hook, so this worktree would run unhooked. Run 'endless setup claude-hook' from main first." >&2
        exit 1
    fi
    mkdir -p .claude
    # A worktree bootstrapped before E-1347 still has the override inside the
    # tracked settings.json behind a skip-worktree bit. Clear that, or the stale
    # copy keeps firing this worktree's binary even though the local file no
    # longer names it.
    # Non-fatal: a stale endless-go predates the subcommand, and a missing
    # de-arm is a worse outcome to abort a bootstrap over than to report.
    endless_go=""
    for cand in "${worktree_root}/bin/endless-go" \
                "$(dirname "${git_common_dir}")/bin/endless-go"; do
        if [ -x "$cand" ]; then endless_go="$cand"; break; fi
    done
    if [ -z "$endless_go" ]; then endless_go="$(command -v endless-go || true)"; fi
    if [ -n "$endless_go" ]; then
        "$endless_go" sandbox claude-settings-repair >/dev/null \
            || echo "claude-settings-init: warning: '$endless_go sandbox claude-settings-repair' failed; a legacy skip-worktree bit may remain on .claude/settings.json" >&2
    else
        echo "claude-settings-init: warning: no endless-go found; skipping the legacy skip-worktree de-arm" >&2
    fi
    # Start from whatever settings.local.json already holds — any hand-written
    # keys — so regenerating preserves them.
    if [ -f .claude/settings.local.json ]; then
        local_json="$(cat .claude/settings.local.json)"
    else
        local_json='{}'
    fi
    python3 - .claude/settings.local.json "$local_json" <<'PY'
    import json, sys
    out_path, local_raw = sys.argv[1:3]
    out = json.loads(local_raw or "{}")
    # Everything already in the local file survives except 'hooks', which is
    # DROPPED (E-2166): this recipe no longer generates one, and a block left by
    # an earlier run would keep firing a possibly-stale worktree binary — now
    # ALONGSIDE the installed one, since the self-skip that used to defer to it
    # is gone too. Popping it here is what makes the sweep a re-run of this
    # recipe rather than separate code.
    had_hooks = out.pop("hooks", None) is not None
    # bgIsolation: "none" stops Claude from creating a nested .claude/worktrees/
    # under endless's tracker worktree when background agents launch (v2.1.143+
    # schema). Required by the bg-agent dispatch system; plain assignment is
    # correct because no other code populates the "worktree" key today.
    out["worktree"] = {"bgIsolation": "none"}
    with open(out_path, "w") as f:
        json.dump(out, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {out_path}: no hooks block (hooks come from the user scope)"
          + ("; removed the stale worktree pin" if had_hooks else ""))
    PY
    echo "claude-settings-init: $worktree_root/.claude/settings.local.json"

# Re-run claude-settings-init across every existing task worktree (E-2166).
#
# The one-time remediation for the population the generator fix cannot reach.
# E-2166 measured 120 worktrees, 96 of them still pinning every Claude hook at
# their own bin/endless-go, about a third of those binaries stale — and a stale
# one aborts before registering the session, auto-registers stray project rows,
# and has twice resurrected dropped schema objects that broke session writes
# machine-wide.
#
# It cannot ride in as a commit, which is why a sweep exists at all: bin/ and
# .claude/settings.local.json are BOTH git-ignored, so neither a rebase nor
# `endless worktree sync` can deliver a fix to them — a rebase cannot carry a
# file git does not track. The sweep writes those files directly, outside git.
#
# There is no sweep-specific logic: each worktree gets the very recipe that
# `.endless/hooks/post-worktree-create.sh` runs at birth, invoked the same way
# (main's justfile, the worktree as working directory). So a swept worktree and
# a newly created one are byte-identical by construction rather than by two
# implementations agreeing.
#
# FAIL-FAST, deliberately: the first worktree it cannot rewrite stops the sweep,
# names the worktree, replays that run's output and exits non-zero, leaving
# every later worktree untouched. A sweep that logged failures and carried on
# would leave an unknown number of worktrees pinned to a stale binary while
# reporting success — the exact silent-degrade shape this task is fixing. Fix
# the named worktree and re-run: the recipe is idempotent, so the ones already
# swept are rewritten to the same bytes.
#
# Worktrees come from `git worktree list`, not a directory glob, so an
# abandoned directory under .endless/worktrees/ is neither swept nor counted as
# a failure. Runs from the main checkout only — it sweeps every worktree, so
# running it from inside one of them would be reaching past its own scope.
claude-settings-sweep:
    #!/usr/bin/env bash
    set -euo pipefail
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    if [ "$git_dir" != "$git_common_dir" ]; then
        echo "claude-settings-sweep: refusing to run from a worktree; it sweeps them all. Run from the main checkout." >&2
        exit 1
    fi
    main_checkout="$(pwd)"
    total=0
    changed=0
    while read -r _ worktree; do
        case "$worktree" in
            "${main_checkout}/.endless/worktrees/"*) ;;
            *) continue ;;
        esac
        total=$((total + 1))
        if ! out="$(just --justfile "${main_checkout}/justfile" \
                        --working-directory "$worktree" \
                        claude-settings-init 2>&1)"; then
            echo "claude-settings-sweep: FAILED on $worktree" >&2
            echo "$out" >&2
            echo "claude-settings-sweep: stopped after $((total - 1)) worktree(s); the rest are untouched." >&2
            echo "  Fix that worktree, then re-run — already-swept worktrees are rewritten identically." >&2
            exit 1
        fi
        if [ "${out#*removed the stale worktree pin}" != "$out" ]; then
            changed=$((changed + 1))
            echo "  unpinned  $(basename "$worktree")"
        fi
    done < <(git worktree list --porcelain | grep '^worktree ')
    echo "claude-settings-sweep: $total worktree(s) visited, $changed unpinned."
    echo "  Restart any live Claude session in an unpinned worktree for it to pick up the change."

# Seed this worktree's sandbox DB for self-dev work (E-1281, relocated by E-1964).
#
# Endless creates the sandbox itself — an empty <worktree>/.endless/sandbox/ —
# at worktree-create time, for every project. What goes IN it is the project's
# business, and this is endless's answer for its own repo: the endless.db a
# self-dev session reads under `--db sandbox`, seeded with the project and
# session rows the CLI needs on first use.
#
# Called from .endless/hooks/post-worktree-create.sh, which is what makes it
# the project's declaration rather than something endless does to everybody.
# Run it by hand for a worktree created before the hook did this, or after
# `sandbox init --force` to rebuild a sandbox DB whose schema has drifted.
#
# No name and no bind: the sandbox is composed from the worktree, and nothing is
# written into the environment (E-1964 deleted the XDG_CONFIG_HOME injection).
# `--db sandbox` resolves the same path through the same resolver.
#
# Recipe must run from a worktree (not main). Refuses otherwise.
dev-sandbox-init:
    #!/usr/bin/env bash
    set -euo pipefail
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    if [ "$git_dir" = "$git_common_dir" ]; then
        echo "dev-sandbox-init: must run from a worktree, not main." >&2
        exit 1
    fi
    # Prefer the worktree-built binary so changes to the sandbox subcommand
    # itself are exercised in self-dev. Fall back to PATH for fresh worktrees.
    if [ -x "$(pwd)/bin/endless-go" ]; then
        sandbox_bin="$(pwd)/bin/endless-go"
    else
        sandbox_bin=endless-go
    fi
    "$sandbox_bin" sandbox init --mode worktree

# Run Python tests
test:
    uv run pytest tests/ -v

# Run a task's verification suite while developing Endless — the self_dev
# counterpart to `just test`.
#
# Thin wrapper over the PRODUCT verb `endless task verify` (E-1603/E-2023). Per
# just-is-dev-only, this recipe holds NO verification logic: discovery of the
# task's .endless/tasks/<id>/verify.toml or verify.sh, the own-task-only
# refusal, per-run temp HOME/XDG isolation, running the suite, CTRF
# normalization, and the pass/fail exit code all live in `endless task verify`
# and the endless-go runner it shells to. This recipe only resolves an id and
# picks a directory.
#
# Task ID derivation is `just land`'s chain, verbatim, so the two verbs behave
# alike and neither needs explaining twice:
#   1. Explicit arg: `just verify E-NNNN`
#   2. `endless-go tmux active-id` — DB-backed session->task binding.
#   3. Path-pattern match on cwd (`.endless/worktrees/e-NNN`).
#   4. Otherwise a usage error. The derived id is ECHOED: a verb that picks a
#      target silently is the same class of problem as a listing that truncates
#      silently.
#
# Having resolved the task it cds into THAT task's worktree and runs there. That
# is what `esu` was doing by hand in the old handoff, and the reason it was in
# the handoff at all: `--db sandbox` routes `endless task verify` to the sandbox
# config context, which selects <worktree>/bin/endless-go (E-1510), so a pre-land
# gate exercises the CANDIDATE runner rather than main's. Exit code passes
# straight through, so `just verify && ...` gates on the result.
verify task_id="":
    #!/usr/bin/env bash
    set -u
    tid="{{task_id}}"
    if [ -z "$tid" ]; then
        if tid=$(endless-go tmux active-id 2>/dev/null) && [ -n "$tid" ]; then
            echo "→ Derived task ID from session: $tid"
        elif [[ "$(pwd)" =~ /\.endless/worktrees/e-([0-9]+)(/|$) ]]; then
            tid="E-${BASH_REMATCH[1]}"
            echo "→ Derived task ID from cwd: $tid"
        else
            echo "just verify: no active session task and not inside a task worktree." >&2
            echo "  Usage: just verify [E-NNNN]" >&2
            exit 1
        fi
    fi
    main_root=$(cd "$(dirname "$(git rev-parse --git-common-dir)")" && pwd)
    wt="$main_root/.endless/worktrees/e-${tid#[Ee]-}"
    if [ ! -d "$wt" ]; then
        echo "just verify: no worktree for $tid at $wt" >&2
        echo "  A suite is a pre-land gate; it runs against that task's candidate tree." >&2
        exit 1
    fi
    cd "$wt" || exit 1
    endless --db sandbox task verify "$tid"

# Guide cross-reference / agent --help map (E-1502).
#
# Deterministic primitives live in src/endless/guide_map.py (graduation-ready);
# these recipes are dev-side wrappers. The semantic mapping of a command to its
# guide section is filled in by the /regenerate-guide slash command, not here.

# Print the skeleton (every command + each section's headers) the LLM fills in.
guide-scaffold:
    uv run python -m endless.guide_map scaffold

# Rebuild the generated cross-reference block in docs/guide/index.md from the
# docs/guide/help/*.md map files. Idempotent.
guide-index:
    uv run python -m endless.guide_map index

# Validate map coverage: every command resolves to a section or an acknowledged
# gap, no dangling section refs, no orphan files, index block in sync. Non-zero
# exit on drift — a pre-land / CI gate. Acknowledged gaps are reported, not failed.
guide-check:
    uv run python -m endless.guide_map check

# Task status lifecycle diagram (E-2018).
#
# The transition table in internal/taskstatus/transitions.go is the source;
# docs/status-lifecycle.mmd is its artifact, and README.md and
# docs/guide/index.md embed that artifact byte-identically. Same shape as
# guide-index/guide-check above, for the same reason: drift stops being
# something a test detects and becomes something that cannot happen.

# Rebuild docs/status-lifecycle.mmd (and its two embedded copies) from the Go
# transition table. Idempotent; leaves the .mmd's hand-written preamble alone.
lifecycle-index:
    uv run python -m endless.lifecycle_map index

# Fail when the committed lifecycle artifacts no longer match the Go table.
# Non-zero exit on drift — a pre-land / CI gate next to guide-check.
lifecycle-check:
    uv run python -m endless.lifecycle_map check

# Build just the Go binary
go:
    go build -o bin/endless-go ./cmd/endless-go

# Build just the land-time migration-only executable (ED-1571, E-2088).
#
# Deliberately NOT part of `just build` / `just go`. This binary exists for one
# moment — a self_dev land whose branch adds a schema change — and `endless
# worktree land` builds it then, from the landing branch, only when there is a
# change to apply. A land carrying no migration never builds it, and neither
# does an ordinary development build.
#
# It is a recipe rather than an inlined `go build` inside the land for the same
# reason `just go` is: the build command has one definition, and the land runs
# the same one a developer would.
migrate-bin:
    go build -o bin/endless-migrate ./cmd/endless-migrate

# Run Go tests across every internal/ package (E-1506: was a hand-maintained
# list of five; the wildcard auto-includes new packages as they grow tests).
test-go:
    go test ./internal/... -v

# Export this project's Endless data (tasks, notes, deps) for version control
db-export:
    #!/usr/bin/env bash
    project_id=$(sqlite3 ~/.config/endless/endless.db "SELECT id FROM projects WHERE path = '$(pwd)'")
    if [ -z "$project_id" ]; then echo "Project not registered in Endless"; exit 1; fi
    sqlite3 ~/.config/endless/endless.db <<SQL > .endless/data.sql
    .mode insert projects
    SELECT * FROM projects WHERE id = $project_id;
    .mode insert tasks
    SELECT * FROM tasks WHERE project_id = $project_id;
    .mode insert notes
    SELECT * FROM notes WHERE project_id = $project_id;
    .mode insert task_deps
    SELECT * FROM task_deps WHERE
      (source_type = 'task' AND source_id IN (SELECT id FROM tasks WHERE project_id = $project_id))
      OR (target_type = 'task' AND target_id IN (SELECT id FROM tasks WHERE project_id = $project_id))
      OR (source_type = 'project' AND source_id = $project_id)
      OR (target_type = 'project' AND target_id = $project_id);
    SQL
    echo "Exported project $project_id to .endless/data.sql"

# Commit with DB export (usage: just git-commit "message")
git-commit msg:
    #!/usr/bin/env bash
    just db-export
    git add .endless/data.sql
    git commit -m "{{ msg }}"

# Commit and push (usage: just git-push "message")
git-push msg:
    #!/usr/bin/env bash
    just git-commit "{{ msg }}"
    git push


# Put the DO-NOT-EDIT banner on every verify suite that lacks one (E-2090).
#
# The banner names the suite's OWN task, so an agent meets the do-not-edit rule
# when it opens the file rather than after it has edited it — the interlock the
# E-1916 PreToolUse arm enforces. It is inserted below the shebang and touches
# nothing else: no assertion, fixture or message changes.
#
# Idempotent by construction: a suite that already carries the banner is
# skipped, so re-running adds no second copy. tests/test_suite_guard.py asserts
# the invariant this recipe establishes, which is what catches a suite written
# without one rather than requiring anyone to remember to run this.
suite-banner:
    #!/usr/bin/env bash
    set -euo pipefail
    added=0; had=0
    for f in .endless/tasks/e-*/verify.sh; do
        id="$(basename "$(dirname "${f}")")"; id="E-${id#e-}"
        if grep -q '^# ── DO NOT EDIT' "${f}"; then had=$((had + 1)); continue; fi
        awk -v id="${id}" 'NR==1 {
            print
            print "# ── DO NOT EDIT ─────────────────────────────────────────────────────"
            print "# This suite belongs to " id " and records what was true when " id
            print "# landed. Edit it only if you ARE " id ". If your change breaks an"
            print "# assertion here, leave it alone — see .endless/tasks/CLAUDE.md."
            next
        } { print }' "${f}" > "${f}.tmp"
        # Write BACK INTO the original file rather than mv'ing over it. A `mv`
        # replaces the inode, so the suite inherits the temp file's umask mode
        # and silently loses its executable bit — which is how a sweep once
        # left 205 suites at 0644 and the runner, which exec's them, unable to
        # start any of them.
        cat "${f}.tmp" > "${f}" && rm -f "${f}.tmp"
        added=$((added + 1))
    done
    echo "suite-banner: ${added} banner(s) added, ${had} already present"
