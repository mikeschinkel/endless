Per ED-1547, no entity id is ever reused, for any entity type — not just tasks.

Sessions are the known live exposure: session_tasks.session_id is FK-free in exactly the way task_id is, task_cmd already renders a state for a touch whose session row is gone, and the dead-pane reaper work in E-1898 is in that neighborhood.
