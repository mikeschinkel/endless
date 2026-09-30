Session conversation history is NOT version-controlled, contrary to what was assumed and depended on.

Verified: the db-ledger carries only task and decision events (no event kind carries message content), while session_messages (92,545 rows, 32MB) lives only in one SQLite file on one machine.

Beyond WHERE, the WHAT is lossy by choices nobody asked for: tool_use input is truncated to 500 chars (transcript.go line 223), and tool RESULTS are never captured at all, so session history shows what was requested and never what came back. Two full-fidelity sources sit unmined outside version control: Claude's own hook log (534MB, complete untruncated tool_input) and its project transcripts (218MB).
