# Let an agent land tasks marked for auto-land

- **`auto_land` field on tasks, nullable:** null means the project default
  (true); true or false only when set on purpose, so a later change to the
  default reaches every task nobody set.
- An agent may land an `unlanded` task whose `auto_land` resolves true, by
  running land in the user's tmux context through E-2263's mechanism — never
  in the agent's own environment.
- **Edit ED-1605's landing sentence** to say when an agent may land.

## Verify

A suite proving: null resolves to the project default; an agent's land
proceeds for a true task and refuses for a false one; the land runs in the
user's tmux context; ED-1605 carries the new sentence.
