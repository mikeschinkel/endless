# Plan

Answer the five questions in this task's context with counts from real
transcripts and the hook log, then report measured capture and
false-positive rates for a candidate hook rule.

## Sample

Endless-driven sessions from the last month. Find them through Endless's
session records (harness session ids on ES- sessions created in the last
30 days) and map each to its transcript JSONL under the Claude projects
directory in the user's home. Sessions with no transcript on disk are
counted and dropped.

## Ground truth

The research session labels by reading; the user is not asked to label.

1. Handover messages, exhaustively: for every sampled session, the
   assistant message in which the task was flipped to unverified or
   unreviewed (locate by the ledger event's timestamp against the
   transcript). Record every command handed to the user in that message
   and how it was written (fence with info string, fence without, inline
   code span, bare prose).
2. Mid-task messages, by random sample: draw assistant messages from the
   same sessions that contain a fence or inline code, outside the handover
   message, and label each code block as command-for-the-user or not
   (error text, file content, TOML, output, example, placeholder).

## Hook events

Read the existing hook log, written by claude-log-hook for every hook
event. For each event kind present, note whether its payload carries
assistant text and whether mid-turn text (between tool calls) appears in
it or only in Stop. No new hook is added; the log already records every
event's payload.

## Extractor

Write a throwaway extractor in the scratchpad that reads the sampled
JSONL, applies a candidate rule (start with: fenced block whose info
string is bash or sh, or an untagged fence whose first line parses as a
shell command), and emits what it would have captured. Score it against
the labels: capture rate on labeled commands, false-positive rate on
labeled non-commands. Try at least one variant of the rule if the first
misses a visible class.

The 'tool' task type is not landed yet, so the extractor stays in the
scratchpad; the outcome records its full path so it can be filed as a
tool later if it earns it.

## Outcome

Counts for each of the five context questions, the hook-event table, the
candidate rule with its measured rates, and a one-paragraph recommendation
for E-2531: hook capture, a fence-tag convention, or neither.
