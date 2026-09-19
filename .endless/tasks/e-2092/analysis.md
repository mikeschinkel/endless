The casing is reachable because verify.Discover compares task ids NORMALIZED — correctly, so the directory can stay lowercase while the manifest's `task` field stays the canonical E-NNNN — but a shell command in the same file gets no such tolerance. Discovery accepting a spelling the runner's own shell rejects is the trap, and it is silent on the developer's machine and fatal on CI.

Fix the cause, not the instance. Fixing E-1603's one string would leave the next author to make the same mistake, and that manifest is a landed suite, so per the rules in .endless/tasks/CLAUDE.md it is not the place to fix it anyway.

The seam already exists: internal/verifycmd/script.go exports three variables into a script suite's environment. The manifest path (runChecks -> driver.Run -> execCapture) passes the same env slice, so adding one entry reaches every driver at once.

Found while moving the suites in E-2023.