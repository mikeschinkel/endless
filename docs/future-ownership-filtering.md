# Future: Project Ownership Filtering

Captured 2026-04-08. Needs design and implementation.

## The Problem

Not all directories under ~/Projects are the user's own projects. Some contain:
- Cloned repos from other people/orgs (references, forks, dependencies)
- Mixed content (e.g., ~/Projects/ai/aiutil has own docs + reference docs from other projects)
- Third-party code checked out for study or contribution

Endless should only track documents belonging to the user's own projects, not other people's repos.

## Proposed: Ownership Rules in Global Config

```json
{
  "roots": ["~/Projects"],
  "ownership": {
    "mine": [
      "github.com/mikeschinkel/*",
      "github.com/gearboxworks/*"
    ],
    "exclude": [
      "github.com/charmbracelet/*",
      "github.com/openai/*"
    ]
  }
}
```

### Matching approaches (to decide):
- `starts_with` — e.g., `github.com/mikeschinkel`
- `glob` — e.g., `github.com/mikeschinkel/*`
- `regex` — e.g., `^github\.com/mikeschinkel`

### Detection:
- Read `git remote get-url origin` to determine repo ownership
- Match against ownership rules
- Projects without a git remote default to "mine" (local-only projects)

## Impact on existing commands

- `discover` — skip repos that don't match ownership rules (or classify in a separate tier)
- `scan` — skip documents in non-owned repos
- `docs` — only list documents in owned projects
- `list` — could show an "ownership" column or filter

## Related: Prompt Hook for Directory Tracking

The user also wants to hook into the shell prompt (or cd) to track directory changes and know which projects are being actively worked in. This relates to session tracking (tmux, Claude Code sessions) from the design brief.

Possible implementations:
- Shell hook (`chpwd` in zsh, `PROMPT_COMMAND` in bash) that logs cwd changes to Endless
- Claude Code hook that fires on session start/end
- Integration with tmux session monitoring (already in design brief Phase 1c)
