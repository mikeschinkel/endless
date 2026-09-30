E-787 Phase 5 verified 'no prod leak' via stat -f mtime check on ~/.config/endless/endless.db before/after the test sequence.

Brittle: prod can change for unrelated reasons (background tasks, other sessions) during a test window, producing false positives.
