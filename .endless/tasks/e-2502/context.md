Endless commits its own files (ledger entries, document mirrors, lessons, verify reports) to main, and those commits touch only .endless/. A push made up only of such commits starts a full CI run in any repo whose workflow triggers on push. CI never reads .endless/.

What prompted it: go-dt's Test history on 2026-10-30 listed most runs as "Endless: record ledger entry". Checking each run's head against the previous run's head showed that all 11 runs (#16–#27) included changes outside .endless/. GitHub names a run after the push's last commit, and a land ends with a ledger commit. So that history held no wasted runs. The exposure is a future push that carries only Endless commits.

go-pkgs gets `paths-ignore: [ '.endless/**' ]` through gomion's generated test.yml (E-2454 P28).

Endless-tracked repos outside go-pkgs that have workflows (2026-10-30):
- go-cli/readme-merge: generate-readme.yaml runs on every push to main or test-actions. This is the only one exposed.
- gearboxworks/homelab: ci.yml runs on push, but the repo has no git remote, so it never runs.
- xmlui/cli: sync-labels.yaml is path-filtered to labels.yaml. release.yml is workflow_dispatch only.
- xmlui/localsvr, xmlui/mcpsvr: workflow_dispatch only.
- 3rd-party/codex and go-3rd-party/h2 are forks of other people's projects and are out of scope.

Endless has no code today that reads a repo's .github/workflows.
