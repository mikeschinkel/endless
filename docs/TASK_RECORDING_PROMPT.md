# Problem: Claude consistently fails to record tasks and decisions

## The issue

Claude Code sessions working on Endless repeatedly fail to:

1. **Create tasks before doing work** -- code changes get made without a corresponding Endless task, violating the "Record all actions" rule
2. **Record decisions when they're made** -- architecture and design decisions emerge from discussion but don't get captured as `--type decision` tasks until Mike notices and asks
3. **Remember the rule even after being corrected** -- the same failure repeats across sessions and even within the same session after explicit correction

This is not a one-time oversight. It's a persistent pattern across multiple sessions spanning days. Examples from the current session alone:

- Updated the session guide (docs change) without creating a task first
- Made 5 architecture decisions in discussion without recording any of them
- After being told to record decisions, forgot to record the meta-decision about using task types for decisions
- After being told about THAT omission, still needed prompting

The memory system has a feedback entry ("Record all actions -- every code change must be an Endless task, even quick fixes") but it's clearly insufficient. Reading the rule and following the rule are different problems.

## What we want

Every session should automatically:
- Create an Endless task before making any code change (or immediately after if the change was reactive)
- Update the Endless task after making any code changes when any requirements changed during the process (when applicable)
- Record any decision that emerges from discussion as a `--type decision` task
- Do this without Mike having to remind, prompt, or catch omissions

## Approaches to explore

### 1. Hook-based enforcement
Could a `PreToolUse` or `PostToolUse` hook detect that a file edit happened without a corresponding `endless task` command in the recent history? The hook system already blocks writes without declared intent; could it also check for task recording?

Think outside the box here. Maybe there are other hooks, such as `Stop` or when `idle_prompt`  notification type? 

### 2. Claude Code system prompt / CLAUDE.md improvements
Is the instruction in CLAUDE.md not strong enough? Should it be restructured, made more prominent, or include explicit checklists? Is there a way to phrase it that Claude is more likely to follow consistently?  

HOWEVER, this needs to work without relying heavily on CLAUDE.md since everyone's CLAUDE.md will be different.

### 3. Periodic self-check mechanism
Could there be a hook or prompt injection that periodically asks "have you recorded a task for what you just did?" -- like a linter for process compliance?  Maybe on `UserPromptSubmit`, `PostToolUse`, `SubAgentStop`, `TaskCreated`, `TaskCompleted`, `WorkTreeCreate`?

### 4. Post-session audit
Could a hook on session end (, `SessionEnd`) scan git diff and compare against tasks created/updated during the session, flagging unrecorded changes? 

### 5. Structural changes to the workflow
Is the problem that task creation is too much friction? Would reducing the ceremony (e.g., auto-creating tasks from commit messages, or inferring tasks from file changes) help?

### 6. Decision detection
Decisions are harder than tasks because they emerge from conversation, not from code changes. Is there a pattern Claude could watch for (e.g., "we decided", "let's go with", "agreed") that triggers a decision-recording prompt?

## Context files

- `/Users/mikeschinkel/Projects/endless/CLAUDE.md` -- project instructions
- `~/.claude/CLAUDE.md` -- global instructions (has the "Record all actions" rule in Task Management section)
- `~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/feedback_record_all_actions.md` -- the memory entry about this
- `/Users/mikeschinkel/Projects/endless/docs/guide-2026-04-15-using-endless-in-sessions.md` -- session guide given to new sessions
- `/Users/mikeschinkel/Projects/endless/src/endless/cli.py` -- where `--type decision` was added

## Goal of this discussion

Produce a concrete plan with specific changes (hooks, prompts, workflow modifications) that make task and decision recording automatic rather than relying on Claude remembering. The solution should be enforceable, not advisory.
