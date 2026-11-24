package search

import (
	"embed"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed prompts/grep.md prompts/glob.md prompts/find.md
var promptFS embed.FS

func mustPrompt(name string) string {
	data, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		panic("search: missing embedded prompt " + name + ": " + err.Error())
	}
	return strings.TrimSpace(string(data))
}

// Register registers omp's search tools (grep, glob, find) on the extension.
func Register(e *sdk.Extension) {
	// upstream: packages/coding-agent/src/tools/grep.ts:61-67
	e.RegisterTool(sdk.ToolDefinition{
		Name:        "grep",
		Label:       "Grep",
		Description: mustPrompt("grep.md"),
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"pattern":   map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string"},
				"case":      map[string]any{"type": "boolean"},
				"gitignore": map[string]any{"type": "boolean"},
				"skip": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "number"},
						map[string]any{"type": "null"},
					},
				},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
		Execute: executeGrep,
	})

	// upstream: packages/coding-agent/src/tools/glob.ts:34-39
	e.RegisterTool(sdk.ToolDefinition{
		Name:        "glob",
		Label:       "Glob",
		Description: mustPrompt("glob.md"),
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string"},
				"hidden":    map[string]any{"type": "boolean"},
				"gitignore": map[string]any{"type": "boolean"},
				"limit":     map[string]any{"type": "number"},
			},
			"additionalProperties": false,
		},
		Execute: executeGlob,
	})

	// upstream: packages/coding-agent/src/tools/jfind/index.ts:26-30
	e.RegisterTool(sdk.ToolDefinition{
		Name:        "find",
		Label:       "Find",
		Description: mustPrompt("find.md"),
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"grep_keywords": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"path": map[string]any{"type": "string"},
			},
			"required":             []string{"query", "grep_keywords"},
			"additionalProperties": false,
		},
		Execute: executeFind,
	})
}
