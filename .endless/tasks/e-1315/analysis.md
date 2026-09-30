Fix: inline the SELECT in execSessionStatusRecorded using the db dbQuerier parameter (same pattern as resolveProjectID and the other Execute handlers), bypassing the connection-pool round-trip.

Public monitor.GetLiveSessionByProcess stays for non-tx callers (E-1302's task id CLI).
