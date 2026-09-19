Scope boundary, so this is not folded into E-1428 by a later reader who sees 'eswt' in both.

E-1428 ('Rework endless task claim output') has four deliverables. A — fix the broken eswt reference — was delivered by E-2106, which deleted the whole 'choose one' block that carried it. B, C and D are open, and B is a live footgun with nothing to do with eswt: worktree_cmd still prints 'sandbox provisioned: ~/.cache/endless/sandboxes/<name>' as a line parallel to the worktree path, users cd into it, and every subsequent endless command then fails with 'Not in a registered project directory' (hit by Mike 2026-05-19). E-1428 also wants an 'endless worktree sandbox' verb to retrieve that path on demand, which does not exist. Folding B into a documentation task would bury it.

So: E-1428 keeps the command-OUTPUT work. This task is the guide PROSE, and it is the tail of E-1180 rather than of E-1428 — E-1180 proposed the helper, the guide documented the proposal, and obsoleting E-1180 is what makes the documentation wrong.

Related closures, for anyone reading the eswt trail: E-1180 obsolete (we will not ship it), E-1254 obsolete (its subject, claim's eswt line, was deleted by E-2106).