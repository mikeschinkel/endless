Implement Vigil-style shadow git commits for Endless's own version history of project files. Auto-commit changes with debouncing to give Endless change detection and staleness tracking without depending on the project's own git state. Subsumes the dropped file_changes table (111k rows of reimplemented git status) and documents table hash tracking. From design brief (docs/brief-2026-04-02-endless-design.md line 465).

Open question: location. Two candidates:
  - ~/.config/endless/repos/<project>/ — separate from project, no risk of interference
  - .git/info/endless/ — colocated with project git, discoverable, but couples to project repo

This is the Vigil concept (debounced file-watching + auto-commit) absorbed into Endless rather than built as a separate tool.