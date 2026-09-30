Go code writes diagnostics with log.Printf to stderr: hookcmd 23, monitor 11, schema 5, channelcmd 3.

Once session monitor fires the E-698 job runner in-process, any stderr write lands on top of the dashboard's stdout frame painting, and because monitorLoop only repaints when the frame content changes, the corruption persists until the next change rather than being cleaned up on the next tick.
