Bug surfaced during E-957 implementation.

Today _project_root_for_task() reads the project's path from the projects table, which is fixed at registration time and points at main.
