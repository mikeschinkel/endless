Wire `_find_sibling_claude_session` (task_cmd.py) as the third fallback in emit_event session resolution.

On 0 matches: still leave session_id empty (no attribution).

On 2+ matches: leave empty (ambiguous; matches task claim refusal behavior).
