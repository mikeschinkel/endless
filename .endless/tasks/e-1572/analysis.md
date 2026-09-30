Configurable threshold in .endless/config.json as bg_throttle_warn (default 3). At dispatch, count active bg agents for the project; if >= threshold, emit a soft warning explaining the quota cost (N× linear) and the community-observed thread-limit risk.

Does NOT block dispatch — coordinator decides.
