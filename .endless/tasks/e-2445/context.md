Replaces go-dt E-2441, which planned the same tools as bash just recipes for go-dt alone. Mike, 2026-10-26: almost all of that logic applies to any Go repo, so build it once as endless-go commands and have each repo's justfile call them. Building it in Endless lets us dogfood the process and mature it before it goes into Gomion's general-use CLI; Gomion is in disrepair, so this also gets runnable code sooner. New dependencies in endless-go (golang.org/x/exp/apidiff, golang.org/x/mod) are acceptable short term. This changes the premise of E-2433, whose context names Gomion as the tool Endless would call for the mechanical parts.

What go-dt's E-2431 did by hand, which these commands automate: ran apidiff from each nested module's last tag to HEAD to choose patch or minor under 0.X.Y; read go.mod requires to check go-dt floors across sibling repos (which found go-tealeaves requiring the never-published go-dt v0.7.0, filed as go-tealeaves E-2440); and started the Release workflow by hand.

Decisions carried over from E-2441 (Mike, 2026-10-26), restated generically:
- Floors scans a directory of repos (go-dt's justfile will default to the parent directory), not only the current repo, since CI with GOWORK=off already catches unpublished requires inside a repo.
- The Go module proxy (go list -m -versions) decides "published". git ls-remote on the module's repo only labels a miss: "tagged, not resolvable" versus "never tagged".
- When a module's require of a dependency moves across an incompatible minor (0.X.Y, X differs) between its previous tag and HEAD, print a warning; the version verdict still comes from the module's own API only. Whether such a raise should force a minor is open in EQ-126 to EQ-128.
- Before a release, refuse a dirty worktree or a HEAD that differs from origin's default branch; the workflow tags origin, so the tagged commit must be the one tested.
- Verdict rule: any incompatible API change proposes a minor on v0 (major on v1+), otherwise a patch, matching Gomion's SuggestNextVersions.
- The previous tag is found by directory prefix (<dir>/vX.Y.Z for a nested module, vX.Y.Z for the repo root), as go-dt's release.yml does. The old version is built from the whole repo at that tag, extracted to a temp directory, with GOWORK=off.
- Agents do not trigger releases; the user does.
- Writing release descriptions is out of scope.

Gomion survey findings (2026-10-26): no conflicts with the above. Gomion's apidiff wrapper (goutils.APIDiff) lives in a release engine that is switched off and diffs HEAD against HEAD (noted on gomion E-2434); its prefixed-tag handling is broken (gomion E-2442); its published check uses git tags only.
