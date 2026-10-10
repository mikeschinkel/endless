# Synthesis: endless doctor

## What doctor is
A command that looks at how a machine and its projects are set up *before* anything fails, reports what is wrong, and separately suggests opt-in settings with the reason for each. It has three scopes: machine (global), the current project, and all registered projects. It also covers the worktree scope.

## Output: problems vs suggestions
- `endless doctor` reports **problems** only. Its output ends with a line pointing to `endless doctor --suggestions`.
- `endless doctor --suggestions` lists **opt-in settings** that are unset, each with why you would want it.
- Problems are ordered by **severity first**, then scope. Machine-level blockers (binaries on PATH, Claude Code hooks registered, shell-init, DB reachable) naturally rank high, but severity always wins over a nice-to-have.
- Doctor **folds in** the existing after-the-fact surfaces: recorded faults (`errors list`) and background job health (`jobs list`).

## Declining a suggestion = tri-state setting
- Every opt-in setting is tri-state: unset, on, off. Doctor suggests only **unset** settings. Declining means writing an explicit **off**.
- The decline lives wherever the setting lives, so the setting's own scope decides who sees it:
  - project-config setting (e.g. Claude hooks in `.claude/settings.json`): the decline is committed, and anyone who clones inherits the project's decision, exactly as they would inherit "on". If a teammate disagrees, they change the committed value.
  - machine-config setting (e.g. zsh prompt hooks): the decline holds for that machine only.
- Declines are not personal for project settings.

## Accepting a problem you choose to live with
- Each problem check has a stable ID. `endless doctor --accept <check-id> --reason "..."` records the acceptance at the check's scope (committed for project checks, machine config for machine checks). A reason is required.
- The acceptance is bound to the specific condition observed. If the condition changes and then recurs, the check is reported again.
- Accepted problems are hidden from default output, but doctor shows a count with a pointer (`--accepted`) to list them.

## Fixing: three tiers
- **auto-fixable**: mechanical and safe (e.g. chmod a hook).
- **fixable with confirmation**.
- **advice only**: policy choices (e.g. turning on main_sync).
- `--fix` is **user-only**: an agent may run doctor to diagnose, but not apply fixes. Revisit if users find the agent block a problem. Enforced through the user-only command refusal mechanism.

## Architecture
- Checks and settings are **registered by the feature that owns them**, so shipping a setting means shipping its doctor entry. A central hand-maintained list would go stale, as it did when main_sync shipped with no discovery path.
- Registration must be impossible for an agent adding a setting to miss: the doctor metadata (tri-state value, scope machine/project, rationale, check) is part of declaring the setting, so a setting cannot exist without it.
- Go registration rule: code in `init()` only adds entries and **cannot fail**. Anything that can fail (validating required fields, duplicate IDs) goes in a public `<package>.Initialize() error`, reached indirectly from `main()`, so the CLI exits gracefully with a human-readable message.
- **One registry per kind of thing** (a settings registry, a check registry), not a general registry of registries. Which registration pattern to use is to be chosen from those already in Mike's go-pkgs, after a review.
- Checks run in **Go**, exposed through `endless-go`; Python only renders. This follows the rule that Go owns database access.

## Scopes
- machine, current project, worktree, and `--all-projects`.
- `--all-projects` runs both: every per-project check in each registered project (with that project's root as context), plus checks that only make sense across projects (stale registrations, a project registered twice, missing directories). Slowness is expected, which is why it is behind a flag.
- Only session- and worktree-specific checks don't carry across projects; they belong to the worktree scope.

## worktree check
- Chosen: option (b). `worktree check` becomes a thin alias for doctor's worktree scope: same checks, same output. It keeps its handoff contract (silent when clean, same exit code). Adding a worktree-scope check to doctor automatically adds it to the handoff gate. Severity can decide what blocks: the gate fails on errors only.
- Motivation: `worktree check` runs constantly, yet its purpose is opaque to the user and nobody would think to look for it. Making it doctor's worktree scope makes it discoverable, and avoids drift between the gate and doctor.

## Deferred
- How users discover doctor exists (install/init, session-start nudge, etc.) is downstream. First build a useful doctor, then decide how to surface it.

## Follow-ups filed
- E-2283 (research): review go-pkgs registration patterns and recommend one for both registries. Blocks E-2284 and E-2285.
- E-2284 (todo): Go settings registry with doctor metadata (tri-state, scope, rationale, check); starts by finding existing settings.
- E-2285 (todo): endless doctor command and Go check registry. Only --suggestions depends on E-2284.
- ED-1616 (decision, proposed): worktree check becomes an alias for doctor's worktree scope (option b).
- ED-1617 (decision, proposed): endless doctor --fix is user-only.
- E-2286 (brainstorm, later): how users discover endless doctor.
