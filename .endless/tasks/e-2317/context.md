Agents are told to file decisions broadly, with no test for what counts as one:
- `docs/guide/decisions.md` (`endless guide decisions`) says "Always document decisions where applicable", listing approaches, scope, conventions and deferrals. Its only filter is preference vs. prohibition.
- The brainstorm handoff (`internal/templatecmd/templates/handoff/_mechanics.tmpl`, `handoff_focus` and the brainstorm deliverable block) tells the agent to "spawn the decisions/tasks it produces (`endless decision add ...`)".

The only place with tests is Mike's personal `/whats-left` command, which runs at the end of a session, after decisions are already filed. Its three tests: was there a real alternative; does it outlive the task (a statement naming a function or file is documentation); did Mike actually make it this session. It also says zero decisions is the normal outcome, and decisions that map one-to-one onto tasks are implementation detail.

Evidence: the E-2223 brainstorm filed ED-1606, which describes E-2229's implementation one-to-one and fails the outlives-the-task test. It was filed because the handoff asked for decisions and the guide gave no test.
