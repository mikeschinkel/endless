Add a defensive check after derivation: if the derived task has no active worktree per 'endless worktree list', skip that source and fall through to the path-regex fallback or error.

Keeps the recipe robust without dictating session-binding behavior.
