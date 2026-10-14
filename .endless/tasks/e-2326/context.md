Agreed in the brainstorm E-2230 (the type rules were proposed as a decision and rejected as task-level, so they live here). Three types for work that writes code that is not merged into the project:

- **Experiment:** tries an approach on the project's own code, in a project worktree on an experiment branch. Findings lane. Its code is a diff, so it is kept as a branch, not a directory, and is never merged. Completion requires keep (the branch is preserved) or discard (the branch is left for the reaper).
- **Prototype:** a self-contained proof of concept, built at the top of an empty-repo task directory. Findings lane. Completion requires keep (land copies the directory to `.endless/prototypes/e-NNNN/` on main) or discard.
- **Tool:** a self-contained utility to keep and run again, built at the top of an empty-repo task directory. Verification lane. Its own test suite, which lives in the tool's directory and is never part of the project's suite, must pass before land; land copies the directory to `.endless/tools/e-NNNN/` on main.

Prototypes and tools depend only on external packages; anything that needs the project's own code is an experiment. Each needs a `README.md` whose metadata is YAML front matter wrapped in an HTML comment at the top of the file (task id, title, the question it answers or problem it solves, how to run it, and for a tool how to run its tests). Land refuses without it. Directories are named `e-NNNN`, never `e-NNNN-<slug>`.

Kept directories are found through the task list and through generated indexes. A periodic job regenerates `INDEX.md` for `.endless/tools/`, `.endless/prototypes/` and `.endless/tasks/` from each directory's metadata, committing to main the way ledger auto-record commits do. A generated index cannot drift, and a schedule avoids conflicts between concurrent lands.

Open: whether a copy-on-land keeps the empty repo's commit history in any form, and which scheduler runs the index job.
