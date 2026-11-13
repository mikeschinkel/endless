Agents write free text into task fields, decisions and lessons, and Endless
commits all of it into the project's repository. In a public repository, a
secret that reaches any of these is published on the next push and stays in
git history after deletion. Nothing checks for secrets on the way in.

The filter is a safety net, not a guarantee. Pattern matching will miss some
secrets and flag some harmless text. Mike's position is that for a filter
like this, removing some is better than removing none: the requirement for
deterministic gates applies to processes that break on a wrong answer, not to
best-effort filtering.

It covers every write that ends up in a committed file: event emits through
the Go pipeline, and `lesson write`. Whether a hit refuses the write or
redacts the value, and which rule set to use (gitleaks' rules are an obvious
candidate), are for the plan.
