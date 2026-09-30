Git's stderr, which names the real reason, is captured in CalledProcessError and discarded.

Observed landing E-2107; a manual `git rebase main` immediately afterwards replayed every commit and exited 0.
