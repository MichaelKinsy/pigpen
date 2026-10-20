## Pi tool mapping

Pi has native skills but does not expose Claude Code's `Skill` tool. When a Superpowers instruction says to invoke a skill, use Pi's native skill system instead: load the relevant `SKILL.md` with `read` when the skill applies, or let a human invoke `/skill:name` explicitly.

Pi's built-in coding tools are lowercase: `read`, `write`, `edit`, `bash`, plus optional `grep`, `find`, and `ls`. Use those for the corresponding actions: read a file, create or edit files, run shell commands, search file contents, find files by name, and list directories.

Pi does not ship a standard subagent tool. If a subagent tool such as `subagent` from `pi-subagents` is available, use it for Superpowers subagent workflows. If no subagent tool is available, do the work in this session or explain the missing capability instead of inventing `Task` calls.

Pi does not ship a standard task-list tool. If an installed todo/task tool is available, use it. Otherwise track work in plan files or a repo-local `TODO.md` when task tracking is needed. Treat older `TodoWrite` references as this task-tracking action.