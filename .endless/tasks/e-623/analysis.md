Options: parent_id on projects table, or a dep_type='contains' in project_deps.

Deeper question: is the project/plan distinction legitimate?

Projects = codebases (path, git, language, persistent). Plans = units of work (lifecycle, prompt, consumed). The asymmetry (plans need projects, projects don't need plans) suggests they are genuinely different. But sub-project use cases blur the line.

Resolve before adding more project hierarchy.
