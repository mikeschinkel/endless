## Known limitation: deferred to E-1458 (2026-05-23)

E-1450's "all hook-fired writes go to the real DB" rule is correct for production but unconditional — it leaves no affordance for legitimate self_dev testing scenarios where a developer wants to exercise the hook binaries (the 5-10 separate Go apps per E-893 / E-1367 fan-out) against a sandbox DB without polluting the real ledger.

Implement E-1450 as designed. The current edge-case carve-outs in the prompt (test-fixture detection, sandbox-init bypass) are stand-ins, not a full solution. When hook testing against the sandbox becomes a felt need, **E-1458** ("Allow opt-in hook routing to sandbox DB for self_dev testing") addresses it properly — an opt-in mechanism (env var, per-event marker, or wrapper) that bypasses the IsSandboxActive() override at the hook entry.

Until E-1458 lands, hook tests must either inject DB handles explicitly or accept that hook events leak to the real DB.
