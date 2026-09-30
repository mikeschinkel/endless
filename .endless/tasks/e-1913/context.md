The promotion lives in the Go executor (`execTaskFieldsUpdated`), gated only on "the caller set status explicitly", while `--keep-status` is a Python-side flag guarding only the E-1762 auto-revisit and the E-1845 auto-untriage. The intent never crosses the Python-to-executor boundary, and the payload has nowhere to carry it.

Hit on E-1671, whose own text reads "status intentionally left unapproved pending his review" and which a routine text append silently promoted; the status had to be restored by hand.

Net effect: there is no way to append to a plan without promoting the task.
