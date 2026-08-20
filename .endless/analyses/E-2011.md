Touches from E-2002 that all move together: monitor.NormalizeProjectPath, MatchProjectPath, ProjectPath and RepairProjectPaths; endless.project_path normalize, match_project_path and project_name_for_cwd; the hook's single cwd normalization at entry; the CLAUDE.md rule; tests on both sides and tests-tasks e-2002-verify.sh.

ProjectPath is the risky one — every caller currently treats its return as a real path (worktree roots, lock files, git invocations, the claim handoff cd line). It should keep returning the RESOLVED form; only the stored column and the comparison path change shape.

Sequencing note: E-2009 refuses registering anything outside $HOME, which makes the tilde form near-universal and the mixed-form case rare. Neither task blocks the other, but doing E-2009 first shrinks the surface this one has to prove.

Measured beforehand, so nobody re-derives it: the byte saving is not the reason. projects.path holds 53 home prefixes, about 954 bytes of a 102 MB database. The reason is readability, recorded in ED-1562.