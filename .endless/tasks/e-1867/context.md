E-1707 has a complete plan in its Text field but a NULL/empty description, so `task show` renders no '— Description —' section at all and list views have only the title to work with.

Since `task add` normally validates a description, some creation path (plan import, epic/child creation, or a direct DB write) is skipping it.
