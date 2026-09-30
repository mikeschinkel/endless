Repro: a task add titled Adopt spawned the adopt verb-check and ended live session 510; while ended, the resolver (filters state!=ended) cannot resolve the caller, breaking endless session id and task add until the next Stop hook.

Recap shares this (session_cmd.py only hides such rows).
