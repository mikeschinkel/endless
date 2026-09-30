Endless runs code from at least four independent binary identities and detects divergence in none of them: a long-running process's image, the main checkout's build behind the global symlink, each worktree's own build, and the editable Python install that goes live while Go does not.

Two silent failures were observed on one day.

Nineteen monitor processes spanning ten days each ran the job runner in-process from the image they were exec'd with, so a landed fix appeared not to work.

And 89 Python tests failed in a worktree after a rebase because its binary predated the rebase.

Both invert trust: correct code appears broken, so the search goes to the code rather than to what is executing it.
