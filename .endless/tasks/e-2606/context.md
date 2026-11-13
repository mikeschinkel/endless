Endless commits `.endless/` into each project's repository. In a public
repository, everything committed there is published. Before deciding what a
public repository should publish, Endless needs a complete inventory of what
it publishes today and why each piece is committed.

Questions to answer:
1. Which files and directories under `.endless/` are committed, by which
   code path (event emit, ledger commit, lesson write, task-file copies,
   decision mirrors, templates, config), and when are they pushed?
2. Which fields in each carry identifying or sensitive data: free text
   written by agents, actor ids (`user@hostname`), session ids, absolute
   paths containing the user's home directory, harness and machine details?
3. Which of these does a fresh clone need in order to rebuild the database,
   and which are only conveniences?
4. How do comparable tools that commit agent or work-tracking state into a
   repository (for example git-bug, git-appraise, and agent memory tools)
   handle public repositories?
