first-class runners (gotest, pytest) use a structured 'tests' list Endless translates to native filters; other runners use a raw 'command' + declared 'format'.

Endless runs all checks, normalizes each to CTRF, merges into one report, and emits resolved commands for bare-clone.
