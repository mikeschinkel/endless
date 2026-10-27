Landing go-cliutil's uncommitted main work (E-2374) took many sessions of manual work.
Every directory with its own go.mod is a separate module with its own tags, and all of this
had to be worked out by hand:

- The tag order across repos: go-dt's dtx/appinfo, then go-cliutil's root, then go-testutil,
  then go-cliutil/test.
- Removing cross-project replace directives before publishing, and restoring them for
  development afterwards.
- Filing the release tasks in each repo's own project, with the right blocking links.

Parallel sessions (E-2374 for go-cliutil, E-2375 for go-dt) also overlapped on the same
dependency work and had to divide it between themselves. For now, Mike runs the tagging
himself when the sessions tell him a module is ready.

Questions to settle:
- Computing release order from the module graph, including cycles that exist only at the
  version level (go-testutil's next release requires go-cliutil's next release, while
  go-cliutil's test/ module requires go-testutil's next release).
- Removing replaces before publishing and restoring them after; which replaces are in-repo
  (kept) versus cross-project (removed).
- How a release is made: GitHub Actions Release workflows (go-cliutil's and go-dt's reject
  directory-prefixed tags like dtx/v0.2.2), manual tags, or a tool.
- Version decisions under the pre-1.0 rule 0.X.Y, where X is breaking and Y is not:
  deterministic checks (gorelease, apidiff, Gomion's AST tools) plus AI judgment for behavior
  changes the tools can't see.
- Auto-filing per-repo release tasks with blocking links, and keeping parallel sessions
  from duplicating each other's work.

Gomion is the likely tool Endless would call for the mechanical parts. Its brainstorm on
the CLI, E-2434 (Gomion: decide the gomion CLI that Endless calls for Go releases), comes
after this one and takes this one's outcome as its requirements.


