# Endless development tasks

# Show available commands
help:
    @echo "Development:"
    @echo "  just build        Build everything (templ, CSS, Go binaries)"
    @echo "  just install      Build + symlink binaries + install Python CLI"
    @echo "  just dev          Run templ + tailwind watchers for development"
    @echo "  just test         Run Python tests"
    @echo "  just kill         Kill any running endless-go serve process"
    @echo ""
    @echo "Build (individual):"
    @echo "  just generate     Generate templ files (one-shot)"
    @echo "  just css          Build CSS (one-shot)"
    @echo "  just go           Build Go binaries only"
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
    @echo ""
    @echo "Demo:"
    @echo "  cd deploy/machine && just demo-sync     Sync to demo machine"
    @echo "  cd deploy/machine && just demo-prepare  Prepare demo machine"

# Resolve the templUI module path and symlink it for CSS imports
_link-templui:
    #!/usr/bin/env bash
    templui_dir=$(go list -m -f '{{"{{"}}.Dir{{"}}"}}' github.com/templui/templui 2>/dev/null)
    if [ -n "$templui_dir" ]; then
        ln -sfn "$templui_dir" internal/web/assets/css/templui
    fi

# Run all watchers for development (templ + tailwind + go server)
dev:
    just tailwind & just templ

# Watch and regenerate templ files, proxy to Go server
templ:
    templ generate --watch --proxy="http://localhost:8484" --cmd="go run ./cmd/endless-go serve"

# Watch and rebuild Tailwind CSS
tailwind: _link-templui
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css --watch

# Build everything for production
build: _link-templui
    templ generate
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css
    go build -o bin/endless-go ./cmd/endless-go

# Build and install everything: Go binaries symlinked to /usr/local/bin,
# Python CLI installed via uv tool in EDITABLE mode (-e). Editable means the
# tool's site-packages contains a pointer to this checkout's src/endless/
# rather than a copy, so subsequent Python source changes go live without
# rerunning install. Run from the main checkout — running from a worktree
# would point the global tool at a transient directory.
install:
    just build
    # E-1367 cleanup: remove pre-consolidation per-binary symlinks. Idempotent.
    rm -f /usr/local/bin/endless-serve /usr/local/bin/endless-hook /usr/local/bin/endless-channel /usr/local/bin/endless-event /usr/local/bin/endless-sandbox /usr/local/bin/endless-tmux /usr/local/bin/endless-session-query
    ln -sfn "$(pwd)/bin/endless-go" /usr/local/bin/endless-go
    uv tool install -e . --force

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
    set -euo pipefail
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
    # Apply this branch's schema-change files to the DB before the
    # (irreversible) ff-merge, backing up first as cheap insurance. The
    # worktree still exists here; `main...HEAD` lists only the change files
    # this branch added since diverging from main. The runner/ helper package
    # is excluded — it is library code, not a change script. If any apply
    # fails, the land aborts before main advances (clean recovery).
    wt="$main_root/.endless/worktrees/e-${tid#[Ee]-}"
    if [ -d "$wt" ]; then
        changes=$(git -C "$wt" diff main...HEAD --diff-filter=A --name-only \
            -- internal/schema/changes/ ':(exclude)internal/schema/changes/runner/')
        if [ -n "$changes" ]; then
            echo "→ Backing up DB before applying schema changes"
            endless db backup
            for f in $changes; do
                case "$f" in
                    *.sql|*.go)
                        echo "→ Applying schema change: $f"
                        endless db apply-change "$wt/$f"
                        ;;
                esac
            done
        fi
    fi
    endless worktree land "$tid"
    echo "→ Refreshing binaries (just build)"
    cd "$main_root"
    just build

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
    with open(out_path, "w") as f:
        json.dump(out, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {out_path}: {sum(len(v) for v in out_hooks.values())} hook entries across {len(out_hooks)} events")
    PY
    git update-index --skip-worktree .claude/settings.json
    echo "claude-settings-init: $worktree_root/.claude/settings.json (skip-worktree set)"

# Provision a per-worktree sandbox DB for self-dev work (E-1281).
#
# Generates wrappers in <worktree>/bin-sandbox/ that redirect endless DB writes
# to ~/.cache/endless/sandboxes/worktree-e-NNN/. Sessions inside the worktree
# pick up the wrappers via the PATH-prepend in <worktree>/.claude/settings.json
# written by 'endless sandbox bind'.
#
# Auto-invoked by 'endless task claim' and 'endless task spawn' when the
# project's .endless/config.json has "worktree_sandbox": true (endless's own
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
    task_id="$(basename "$(pwd)" | sed -n 's/^e-\([0-9][0-9]*\).*/\1/p')"
    if [ -z "$task_id" ]; then
        echo "dev-sandbox-init: cannot parse task ID from $(pwd) (expected .endless/worktrees/e-NNN)" >&2
        exit 1
    fi
    name="worktree-e-${task_id}"
    # Prefer the worktree-built binary so changes to the sandbox subcommand
    # itself are exercised in self-dev. Fall back to PATH for fresh worktrees.
    if [ -x "$(pwd)/bin/endless-go" ]; then
        sandbox_bin="$(pwd)/bin/endless-go"
    else
        sandbox_bin=endless-go
    fi
    "$sandbox_bin" sandbox init --mode empty "$name"
    "$sandbox_bin" sandbox bind "$(pwd)" "$name"

# Run Python tests
test:
    uv run pytest tests/ -v

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

# Generate templ files (one-shot)
generate:
    templ generate

# Build CSS (one-shot)
css: _link-templui
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css

# Build just the Go binary
go:
    go build -o bin/endless-go ./cmd/endless-go

# Run Go tests
test-go:
    go test ./internal/kairos/... ./internal/events/... ./internal/sandboxcmd/... -v

# Kill any running endless-go serve process
kill:
    pkill -f 'endless-go serve' || true

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

