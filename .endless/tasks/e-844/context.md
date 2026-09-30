E-708 incorrectly classified session tracking, activity recording, and other DB operations as 'non-fatal' (log to stderr but continue).

The original bug that triggered E-708 was exactly this kind of 'non-fatal' error causing lost data.
