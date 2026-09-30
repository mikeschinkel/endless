Optimization to evaluate at plan time: for commits that haven't been pushed to origin yet, amend successive auto-commits into a single rolling commit.

Bounds the commit count without weakening data integrity (each event still becomes part of git history at write time; the amend just rewrites how recent unpushed history is grouped).
