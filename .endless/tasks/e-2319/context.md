Agreed in the brainstorm E-2230. Today accepting a finished findings task (research, brainstorm) needs the agent twice: follow-ups are filed during the discussion, so finishing the task gets lost in deciding what to file, and `task complete` requires `--outcome` even when the task already has one, so the user has to prompt the agent again just to complete it.

The agreed shape:
- `task complete` on an `unreviewed` task accepts the outcome it already has. Reaching `unreviewed` already requires an outcome, so `unreviewed` is the agent declaring that outcome final. From any other status, `complete` still requires an outcome.
- The agent drafts follow-ups rather than filing them. A draft passes every gate a real filing does (verb-first title, length limits, and so on), via a `--draft` mode on the mutating commands (`task add`, `task update` for folding into an existing task, `decision add`). Instead of writing, it emits a validated, human-readable block (YAML).
- The blocks go in the task's outcome as free text. The user reviews them; in practice the agent makes any edits the user asks for.
- Each block names a domain operation (for example `add`, `update`, `decision`) and its fields, not a CLI command line, so the blocks survive changes to command flags.
- `endless task complete <id>`, run by the user, re-validates and files every block, then marks the task completed. One deterministic step, no agent involved.

Open for this task: the block schema and versioning, how `complete` handles a block that fails re-validation (all-or-nothing vs. partial), and whether E-2280's structured plan items are the right home instead of free text.
