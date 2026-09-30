## The port must embed the files Python reads from the source tree (E-2155, 2026-09-17)

Three Python modules resolve data files by walking up from their own file to the
repository root: the guide directory (read by `endless guide` and by the
agent-help map), the lifecycle map, and `schema.sql`. The wheel ships only
`src/endless`, so none of those files exist in a non-editable install: a wheel
built from this tree contains no `docs/`, and `endless guide` — the first command
CLAUDE.md tells an agent to run — fails with "Guide directory not found" under a
plain `pip`, `pipx`, or `uv tool install` without `-e`.

It has not bitten anyone because the only documented install is editable.

Recorded here rather than fixed in Python, because the port is where it goes
away: Go already embeds `schema.sql`, the migrations and the templates with
`go:embed`, and the ported `guide` should embed the guide the same way. Worth an
explicit check when `guide` and the status vocabulary move, plus a test that the
built artifact — not the source tree — can render the guide.

## From the description

Settled: the end state is ONE binary named endless, and no transitional second command set is needed — every name the two surfaces share today is a Python shim in front of the Go implementation, so merging deletes the shim rather than reconciling a conflict.

Settled: once the port starts, no new Python work lands in the files being ported (task_cmd, cli, worktree_cmd, session_cmd); anything still open at that point is re-filed against the Go implementation rather than carrying a patch with nowhere to land.

Still open: whether internal plumbing (event, hook, sandbox, template, markdown) stays visible in help once the binary is user-facing.
