`verify` is registered on the root group the way `touch` is, taking the same
optional task id as `task verify` and delegating to the same `run_verify`, so
there is one implementation and the flat spelling is an entry point rather than
a copy. `task verify` stays, because it is what the guide and the refusal
messages name.

`verify` is unambiguous at the top level: verifying is something only a task
does, so the flat name does not have to be qualified to say what it acts on.

With that in place the verify recipe in `justfile` goes. Its remaining content —
id derivation, worktree location, the change of directory — is duplicated in the
command, and the one thing it adds beyond the command is the database pin this
task is not here to re-litigate.

References to the recipe have to move with it: whatever names `just verify` in
the guide, the docs and the project rules should name the command instead.
Searching for the recipe name finds them.

Ordering matters. The recipe must not be deleted before `endless verify` exists,
or the shortest spelling of a thing done constantly disappears for however long
the gap lasts.
