# Endless — Future Work

Items documented but not scheduled. Move to PLAN.md when ready to implement.

## Project Attributes
Priority (1-10), stage, progress tracking. See `docs/future-project-attributes.md`

## Sprawl Management
Detect and consolidate document proliferation. Needs significant design discussion. See `docs/future-sprawl-management.md`

## Ownership Filtering Enhancements
Per-project overrides, more matching patterns. See `docs/future-ownership-filtering.md`

## Web Dashboard
Phase 1b from design brief. Portfolio view, project detail, notes view, dependency graph.

## Private Companions
Phase 1d from design brief. `.endless/private` manifest, rsync to companion repos, vigil-style auto-commit.

## Browser Extension
Phase 1e from design brief. Chrome extension for claude.ai / chatgpt.com conversation classification.

## NOTES.md Generation
Public + private per-project markdown files maintained by Endless for Claude to read.

## Scan Command Redesign
Document indexing, staleness detection, sprawl detection. Currently implemented but output format needs significant work. Deferred until notes/docs prove useful.

## Daemon
Background scan loop. Bash sleep loop initially, launchd plist for Go phase.

## Hook Setup Commands
- `endless setup claude-hook` / `remove-claude-hook` — install/remove Claude Code hook in `~/.claude/settings.json`
- `endless setup codex-hook` / `remove-codex-hook` — install/remove Codex hooks in `.codex/config.toml`

## Shell Command Activity Capture
Parse Bash tool_input from PostToolUse hooks to detect file operations (rm, mv, cp, mkdir, git). Doesn't need to be 100% reliable — simple pattern matching gets 80% of value. Record the raw command + detected operation type + affected paths in the activity/file_changes tables. Other signals (filesystem mtime, git status) fill in the gaps.

## Dashboard: Fix Theme Toggle
Dark/light/system toggle buttons are present but don't switch styles correctly. Tailwind dark mode classes need proper setup (likely need `darkMode: 'class'` config and correct class toggling on `<html>`).

## Content Hashing for File Change Detection
Currently using mtime comparison. Content hashing (SHA-256) would be more reliable but slower. Since the prompt hook runs in the background, speed isn't a constraint — could switch to hash-based detection.

## Document Type Detection via AI (apfel)
Use Apple Intelligence (apfel CLI) to classify document types when simple name matching fails. Useful for ambiguous filenames.

## Hook Handlers (Go)
- `endless-hook claude` — read JSON from stdin, record session activity and file changes
- `endless-hook codex` — same pattern, adapted for Codex hook payloads
- See design docs: `docs/design-2026-04-09-hooking-codex-actions.md`, `~/Projects/go-cli/claude-log-hook`
