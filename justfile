# Endless development tasks

# Show available commands
help:
    @echo "Development:"
    @echo "  just build        Build everything (templ, CSS, Go binaries)"
    @echo "  just install      Build + symlink binaries + install Python CLI"
    @echo "  just dev          Run templ + tailwind watchers for development"
    @echo "  just test         Run Python tests"
    @echo "  just kill         Kill any running endless-serve process"
    @echo ""
    @echo "Build (individual):"
    @echo "  just generate     Generate templ files (one-shot)"
    @echo "  just css          Build CSS (one-shot)"
    @echo "  just go           Build Go binaries only"
    @echo ""
    @echo "Git:"
    @echo "  just git-commit \"msg\"  Export DB + commit"
    @echo "  just git-push \"msg\"    Export DB + commit + push"
    @echo ""
    @echo "Database:"
    @echo "  just db-export    Export project data to .endless/data.sql"
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
    templ generate --watch --proxy="http://localhost:8484" --cmd="go run ./cmd/endless-serve"

# Watch and rebuild Tailwind CSS
tailwind: _link-templui
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css --watch

# Build everything for production
build: _link-templui
    templ generate
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css
    go build -o bin/endless-serve ./cmd/endless-serve
    go build -o bin/endless-hook ./cmd/endless-hook
    go build -o bin/endless-channel ./cmd/endless-channel
    go build -o bin/endless-event ./cmd/endless-event

# Build and install everything: Go binaries symlinked to /usr/local/bin,
# Python CLI installed via uv tool in EDITABLE mode (-e). Editable means the
# tool's site-packages contains a pointer to this checkout's src/endless/
# rather than a copy, so subsequent Python source changes go live without
# rerunning install. Run from the main checkout — running from a worktree
# would point the global tool at a transient directory.
install:
    just build
    ln -sfn "$(pwd)/bin/endless-serve" /usr/local/bin/endless-serve
    ln -sfn "$(pwd)/bin/endless-hook" /usr/local/bin/endless-hook
    ln -sfn "$(pwd)/bin/endless-channel" /usr/local/bin/endless-channel
    ln -sfn "$(pwd)/bin/endless-event" /usr/local/bin/endless-event
    uv tool install -e . --force

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
# paths to point at this worktree's own bin/endless-hook (E-998).
#
# Without this, exercising new hook code in a Claude session requires
# repointing /usr/local/bin/endless-hook at the worktree's binary, which
# affects every other live Claude session on the machine. Claude Code's
# project-level .claude/settings.json takes precedence over the user-level
# config for matching keys (including 'hooks'), so this file scopes the
# override to sessions whose cwd is inside this worktree.
#
# Mirrors the endless-hook entries from ~/.claude/settings.json verbatim
# (event, async flag, args after the binary), then rewrites the binary
# path to "$(pwd)/bin/endless-hook". enabledPlugins from the committed
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
# so that bin/endless-hook exists. The recipe writes the absolute path
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
    # Capture the committed settings.json content (enabledPlugins etc.) before
    # we overwrite the working-tree copy, so we can preserve non-hook keys.
    committed_json="$(git show HEAD:.claude/settings.json 2>/dev/null || echo '{}')"
    python3 - "$user_settings" "$worktree_root" .claude/settings.json "$committed_json" <<'PY'
    import json, sys
    user_path, worktree_root, out_path, committed_raw = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    with open(user_path) as f:
        user = json.load(f)
    committed = json.loads(committed_raw or "{}")
    new_bin = f"{worktree_root}/bin/endless-hook"
    out_hooks = {}
    for event, entries in (user.get("hooks") or {}).items():
        rewritten = []
        for entry in entries:
            new_entry_hooks = []
            for h in entry.get("hooks", []):
                cmd = h.get("command", "")
                if "endless-hook" not in cmd:
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
    out = {k: v for k, v in committed.items() if k != "hooks"}
    if out_hooks:
        out["hooks"] = out_hooks
    with open(out_path, "w") as f:
        json.dump(out, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {out_path}: {sum(len(v) for v in out_hooks.values())} hook entries across {len(out_hooks)} events")
    PY
    git update-index --skip-worktree .claude/settings.json
    echo "claude-settings-init: $worktree_root/.claude/settings.json (skip-worktree set)"

# Run Python tests
test:
    uv run pytest tests/ -v

# Generate templ files (one-shot)
generate:
    templ generate

# Build CSS (one-shot)
css: _link-templui
    tailwindcss -i internal/web/assets/css/input.css -o internal/web/assets/css/output.css

# Build just the Go binaries
go:
    go build -o bin/endless-serve ./cmd/endless-serve
    go build -o bin/endless-hook ./cmd/endless-hook
    go build -o bin/endless-channel ./cmd/endless-channel
    go build -o bin/endless-event ./cmd/endless-event

# Run Go tests
test-go:
    go test ./internal/kairos/... ./internal/events/... -v

# Kill any running endless-serve process
kill:
    pkill -f endless-serve || true

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

