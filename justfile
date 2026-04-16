# Endless development tasks

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

# Build and install everything (Go binaries symlinked to /usr/local/bin, Python CLI installed)
install:
    just build
    ln -sfn "$(pwd)/bin/endless-serve" /usr/local/bin/endless-serve
    ln -sfn "$(pwd)/bin/endless-hook" /usr/local/bin/endless-hook
    uv tool install -e . --force

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

# Kill any running endless-serve process
kill:
    pkill -f endless-serve || true

# Export this project's Endless data (plans, notes, deps) for version control
db-export:
    #!/usr/bin/env bash
    project_id=$(sqlite3 ~/.config/endless/endless.db "SELECT id FROM projects WHERE path = '$(pwd)'")
    if [ -z "$project_id" ]; then echo "Project not registered in Endless"; exit 1; fi
    sqlite3 ~/.config/endless/endless.db <<SQL > .endless/data.sql
    .mode insert projects
    SELECT * FROM projects WHERE id = $project_id;
    .mode insert plans
    SELECT * FROM plans WHERE project_id = $project_id;
    .mode insert notes
    SELECT * FROM notes WHERE project_id = $project_id;
    .mode insert task_dependencies
    SELECT * FROM task_dependencies WHERE
      (source_type = 'task' AND source_id IN (SELECT id FROM plans WHERE project_id = $project_id))
      OR (target_type = 'task' AND target_id IN (SELECT id FROM plans WHERE project_id = $project_id))
      OR (source_type = 'project' AND source_id = $project_id)
      OR (target_type = 'project' AND target_id = $project_id);
    SQL
    echo "Exported project $project_id to .endless/data.sql"

# Commit with DB export (usage: just git-commit "message")
git-commit msg:
    just db-export
    git add .endless/data.sql
    git commit -m "{{ msg }}"

# Commit and push (usage: just git-push "message")
git-push msg:
    just git-commit "{{ msg }}"
    git push
