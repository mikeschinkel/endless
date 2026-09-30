Apply exclusions from the post-worktree-create hook so each new worktree self-excludes, plus a one-time backfill; tmutil has no glob support, so the creation hook is the only viable pattern engine. Use 'tmutil addexclusion' WITHOUT -p: that form is a sticky xattr that dies with the directory, verified to leave SkipPaths untouched, whereas -p accumulates in com.apple.TimeMachine.plist forever and needs root.

MACOS-ONLY: needs a runtime.GOOS guard with a silent no-op elsewhere; the repo has no platform-conditional precedent.
