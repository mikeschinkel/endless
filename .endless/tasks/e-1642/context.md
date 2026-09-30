Pattern-B: SessionEnd/EndSession (claude.go) rarely fires (harness is killed, not cleanly exited), so sessions linger state='working' until a later reaper / collision sweep marks a batch 'ended' at one shared instant (e.g. 6 rows all ending 2026-05-27T02:13:07).

Effect: last_activity is bumped far past real life, durations span days, and duration becomes useless as a 'real session' signal.
