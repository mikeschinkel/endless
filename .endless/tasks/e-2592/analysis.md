Every rule path is read relative to the base directory: /private becomes <base>/private, /etc becomes <base>/etc. A test builds a fake root under its temp dir (creating whatever symlinks it needs, e.g. <base>/tmp -> <base>/private/tmp) and runs the real built-in rules against it, so the fixtures' `remove` override can go.

Today the flag is global on endless-go and Python passes it through, the way --db-dir is. After E-1063 there is one Go binary: it is a global flag on the root command, parsed once and handed to the resolver, with no pass-through at all.

Windows known-folder lookups ask the OS and cannot be relocated; Windows is not supported yet, so that waits.

Where the rules are consulted: monitor.ResolveDirectory, reached from ProjectIDForPath (hook auto-registration, prompt and session-query lookups), ProjectForCwd, and the `project resolve` verb Python uses for register, discover, project_sync, list, unregister and project_name_for_cwd.
