# Confirmed root cause

In src/endless/cli.py, the _absolute_path_tokens helper yields any whitespace-delimited token whose first character is a slash. A LONE slash (a spaced slash used as a prose separator) has a slash as its first character, so it is wrongly reported as an absolute path.

Verified behavior (uv run against the live function):
- a spaced lone slash between two words -> yielded as a path (FALSE POSITIVE)
- 'push' joined to 'pull' by a slash    -> not yielded (correct)
- 'FK' joined to 'UNIQUE' by a slash     -> not yielded (correct)
- a comma-free 'Beads JIRA GitHub Notion' list joined by slashes -> not yielded (correct)
- '.endless' joined to 'db-ledger' by a slash -> not yielded (correct)
- a genuine leading-slash path with real segments -> yielded (correct, keep)
- a tilde-abs path -> yielded (correct, keep)

Only the standalone slash (and a bare tilde-slash) is wrong. Interior slashes are already handled correctly.

# Candidate approaches (explore, do not rank)

1. Narrow the token test: only flag a leading-slash token that has at least one non-empty path segment after the leading slash. A lone slash or bare tilde-slash is definitionally not a path. Smallest change; kills exactly this false positive.
2. Require path-shaped structure: only flag when the leading-slash token also has two or more segments or a file extension, via a tightened _is_path_shaped.
3. Restrict to the non-portable machine paths the gate actually cares about (tmp, Users, var, home, tilde-abs) rather than every leading slash. Broader behavior change; needs thought about the gate's full intent.
4. Whitelist common prose slash idioms — brittle, listed only for completeness.

# Output

A chosen detection rule (likely approach 1) plus a test asserting the confirmed false-positive strings pass and genuine absolute paths still fail. Consider whether the same fix belongs in any Go-side mirror of the gate.