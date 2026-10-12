## Delete `just claude-settings-sweep` once this is answered (from E-2166/E-2183, 2026-10-11)

E-2166 stopped `claude-settings-init` writing the `hooks` key into a worktree's
`.claude/settings.local.json` — the key that made Claude run that worktree's own
`bin/endless-go` for every hook event, which is how a stale binary twice
resurrected dropped schema objects and broke session writes machine-wide. The
same task added `just claude-settings-sweep` to strip that key from the
worktrees already carrying it, because `bin/` and `settings.local.json` are both
gitignored, so no commit and no rebase can reach them.

**The sweep is a stopgap and should be deleted when this task's answer lands.**
Mike's objection to keeping it stands on its own: it is a generically-named
recipe solving a problem no PRODUCT user has, so it is maintenance debt from the
moment its job is done.

What keeps it alive is precisely this task's gap. A worktree whose branch
predates E-2166 still carries the config recipe that writes the key, so running
`just install` or `just claude-settings-init` there writes it back. Rebasing the
branch is the only thing that closes it — and `endless worktree sync` skips
worktrees with uncommitted changes, which is most live sessions.

Measured on main 2026-10-11:

    worktrees                                          191
      justfile still carrying the key-writing recipe    88
      justfile carrying the fixed recipe               103
      settings.local.json with the hooks key              0  (after a re-sweep)

Observed recurrence rate: one worktree (`e-2157`) in the two weeks since the
first sweep — unpinned Sep 27, written back the same morning.

So the deletion is safe once that 88 reaches zero, by whatever mechanism this
task settles. It is NOT gated on E-2183: that task only stops a worktree-scoped
`just install` running the branch's config recipes, which helps future and
already-rebased worktrees and does nothing for the 88.

E-2183 also commits to documenting the manual equivalent — detect worktrees
whose settings carry a `hooks` key, and repair one with
`just --justfile <main>/justfile --working-directory <worktree>
claude-settings-init` — so removing the sweep costs a loop, not a capability.

Deleting it will break E-2166's landed verify suite, which asserts the recipe
exists in five places. Per `.endless/tasks/CLAUDE.md` that suite is left alone;
verify suites are expected to bit-rot.
