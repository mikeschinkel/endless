Fix: split into two modes.

(1) Bare 'release' (no ID): resolve current session, fail with pointer to explicit-ID form if can't.

(2) 'release E-NNN': look up which session has E-NNN bound.

If none: error UNLESS --ignore-missing then info.

If a different LIVE session has it: refuse, name the live session.

If a DEAD/stale session has it (no live companion): clear the binding, report what happened.
