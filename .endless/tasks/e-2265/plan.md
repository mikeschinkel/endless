# Wait out an unreachable external service in verify and land

- **Hosts are declared in `verify.toml`** and probed before the suite starts;
  the run waits and retries until they answer. Land's own network calls, if
  any, get the same wait.
- **The wait limit is configurable,** default 30 minutes. On timeout the
  failure names the service that was down.
- The waiting happens inside the command, so an agent running it as a
  background command spends no tokens while it waits.

## Verify

A suite proving: an unreachable declared host delays the run and it proceeds
once the host answers; the limit is honoured and configurable; the timeout
failure names the host; a suite with no declared hosts runs unchanged.
