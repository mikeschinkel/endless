`endless worktree land` reported "rebase conflict while rebasing your branch onto main" with "Conflicting files: (none reported)" for a failure that was not a conflict — git had refused to detach HEAD over a skip-worktree file (see E-1998). It then offered two recoveries, neither applicable, one of them `git reset --hard main`, which is destructive and would have been wrong here.

"(none reported)" is the tell that the classifier had no evidence for its own diagnosis and reported it anyway.
