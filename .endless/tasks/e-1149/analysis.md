Affects only src/endless/cli.py (link) and cli.py (unlink) — confirmed via grep that no other --as references exist in src/ or tests/. Update help text.

Verification: 'endless task link E-A --to E-B --type blocks' creates the relation; 'endless task unlink E-A --to E-B --type blocks' removes it; --as raises a click 'No such option' error (no alias).

Out of scope: E-1139's 'endless decision link' will adopt --type natively without going through --as first.
