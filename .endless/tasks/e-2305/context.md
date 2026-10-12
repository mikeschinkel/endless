The Claude Code hooks reference (code.claude.com/docs/en/hooks, read
2026-10-12) lists 32 event names usable as keys under `hooks`. Endless installs
eight: PreToolUse, SessionStart, UserPromptSubmit, PostToolUse, Stop,
StopFailure, SessionEnd, Notification. A ninth, PreCompact, is being installed
by the bugfix this task relates to.

Several of the uninstalled ones are dedicated signals for conditions Endless
currently infers by other means:

- `CwdChanged` — the cwd gate, today inferred from Bash command text.
- `WorktreeCreate` / `WorktreeRemove` — the worktree-removal refusal, today a
  regex over Bash command text.
- `PermissionRequest` / `PermissionDenied` — a session waiting on the user,
  today inferred from `Notification`'s `permission_prompt` matcher (E-2091).
- `SubagentStart` / `SubagentStop` — subagent activity, today invisible to the
  session model.

And several are state Endless does not track at all: PostCompact,
PostToolUseFailure, ConfigChange, TaskCreated, TaskCompleted, PreModelSwitch,
PostModelSwitch, Setup, InstructionsLoaded, MessageDisplay, Elicitation,
ElicitationResult, TeammateIdle, FileChanged, DirectoryAdded,
UserPromptExpansion, PostToolBatch, PermissionRequest, Elicitation.

E-2177 hardened the Bash-text regexes rather than replacing the signal, and
landed before these events were available to weigh. The premise behind that
machinery — that Bash command text is the only signal — is what has changed.

One caveat on the evidence: the reference page is 268k characters and only the
first 100k was read. The event-name list and the matcher values quoted below are
from that part. The per-event payload schemas were not read, so what each event
actually carries still needs checking before any of it is planned.
