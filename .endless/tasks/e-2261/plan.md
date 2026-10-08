# Settle verify and land around the user's own context

## Objective

A task's handoff verify runs in Mike's own context on the agent's request, a
successful land settles the task without a manual status change, and — once
dogfooded — an agent can land tasks marked for it. A verify that passes is one
Mike would also see pass.

## Strategy

- **One lifecycle carries it:** `underway → unverified → unlanded → (land) →
  assumed`, where `unlanded` means a verify passed in the user's context at a
  recorded commit, land is allowed only from `unlanded`, and a commit after the
  pass returns the task to `unverified` deterministically.
- **Settle land first (E-2262)**, so the lifecycle exists before anything
  automates around it.
- **Learn why agent and user verifies differ (E-2266) before building the
  user-context runner (E-2263)**, so a bug is fixed at the source rather than
  worked around.
- **Then run verify in the user's tmux context (E-2263)**, and make it survive
  a temporarily unreachable service (E-2265).
- **Agent auto-land last (E-2264)**, after the runner has been dogfooded.

## Not in this epic

- Requiring every plan deviation to be approved before a verify is requested —
  Mike's process task, still to be filed.
- A `prototype` task type — trialled first as research in E-2267.
