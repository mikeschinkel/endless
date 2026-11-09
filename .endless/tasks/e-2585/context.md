E-2063 was planned as five increments landing together. It was split after
Increment 3 so the fix for its defect could land and be verified on its own:
a Claude Code clear now appends a `session_instances` row to the pane's session
instead of creating a second `sessions` row.

What E-2063 left behind is a bridge. `sessions.session_id` still exists and
holds the UUID of whichever instance fired last, so Go and Python code that
looks a session up by UUID keeps reaching the live conversation. That is the
same fact stored in two places, kept in step by every append path, which is the
thing E-2063's plan set out to stop.

Removing it breaks every open branch that names `sessions.session_id`: the SQL
is a string, so it compiles and fails only when run. Landing it separately
lets the branches that were open during E-2063 land or rebase first.

Resume needs instance selection only once a session can hold two open
instances. Until this task drops the bridge, resume reaches the latest UUID
through `sessions.session_id`.
