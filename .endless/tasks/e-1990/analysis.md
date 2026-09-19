## Parity notes

- readlink on another user's `/proc/<pid>/cwd` returns EACCES. Skip those
  silently — non-root lsof has the same blind spot, so this is parity, not a
  new hole.
- Resolve symlinks on both sides before the prefix match: readlink returns the
  resolved path, and the caller's dir may not be resolved.
- Keep the fail-closed contract — an error still answers "in use".
- Roughly 40 lines plus tests. Make the proc root an injectable package var so
  the Linux branch is exercisable from a macOS test run against a fixture tree.

## Why this is its own task rather than folded into E-1947

Endless has zero `runtime.GOOS` switches and zero build tags today — this would
be the first portability seam in the tree, and it should be verified on a real
Linux box (a VM is available) rather than shipped from a macOS-only run.
Writing a branch against an environment nobody sampled is exactly how E-1962
shipped broken the first time.
