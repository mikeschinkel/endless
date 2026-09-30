E-1294's single-sibling fallback resolves _current_endless_session_id only when EXACTLY one other Claude pane is alive.

With 5+ active sessions today, the fallback refused to resolve, leaving emit_event with no session_id.
