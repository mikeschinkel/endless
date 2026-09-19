## Decision: Child status summary hidden in needs-attention section

**Context:** Child status summaries (e.g. "1 verify · 1 plan") were showing in both the needs-attention and backlog sections. The "1 plan" count is irrelevant in needs-attention since that section only shows verify/blocked items.

**Options considered:**
1. Filter the summary to only show verify/blocked counts in needs-attention
2. Hide the summary entirely in needs-attention

**Decision:** Hide entirely (#2). Needs-attention items are expanded by default, so the collapsed summary is rarely seen. When collapsed, the verify/blocked children are visible in the section itself. The filtered summary would be redundant.

**Revisit if:** Needs-attention section changes to collapsed-by-default, or if parents routinely have many verify/blocked children where a count would help.
