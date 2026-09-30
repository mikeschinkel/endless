Endless shells out to git constantly and assumes capabilities without checking for them.

E-2128 uses 'git rev-parse --path-format=absolute' (git 2.31+) and carries a hand-written fallback for older git; E-1881's conflict pre-flight needs 'git merge-tree --write-tree' (2.38+), for which there is no fallback -- it would simply fail, at sweep time, with git's own error rather than Endless's.

Nothing anywhere states a minimum git version or verifies one.
