Render the spawn handoff from a single template merged with task metadata (id, title) and runtime context (spawning pane id).

The handoff is mostly boilerplate (orient, default interaction rules, closing) — generating it removes the prompt-vs-plan drift entirely since agents no longer write prompts.

Drops the tasks.prompt column, the --prompt flag on task add/update, and reshapes 'endless task prompt' into 'endless task handoff' which renders the template instead of displaying a stored field.
