# Research request: how every Endless session learns to leave other projects' work to those projects

## Why

A session in one project read its own plan step ("check each dependency for uncommitted work, commit, push") as authority over another project's in-flight work, and proposed committing and tagging it. Mike corrected it: work in a project is managed by a task in that project, run by a session for that project. Neither the guide nor any CLAUDE.md says so, and a plan can appear to say the opposite. The rule must be unmissable and must hold even against a plan that seems to grant the authority.

## Questions to answer

1. Does a task or decision already cover this? Search tasks and decisions for cross-project work, delegation, dependency, uncommitted work, release and ownership. Read the decision on searching for an owning task before filing. If one exists, the finding goes there as evidence and nothing new is filed.
2. Where does a session learn its rules today, and which of those places should carry this one? Cover: `endless guide` and its subtopics, the spawn handoff text, the generated per-repo CLAUDE.md and AGENTS.md templates, the go-pkgs parent CLAUDE.md, and a shared instruction file under the Endless config directory that the user-level CLAUDE.md imports so every session in every project reads it. For each, weigh reach, drift (generated versus hand-edited), and whether it survives a plan that says otherwise.
3. Does Endless already enforce any part of this (a hook on writes outside the session's project, claim checks), and would enforcement beat instruction? What would an enforcement hook have to distinguish: reading, writing, committing, tagging?
4. How do plans get written? A step like "commit each dependency's work" should be a task in the dependency's project with a blocker on the dependent task. Does plan-authoring guidance or plan review need to catch this shape?
5. What does a session do when it needs another project's work done and no owning task exists? The answer must follow the filing rules: ask the user, per the decision above.

## Inputs

- The go-cfgstore release task and the go-logutil tasks that own the uncommitted work there (the logutil tasks on landing uncommitted work, adopting generated workflows, and the vet fix).
- The guide topics, the handoff renderer, the CLAUDE.md templates and the hook code in the Endless repo.
- The user-level CLAUDE.md and the Endless config directory.

## Deliverable

An outcome that states the rule plainly (a session works only in its own project; for work in another project it finds the owning task there, or asks the user whether one should be filed, adds a blocker on its own task, and stops; it never commits, lands, tags or releases another project's in-flight work, whatever its plan says), names the chosen home or homes for it with the reason for each, says whether enforcement is warranted, and lists the follow-up tasks it recommends. Filing those follow-ups is the user's call after reading the outcome.
