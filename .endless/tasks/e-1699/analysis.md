Fix: erase each line to EOL on repaint - insert '\x1b[K' before each newline in monitorLoop (strings.ReplaceAll(frame, "\n", "\x1b[K\n")) plus one after the last line; keep the trailing '\x1b[J' for the fewer-rows case.

Leave the one-shot snapshot renderer free of escapes.
