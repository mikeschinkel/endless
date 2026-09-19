E-989's companion file is written only at SessionStart. Sessions that were already running when E-989 landed never got one, so 'endless session cd' (E-990) cannot see them until the session is restarted. We hit this immediately after landing E-990 — the session that built it didn't have a companion file.

Same pattern as the existing BackfillProcess in cmd/endless-hook/claude.go (UserPromptSubmit handler): if process is missing, fill it. Apply the equivalent for companion files.

Implementation:
- In the UserPromptSubmit hook handler in cmd/endless-hook/claude.go, after BackfillProcess, check whether the per-session companion file exists.
- If not, build the same record writeClaudeCompanion builds at SessionStart and write it.
- Idempotent: existing files should not be overwritten on every prompt (cheap stat check; only write when missing).
- Same cost model as BackfillProcess — a few syscalls per turn, no DB hit.

Verification:
- Manually delete a live session's companion file. Submit a prompt. File reappears with correct fields.
- Existing companion file's mtime does not change across prompts (no needless writes).

Origin: E-990 verification revealed the gap. Manual workaround for the bootstrap session (writing the JSON by hand) is acceptable as one-off; this task makes the recovery automatic for all current and future deployments.