Resolution: same 3-layer lookup as _current_endless_session_id (env / pane-direct / single-sibling).

On failure, exit non-zero with a short stderr message; do not print anything to stdout.

Cleanup task: delete the older 'Export ENDLESS_SESSION_ID=<id>; see endless session show' hint from event_bridge.py once shipped.
