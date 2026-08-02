# Roadmap for Contributors

*The following outlines our vision for Endless for those who want to use, suggest improvements for, and ideally contribute PRs to advance Endless.*

*Specifically, this roadmap does not provide commitments or dates. It instead simply expresses our current intent for Endless' future direction.*

| Area | Exists today | Planned |
|---|---|---|
| **Ledger** | JSONL write-ahead log as source of record; SQLite as a rebuildable projection | Harden it for real-world multi-developer collaboration |
| **Collaboration** | Single-developer | Many developers on one project via git push/pull, no central server; collision-free task IDs |
| **Runtime** | Python + Go hybrid | Port all Python to Go → one copy-and-run binary; Windows support |
| **Tasks** | Epic / brainstorm / research / bugfix task types; per-task worktrees + sandboxes; `spawn` / `land`; agent work scored for complexity & risk, gated on human sign-off | Background tasks that kick off unattended sessions, promotable to foreground |
| **Verification** | Verify-by-convention scripts run at land time | `verify.toml` spec + tooling *(in progress)*; standardize the spec; adversarial-agent verification |
| **Schema** | — | Event upcasting: replay history into a new schema without rewriting the ledger *(in progress)* |
| **Views / surfaces** | CLI; session view (`session monitor`) | Project view; web console; TUI with task-tree view + terminal multiplexer; then GUI likewise |
| **Multiplexer** | tmux | Pluggable multiplexers beyond tmux (incl. Endless-as-multiplexer) |
| **Integrations** | — | Pluggable task backends: JIRA / Notion / GitHub Issues mirrors |
| **Distribution** | — | Packaging to install & run without a developer toolchain |
| **Roadmap tooling** | — | Generate & maintain this roadmap from the ledger |
