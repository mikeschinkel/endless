independent of E-1576 (confirmed: they fail at HEAD before any E-1576 edit).

The rendered handoff text lacks the strings the tests assert.

Cluster 1 — tests/test_handoff_children_state.py (test_epic_render_shows_no_children_yet, _shows_mixed_breakdown, _shows_single_bucket, _drops_redundant_child_count_block): epic handoffs no longer carry a 'Children:' status-breakdown line.

Cluster 2 — tests/test_handoff.py::test_render_handoff_research_variant: the research variant doesn't emit the '--status completed --outcome-file' instruction (post-E-1001 --xxx-file flag form).
