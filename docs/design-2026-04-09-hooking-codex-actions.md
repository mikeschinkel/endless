# Codex Hook Integration for File Change Monitoring

## Objective

Implement hook-based execution in Codex CLI to enable an external process to track file system changes (create/update/delete) in the current working directory.

This document defines ONLY the mechanism for invoking monitoring logic via Codex hooks. It does NOT define how file tracking itself is implemented.

---

## Core Mechanism

Codex CLI supports event-driven hooks configured in:

.codex/config.toml

Hooks execute shell commands at defined lifecycle events.

For file monitoring, we rely on triggering a script at key interaction points.

---

## Required Hooks

### 1. UserPromptSubmit (PRIMARY)

This is the most important hook.

- Fires on every prompt submission
- Runs BEFORE the model processes input
- Guarantees frequent execution tied to user activity

Configuration:

[[hooks]]
event = "UserPromptSubmit"
command = "/absolute/path/to/file-monitor-hook.sh"

---

### 2. AfterAgent (RECOMMENDED)

- Fires after each full agent turn
- Captures file changes caused by Codex itself

[[hooks]]
event = "AfterAgent"
command = "/absolute/path/to/file-monitor-hook.sh"

---

### 3. PostToolUse (OPTIONAL, IF AVAILABLE)

- Fires after each tool execution
- Useful if tools modify files incrementally

[[hooks]]
event = "PostToolUse"
command = "/absolute/path/to/file-monitor-hook.sh"

If unavailable, fallback:

event = "AfterToolUse"

---

### 4. SessionStart (INITIAL BASELINE)

- Runs once per session
- Useful for initializing baseline state

[[hooks]]
event = "SessionStart"
command = "/absolute/path/to/file-monitor-hook.sh --init"

---

### 5. Stop (OPTIONAL CLEANUP)

- Fires when session ends

[[hooks]]
event = "Stop"
command = "/absolute/path/to/file-monitor-hook.sh --finalize"

---

## Notes

- Hooks are synchronous; keep scripts fast.
- Use absolute paths for reliability.
- Hooks may evolve; verify against current Codex CLI version.

---

## Summary

Use UserPromptSubmit as the primary trigger, supplemented by AfterAgent and optionally PostToolUse, to ensure file monitoring runs consistently during Codex interactions.
