Coordinator row: stable=epic id, active_task_id changes as user switches view. Bg agent row: active_task_id=child task id, active_epic_id=parent epic id. Non-epic foreground: active_epic_id=NULL.

tmux_pane becomes nullable for 'background' rows. Window-name renderer reads active_epic_id + active_task_id: NOT NULL AND != → [E-EEEE:E-CCCC]; NOT NULL AND = → [E-EEEE]; NULL → [E-NNNN]. Focused-bg-agent status is derived from the coordinator row, not stored.
