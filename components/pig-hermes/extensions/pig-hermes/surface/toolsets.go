package surface

import (
	"sort"
	"strings"
)

// Toolset describes a named group of Hermes tools (matches hermes-agent/toolsets.py).
type Toolset struct {
	Name        string
	Description string
	Tools       []string
	Includes    []string
}

var hermesCoreTools = []string{
	"web_search", "web_extract",
	"terminal", "process_manage",
	"read_file", "write_file", "patch", "search_files",
	"skills_list", "skill_view", "skill_manage",
	"todo_list", "memory",
	"session_search",
	"clarify",
	"execute_code", "delegate_task",
	"cronjob_manage",
	"tool_search", "tool_describe", "tool_call",
	"browser_exec",
	"vision_analyze", "image_generate", "text_to_speech",
}

var codingTools = []string{
	"web_search", "web_extract",
	"terminal", "process_manage",
	"read_file", "write_file", "patch", "search_files",
	"skills_list", "skill_view", "skill_manage",
	"todo_list", "memory",
	"session_search",
	"clarify",
	"execute_code", "delegate_task",
}

var builtinToolsets = map[string]Toolset{
	"hermes-cli": {
		Name:        "hermes-cli",
		Description: "Full interactive CLI toolset — all default tools plus cronjob management",
		Tools:       hermesCoreTools,
	},
	"coding": {
		Name:        "coding",
		Description: "Coding-focused toolset: files, terminal, search, web docs, skills, todo, delegate",
		Tools:       codingTools,
	},
	"debugging": {
		Name:        "debugging",
		Description: "Debugging and troubleshooting toolkit",
		Tools:       []string{"terminal", "process_manage"},
		Includes:    []string{"web", "file"},
	},
	"safe": {
		Name:        "safe",
		Description: "Safe toolkit without terminal access",
		Includes:    []string{"web", "file"},
	},
	"file": {
		Name:        "file",
		Description: "File manipulation tools: read, write, patch, and search",
		Tools:       []string{"read_file", "write_file", "patch", "search_files"},
	},
	"terminal": {
		Name:        "terminal",
		Description: "Terminal/command execution and process management tools",
		Tools:       []string{"terminal", "process_manage"},
	},
	"web": {
		Name:        "web",
		Description: "Web research and content extraction tools",
		Tools:       []string{"web_search", "web_extract"},
	},
	"search": {
		Name:        "search",
		Description: "Web search only (no content extraction/scraping)",
		Tools:       []string{"web_search"},
	},
	"skills": {
		Name:        "skills",
		Description: "Access, create, edit, and manage skill documents",
		Tools:       []string{"skills_list", "skill_view", "skill_manage"},
	},
	"memory": {
		Name:        "memory",
		Description: "Persistent memory across sessions (MEMORY.md + USER.md)",
		Tools:       []string{"memory"},
	},
	"todo": {
		Name:        "todo",
		Description: "Task planning and tracking for multi-step work",
		Tools:       []string{"todo_list"},
	},
	"session_search": {
		Name:        "session_search",
		Description: "Search and recall past conversations",
		Tools:       []string{"session_search"},
	},
	"delegation": {
		Name:        "delegation",
		Description: "Spawn subagents with isolated context for complex subtasks",
		Tools:       []string{"delegate_task"},
	},
	"code_execution": {
		Name:        "code_execution",
		Description: "Run scripts that call tools programmatically",
		Tools:       []string{"execute_code"},
	},
	"cronjob": {
		Name:        "cronjob",
		Description: "Cronjob management tool — create, list, pause, resume, remove scheduled tasks",
		Tools:       []string{"cronjob_manage"},
	},
	"clarify": {
		Name:        "clarify",
		Description: "Ask the user clarifying questions",
		Tools:       []string{"clarify"},
	},
	"tool_search": {
		Name:        "tool_search",
		Description: "Progressive-disclosure tool catalog search, schema inspection, and dynamic invocation",
		Tools:       []string{"tool_search", "tool_describe", "tool_call"},
	},
	"browser": {
		Name:        "browser",
		Description: "Interactive browser automation via browser-use CLI",
		Tools:       []string{"browser_exec"},
	},
	"vision": {
		Name:        "vision",
		Description: "Image inspection and visual analysis",
		Tools:       []string{"vision_analyze"},
	},
	"image_gen": {
		Name:        "image_gen",
		Description: "AI image generation",
		Tools:       []string{"image_generate"},
	},
	"tts": {
		Name:        "tts",
		Description: "Text-to-speech audio synthesis",
		Tools:       []string{"text_to_speech"},
	},
	"hermes-acp": {
		Name:        "hermes-acp",
		Description: "Editor integration — coding-focused tools without interactive clarify UI",
		Tools: []string{
			"web_search", "web_extract",
			"terminal", "process_manage",
			"read_file", "write_file", "patch", "search_files",
			"skills_list", "skill_view", "skill_manage",
			"todo_list", "memory",
			"session_search",
			"execute_code", "delegate_task",
		},
	},
}

// BuiltinToolsets returns all built-in Hermes toolsets sorted by name.
func BuiltinToolsets() []Toolset {
	names := make([]string, 0, len(builtinToolsets))
	for name := range builtinToolsets {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Toolset, 0, len(names))
	for _, name := range names {
		ts := builtinToolsets[name]
		toolsCopy := make([]string, len(ts.Tools))
		copy(toolsCopy, ts.Tools)
		incCopy := make([]string, len(ts.Includes))
		copy(incCopy, ts.Includes)
		ts.Tools = toolsCopy
		ts.Includes = incCopy
		out = append(out, ts)
	}
	return out
}

// GetToolset looks up a built-in toolset by name (case-insensitive).
func GetToolset(name string) (Toolset, bool) {
	ts, ok := builtinToolsets[strings.ToLower(strings.TrimSpace(name))]
	return ts, ok
}

// ResolveToolsetTools expands one or more comma-separated or list-provided toolset names
// into a deduplicated, order-preserving list of tool names.
func ResolveToolsetTools(spec string) []string {
	if strings.TrimSpace(spec) == "" {
		spec = "hermes-cli"
	}
	visited := make(map[string]bool)
	seenTool := make(map[string]bool)
	var out []string

	var walk func(name string)
	walk = func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || visited[key] {
			return
		}
		visited[key] = true
		ts, ok := builtinToolsets[key]
		if !ok {
			return
		}
		for _, t := range ts.Tools {
			if !seenTool[t] {
				seenTool[t] = true
				out = append(out, t)
			}
		}
		for _, inc := range ts.Includes {
			walk(inc)
		}
	}

	for _, part := range strings.Split(spec, ",") {
		walk(part)
	}
	return out
}

// isHermesTool reports whether toolName belongs to the core Hermes tool set.
func isHermesTool(toolName string) bool {
	for _, t := range hermesCoreTools {
		if t == toolName {
			return true
		}
	}
	return false
}
