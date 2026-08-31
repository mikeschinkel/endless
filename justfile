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
# scoping is achieved purely by injection (.claude/settings.json + PATH/XDG) via
# the existing recipes (go-work-init + build + dev-sandbox-init +
# claude-settings-init). This turns the `just install`-from-a-worktree footgun
# (which used to silently repoint the system-default toolchain at transient
# worktree code — live incident 2026-06-15) into the right outcome.
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
    # Worktree-scoped install: bundle the existing injection-based recipes and
    # NEVER run `ln -sfn` or `uv tool install`. dev-sandbox-init runs before
    # claude-settings-init so the latter preserves the env block 'sandbox bind'
    # wrote.
    just go-work-init
    just build
    just dev-sandbox-init
    just claude-settings-init
    echo "install: worktree-scoped setup complete for $(pwd)"
    echo "  Wired: go.work, bin/*, .claude/settings.json. Global /usr/local/bin untouched."

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

# Generate per-worktree .claude/settings.json that overrides hook command
# paths to point at this worktree's own bin/endless-go (E-998, E-1367).
#
# Without this, exercising new hook code in a Claude session requires
# repointing /usr/local/bin/endless-go at the worktree's binary, which
# affects every other live Claude session on the machine. Claude Code's
# project-level .claude/settings.json takes precedence over the user-level
# config for matching keys (including 'hooks'), so this file scopes the
# override to sessions whose cwd is inside this worktree.
#
# Mirrors the endless-go hook entries from ~/.claude/settings.json verbatim
# (event, async flag, args after the binary), then rewrites the binary
# path to "$(pwd)/bin/endless-go". enabledPlugins from the committed
# main-checkout settings.json is preserved so Claude sessions in the
# worktree don't lose plugin enablement.
#
# Also writes worktree.bgIsolation:"none" (Claude v2.1.143+) so background
# agents launched in the worktree don't create a nested .claude/worktrees/
# under endless's tracker worktree. Required by the bg-agent dispatch system;
# see docs/research-2026-06-12-claude-background-agents.md §3.
#
# Idempotent: re-running produces the same file (sorted keys, stable
# JSON). Refuses to run from the main checkout to avoid clobbering the
# committed .claude/settings.json there.
#
# git tracks .claude/settings.json (the main checkout commits enabledPlugins
# via it), so we use 'git update-index --skip-worktree' to mask the
# regenerated content from this worktree's git status without affecting
# main or other worktrees.
#
# Run AFTER `just build` (or `just go` / `just go-work-init` then `just go`)
# so that bin/endless-go exists. The recipe writes the absolute path
# regardless, since hook fire-time cwd is unpredictable.
claude-settings-init:
    #!/usr/bin/env bash
    set -euo pipefail
    git_dir="$(cd "$(git rev-parse --git-dir)" && pwd)"
    git_common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    if [ "$git_dir" = "$git_common_dir" ]; then
        echo "claude-settings-init: refusing to run from the main checkout (would clobber tracked .claude/settings.json). Run from a worktree." >&2
        exit 1
    fi
    worktree_root="$(pwd)"
    user_settings="$HOME/.claude/settings.json"
    if [ ! -f "$user_settings" ]; then
        echo "claude-settings-init: $user_settings not found. Run 'endless setup claude-hook' from main first." >&2
        exit 1
    fi
    mkdir -p .claude
    # Capture the committed settings.json content (enabledPlugins etc.) and
    # the working-tree copy (which may carry an env block from
    # 'endless sandbox bind') so we can preserve non-hook keys from both.
    committed_json="$(git show HEAD:.claude/settings.json 2>/dev/null || echo '{}')"
    if [ -f .claude/settings.json ]; then
        working_json="$(cat .claude/settings.json)"
    else
        working_json='{}'
    fi
    python3 - "$user_settings" "$worktree_root" .claude/settings.json "$committed_json" "$working_json" <<'PY'
    import json, sys
    user_path, worktree_root, out_path, committed_raw, working_raw = sys.argv[1:6]
    with open(user_path) as f:
        user = json.load(f)
    committed = json.loads(committed_raw or "{}")
    working = json.loads(working_raw or "{}")
    new_bin = f"{worktree_root}/bin/endless-go"
    out_hooks = {}
    for event, entries in (user.get("hooks") or {}).items():
        rewritten = []
        for entry in entries:
            new_entry_hooks = []
            for h in entry.get("hooks", []):
                cmd = h.get("command", "")
                if "endless-go" not in cmd:
                    continue
                parts = cmd.split(None, 1)
                tail = f" {parts[1]}" if len(parts) > 1 else ""
                new_h = dict(h)
                new_h["command"] = new_bin + tail
                new_entry_hooks.append(new_h)
            if new_entry_hooks:
                rewritten.append({"hooks": new_entry_hooks})
        if rewritten:
            out_hooks[event] = rewritten
    # Start from committed (enabledPlugins, etc.), overlay working-tree
    # additions (env block from 'endless sandbox bind'), then replace hooks
    # with the freshly-rewritten ones.
    out = {k: v for k, v in committed.items() if k != "hooks"}
    for k, v in working.items():
        if k in ("hooks",):
            continue
        out[k] = v
    if out_hooks:
        out["hooks"] = out_hooks
    # bgIsolation: "none" stops Claude from creating a nested .claude/worktrees/
    # under endless's tracker worktree when background agents launch (v2.1.143+
    # schema). Required by the bg-agent dispatch system; plain assignment is
    # correct because no other code populates the "worktree" key today.
    out["worktree"] = {"bgIsolation": "none"}
    with open(out_path, "w") as f:
        json.dump(out, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {out_path}: {sum(len(v) for v in out_hooks.values())} hook entries across {len(out_hooks)} events")
    PY
    git update-index --skip-worktree .claude/settings.json
    echo "claude-settings-init: $worktree_root/.claude/settings.json (skip-worktree set)"

# Provision a per-worktree sandbox DB for self-dev work (E-1281).
#
# Creates the sandbox DB at ~/.cache/endless/sandboxes/e-NNN[-slug]/ and writes
# XDG_CONFIG_HOME into <worktree>/.claude/settings.json so Claude-spawned
# subprocesses route there. The endless-go binary also self-detects this
# sandbox from cwd (E-1368), so bare-shell `./bin/endless-go ...` inside the
# worktree routes to it without any wrapper or env export.
#
# Auto-invoked by 'endless task claim' and 'endless task spawn' when the
# project's .endless/config.json has "self_dev": true (endless's own
# config does). Run manually for worktrees created by hand or to re-wire after
# moving binaries.
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
    name="$(basename "$(pwd)")"
    case "$name" in
        e-[0-9]*) ;;
        *)
            echo "dev-sandbox-init: cannot derive sandbox name from $(pwd) (expected .endless/worktrees/e-NNN[-slug])" >&2
            exit 1
            ;;
    esac
    # Prefer the worktree-built binary so changes to the sandbox subcommand
    # itself are exercised in self-dev. Fall back to PATH for fresh worktrees.
    if [ -x "$(pwd)/bin/endless-go" ]; then
        sandbox_bin="$(pwd)/bin/endless-go"
    else
        sandbox_bin=endless-go
    fi
    "$sandbox_bin" sandbox init --mode worktree "$name"
    "$sandbox_bin" sandbox bind "$(pwd)" "$name"

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
        } { print }' "${f}" > "${f}.tmp" && mv "${f}.tmp" "${f}"
        added=$((added + 1))
    done
    echo "suite-banner: ${added} banner(s) added, ${had} already present"
