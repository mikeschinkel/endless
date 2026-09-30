Pattern violates project rule that FKs reference integer PKs, not candidate keys.

Fix: migrate session_gates.session_id to INTEGER FK referencing sessions.id; update writers (hook code), update readers (status-line, gate checks).

Pivot-gate behavior must be preserved.
