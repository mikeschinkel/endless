Three routes, and the choice belongs to whoever owns the launcher:

1. Isolate the socket in the suite. The path is resolved through `config.Load`,
   so a conftest fixture that points the config at a temp dir keeps every test
   off the user's servers, not just this one. Widest blast radius, and it fixes
   the class rather than the instance.

2. Give the launcher an explicit override and have the suite set it, so a caller
   can say which server it means instead of inferring it from what is running.

3. Re-pin the test to the new behaviour, asserting the deprecation warning and
   the status demotion without relying on a downstream death at all. The test
   only ever wanted the two things upstream of the spawn; the exception was
   incidental scaffolding.

Route 1 and route 3 are not exclusive: the test's premise is stale regardless of
whether the suite gains isolation, since a bogus `$TMUX` is no longer a reason
for a spawn to fail.
