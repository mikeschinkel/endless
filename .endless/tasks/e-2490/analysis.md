Decided by Mike, 2026-10-29:
- The E-NNNN: subject prefix stays alongside the trailer (CI run titles show the subject).
- Mechanism: a prepare-commit-msg Git hook, installed through a hook manager or into an unclaimed hook file. Endless's own alternatives were set aside: an endless git passthrough and a Claude Code hook that rewrites or refuses agent commits both cover only agents, and adding trailers at land rewrites the SHAs verify and reconcile checked.
- lefthook is the supported hook manager (a Go binary; lefthook-local.yml lets Endless add itself without touching committed config). Its entry goes in lefthook-local.yml or lefthook.yml at the user's choice.
- husky is not configured by Endless: v9 has no commands to manage a hook's contents, and husky's npm prepare script resets core.hooksPath on every install, so it and lefthook cannot be chained robustly. Husky users, and users of other managers, add one line calling endless git-hook themselves (Mike, 2026-10-29).
- In a repo Endless cannot set up, project init completes without the trailer rather than failing; endless doctor reports it unless a user setting turns that off.
- Installing lefthook belongs with Endless's other prerequisites (E-1852).

prepare-commit-msg rather than commit-msg: its job is editing the message, --no-verify does not skip it, and hook tools more often claim commit-msg (Gerrit's Change-Id, commitlint).

Governed by ED-1641.
