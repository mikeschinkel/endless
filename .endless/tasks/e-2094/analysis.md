Repointed 2026-08-31 by ES-1159 (E-2023), at Mike's direction, after he asked
whether E-2023 could have been implemented so that this task were unnecessary.
It could — and the answer moves the fix from the test to the runner.

## What was originally filed, and why it was the wrong layer

`TestProjectPath_TildeExpandsToHome` (internal/monitor/projects_test.go) builds
its expectation as `filepath.Join(os.UserHomeDir(), "some-project")` and
compares it against `ProjectPath`, which returns the RESOLVED form. The two
agree only when HOME is not itself reached through a symlink. The original plan
was to fix the expectation and sweep for the same shape elsewhere.

The test IS wrong — it compares a resolved value against an unresolved one. But
nothing had ever observed it, and after the change below nothing will again.
Under ED-1550, "noticing something true does not earn a task."

## Where it actually belongs

`makeRunDir` (internal/verifycmd/env.go) takes `os.MkdirTemp("")` verbatim. On
macOS that lands beneath a symlinked prefix, so the runner hands every suite a
HOME reached through a symlink — something a real home directory almost never
is.

Three reasons that is the runner's bug and not the test's:

1. It is an artifact of the isolation MECHANISM, not a property of the system
   under test. The runner's job is to substitute HOME transparently; anything
   else it changes is noise it injected.
2. It is platform-dependent. The same suite, the same code, passes on Linux
   (the temp root is not symlinked there) and fails on macOS. A runner whose
   verdict depends on the OS's mktemp implementation emits false signal.
3. It misattributes failures — this task's own sharpest observation, that "the
   failure it reports will point at the wrong task." That is precisely the harm
   E-2023 exists to prevent.

The counter-argument from E-2023's plan — "a break is a finding, not a
regression" — was written about suites that secretly depended on the real
database, where the dependency was always a contract violation. It does not
transfer. Nothing in the contract says a suite must tolerate a symlinked HOME,
and the runner is not a fuzzer; a deliberate test of symlinked-HOME behaviour is
a thing someone can write on purpose.

## The change

`filepath.EvalSymlinks` on the freshly created temp dir, failing open (a
directory this process just made should always resolve; if it does not, running
under the unresolved path beats refusing to verify).

Measured before and after:

    raw temp HOME       -> FAIL  ProjectPath = "<resolved>" want "<unresolved>"
    resolved temp HOME  -> ok    internal/monitor

The whole `internal/monitor` package passes under a resolved temp HOME, with
that test the only failure under the raw one — so the sweep this task planned
finds nothing left to fix.

## Pinned, not left to the platform

Three tests, because the invariant is invisible on Linux and would rot silently:

- `TestMakeRunDir_IsCanonical` — the run dir equals its EvalSymlinks form.
- `TestIsolatedEnv_HomeIsCanonical` — HOME and XDG_CONFIG_HOME, the values
  consumers actually see, are canonical.
- `TestRun_ScriptSuite_SeesCanonicalHome` — end to end through the real runner:
  a suite observing a non-canonical HOME exits 30 and fails the test.

## Verification of the original symptom, end to end

A throwaway suite driven through `endless-go verify`, whose check was the exact
repro from the original filing, printed a canonical HOME and passed. Before the
change it failed.

## PRODUCT

Nothing macOS-specific in the fix, though the symptom is. Any platform whose
temp root is symlinked hands the same trap to every downstream project's
suites, and the runner is shipped surface.
