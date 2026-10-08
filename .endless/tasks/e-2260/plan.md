Decisions (Mike, 2026-10-07): Q1 A, Q2 A, Q3 B, Q4 A, Q5 A, Q6 A.

1. Marker (internal/jobs). Add `jobs.Transient(err) error` and `jobs.IsTransient(err) bool`: a wrapper that keeps `errors.Is/As` working on the cause. The runner does not classify errors itself; a job marks the failures it knows are likely to pass on retry.

2. main-sync marks its network failures (internal/mainsyncjob). A failed `git fetch` or `git push` whose stderr shows a DNS failure, connection refused, network unreachable, or a timeout ("Could not resolve hostname", "Connection refused", "Network is unreachable", "Operation timed out" / "Connection timed out", "Could not resolve host") is returned wrapped in `jobs.Transient`. Everything else — a rejected push, an auth failure, a missing upstream — stays unwrapped and reports at once, as today.

3. Threshold (internal/jobs/run.go). A failed run whose error is transient records nothing while the consecutive failure count (fail_count after this run) is below N = 4, a package constant. At N and beyond it records the new warning (step 4) instead of WARN-0001. A non-transient failure records WARN-0001 at once, whatever came before. Backoff is unchanged: it already escalates for any job with MaxBackoff, which main-sync sets (5m, 10m, 20m, 40m: about 75 minutes of outage before the warning).

4. New code WARN-0032 `job-unreachable` (internal/faults/codes.go + docs/errors.md): "A background job could not reach the network". Summary names the job, the consecutive failures and the first line of the cause. Remedy: check network, VPN and the remote's credentials; the job keeps retrying on its own and the warning clears itself on the next successful run; `endless jobs retry <name>` to retry now once fixed. Fingerprint `job-unreachable:<job>`.

5. Self-clearing (internal/jobs/run.go). When a run succeeds, clear the open `job-unreachable:<job>` incident with `faults.ClearFingerprintSince(fp, "", "job:<name>")`. Best-effort: a failed clear is not the run's error.

6. Nothing new before the threshold: `jobs list` already shows fail count, last_error and next due.

7. Tests: a transient failure below N records nothing and still backs off; the Nth records WARN-0032, not WARN-0001; a non-transient failure records WARN-0001 on the first run; a success after the warning clears it; Transient survives errors.Is/As; main-sync's classifier marks each listed stderr phrase as transient and a rejected push and an auth failure as not.
