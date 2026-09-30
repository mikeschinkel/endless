Triage records its call as a task.status_changed event with actor.kind=triager, carrying the deciding model and a one-line rationale in the payload. Nothing reads it. task show displays neither, so a triage decision that lands on unplanned is indistinguishable from a task that was never triaged.

This caused a live misdiagnosis on 2026-08-08: E-1847, E-1891 and E-1893 were manually set to untriaged, the sweep judged all three and routed each back to unplanned with a specific, correct rationale, and the feature was reported as not working because the end state matched the start state.

E-1859's plan called that payload 'what you want when triage starts making calls you disagree with' but never gave it a reader.
