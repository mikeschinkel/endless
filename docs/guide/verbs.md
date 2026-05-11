# Verbs

Verbs are the registered action words that may start a task title (E-723, E-947). When you `task add`, Endless validates that the title begins with a registered verb.

## Why

Verb-first titles enforce that every task names an action — "Fix login redirect", "Refactor task_cmd", "Document the guide command" — rather than vague nouns like "Login bug" or "task_cmd". This makes task lists readable at a glance.

## Working with verbs

```bash
endless verb list                # all registered verbs (project + machine layers)
endless verb add <verb>          # register a new verb
endless verb remove <verb>       # remove (with confirmation)
```

Verbs are stored in `verbs.json` at the project root and a machine-layer copy. Project verbs auto-commit to main on `worktree land` (E-1141).

## When `task add` rejects a title

If you try:

```bash
endless task add "Logging system improvements"
```

and Endless refuses because "Logging" isn't a verb, you have three options:

1. **Pick an existing verb.** Run `endless verb list`, choose one that fits — e.g., `endless task add "Improve logging system"` ("Improve" is registered).
2. **Register a new verb.** If your verb genuinely doesn't exist in the list and you want to add it: `endless verb add Logging`. Use sparingly — the verb list is a contract for readability, not a free namespace.
3. **`--force` bypasses validation.** Don't habituate to this. It's an escape hatch, not a workflow.

## Registering responsibly

Before adding a verb:

- Check `endless verb list` — there may be a close synonym already.
- Confirm with your user if you're not sure the new verb adds value.
- Prefer verbs that work for many tasks over one-off verbs.

## See also

- `endless guide tasks` — `task add` and title validation
- `endless guide decisions` — verb additions sometimes warrant a decision record
