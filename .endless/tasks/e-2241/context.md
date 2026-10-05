Found 2026-10-04 while Mike searched session history for a discussion of `endless tmux start` that he knew had happened: `session_messages` held only the assistant's replies about it, never his own words.

Cause: `monitor.ParseTranscript` (internal/monitor/transcript.go, the `strings.HasPrefix(text, "<")` test) skips any user message whose text starts with `<` — meant for Claude Code's own wrappers (`<task-notification>`, `<local-command-stdout>`, `<command-name>`, …). Claude Code puts pasted text FIRST in a prompt as `<pasted_content id=…>`, so any prompt that opens with a paste is discarded whole, including everything typed after it. Prompts with a paste in the middle are kept.

Measured across this project's transcripts: 50 paste-led prompts in 20 sessions are missing, four of them the `tmux start` discussion (session 71c83770 on 2026-10-03, session 3f79273a on 2026-10-04). Also dropped: prompts that simply begin with `<` (three begin `<sigh>`) and the `/command args` line inside `<command-name>` wrappers.

The content is not lost: the Claude Code transcript files still hold every dropped prompt, so they can be re-imported (`ParseTranscriptFull`; `INSERT OR IGNORE` on message_uuid makes re-import safe).

Seen in the same code but not confirmed: ParseTranscript saves the offset as the file position after bufio read-ahead; a line still being written when a hook fires fails to parse and the offset moves past it; a line over the scanner's 1 MB limit stops the scan while the offset still advances; insertMessage ignores db.Exec errors.
