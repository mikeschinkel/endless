# Discussion: Parent/Child Projects and Project vs. Plan Distinction

## Resolution

The sub-project concern turned out to be a same-repo, different-concerns scenario — not separate codebases. The right model is **plan tree branches within one project**, not parent/child projects.

### Project vs. Plan — the distinction is real

| Dimension | Project | Plan |
|---|---|---|
| **Identity** | A codebase (path, git, language) | A unit of work (lifecycle, prompt) |
| **Persistence** | Permanent until unregistered | Temporary — completed and done |
| **Ownership** | You own the codebase | A session works the plan |
| **Hierarchy** | Grouping/dependencies between repos | Parent/child decomposition of work |
| **Multiplicity** | One project = one directory | One project has many plans |

Merging them into one table would put "go-tealeaves" (a codebase) and "Fix CLI flag parsing" (a task) as siblings, which is confusing.

### Multiple concurrent sessions per project

Already supported. `ai_sessions.UNIQUE` is on `session_id`, not `project_id`. Multiple Claude sessions can each `plan start` a different plan branch in the same project. Created #624 for UI improvements to make this visible.

### When separate projects ARE needed

If a CLI wrapper lives in its own repo with its own `go.mod`, it's a separate project. The `group_name` field (already in schema) can group related projects. Full parent_id hierarchy is deferred unless grouping proves insufficient.

## Status: Resolved — no schema changes needed
