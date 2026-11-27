package autolearn

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// upstream: packages/coding-agent/src/tools/manage-skill.ts:1-104
// upstream: packages/coding-agent/src/autolearn/settings.ts:10-21

//go:embed prompts/manage-skill.md
var manageSkillDescription string

// Enabled reports whether a boolean setting key is enabled for ctx.
// Wired by wiring.go -> settings.Enabled. Defaults to returning def (false for autolearn.enabled).
var Enabled = func(ctx sdk.Context, key string, def bool) bool {
	return def
}

// IsSubagent reports whether ctx belongs to a subagent session (taskDepth > 0).
// Wired by wiring.go -> agents.IsSubagent. Defaults to false.
// upstream: packages/coding-agent/src/tools/index.ts:788-789,846-850
var IsSubagent func(ctx sdk.Context) bool

// ExplicitTools returns a session's explicit caller/frontmatter tool whitelist
// when set (nil/empty for default sessions). Wired by wiring.go -> agents.ExplicitTools.
// upstream: packages/coding-agent/src/tools/index.ts:846-850
var ExplicitTools func(ctx sdk.Context) []string

// IsNameClaimedByAuthoredSkill reports whether a normalized skill name is already
// claimed by a user-authored (non-managed) skill.
// When nil, checks ctx.GetSystemPromptOptions().Skills for any skill whose name matches
// and whose filePath does not lie inside ManagedSkillsDir(agentDir).
// upstream: packages/coding-agent/src/tools/manage-skill.ts:78
var IsNameClaimedByAuthoredSkill func(ctx sdk.Context, name, agentDir string) bool

// RefreshSkills is invoked after a successful create/update/delete mutation if non-nil.
// upstream: packages/coding-agent/src/tools/manage-skill.ts:50,60,96
var RefreshSkills func(ctx sdk.Context) error

// ManageSkillParams holds the parsed parameters for the `manage_skill` tool.
// upstream: packages/coding-agent/src/tools/manage-skill.ts:16-34
type ManageSkillParams struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
}

func manageSkillSchema() sdk.Schema {
	// upstream: packages/coding-agent/src/tools/manage-skill.ts:16-32
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []any{"create", "update", "delete"},
			},
			"name": map[string]any{
				"type":        "string",
				"description": "kebab-case skill name",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "one-line description of when to use the skill (required for create/update)",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "the SKILL.md body in markdown, no frontmatter (required for create/update)",
			},
		},
		"required":             []any{"action", "name"},
		"additionalProperties": false,
	}
}

// IsEnabled reports whether `manage_skill` is enabled for ctx:
// requires `autolearn.enabled` and either top-level (not a subagent) OR an
// explicit caller/frontmatter tool list that includes `manage_skill`.
// upstream: packages/coding-agent/src/tools/manage-skill.ts:52-55
// upstream: packages/coding-agent/src/tools/index.ts:788-789,846-850
func IsEnabled(ctx sdk.Context) bool {
	if Enabled == nil || !Enabled(ctx, SettingEnabled, false) {
		return false
	}
	if IsSubagent != nil && IsSubagent(ctx) {
		if ExplicitTools != nil && slices.Contains(ExplicitTools(ctx), "manage_skill") {
			return true
		}
		return false
	}
	return true
}

// Register installs the `manage_skill` tool gated behind `autolearn.enabled` (default false),
// registers `intent.RegisterSync` so print/intentTracing:false sessions drop `i`, registers
// `toolcmd.RegisterAutoActivate` so `manage_skill` activates under both command transport
// modes, and attaches `resources_discover` so managed skills under `<agentDir>/managed-skills`
// are discovered by PiG.
// upstream: packages/coding-agent/src/tools/manage-skill.ts:40-55
// upstream: packages/coding-agent/src/discovery/builtin.ts:343-366
func Register(e *sdk.Extension) {
	defaultOff := false
	manageSkillDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "manage_skill",
		Label:         "Manage Skill",
		Description:   strings.TrimSpace(manageSkillDescription),
		Parameters:    manageSkillSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeManageSkill,
	})
	e.RegisterTool(intent.Tool(manageSkillDef))
	toolcmd.RegisterAutoActivate("manage_skill", IsEnabled)
	intent.RegisterSync(e, manageSkillDef)

	// Discover managed skills under <agentDir>/managed-skills unconditionally when the
	// directory exists (empty/absent dir is a no-op; matching discovery/builtin.ts:341-367).
	e.OnEvent(sdk.EventResourcesDiscover, func(ctx sdk.Context, _ map[string]any) (any, error) {
		dir := ManagedSkillsDir(agentDirForContext(ctx))
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return nil, nil
		}
		return map[string]any{
			"skillPaths": []string{dir},
		}, nil
	})
}

func agentDirForContext(ctx sdk.Context) string {
	if fn := AgentDirOverride; fn != nil {
		if d := fn(); d != "" {
			return d
		}
	}
	var zero sdk.Context
	if ctx != zero {
		if d := settings.AgentDirForContext(ctx); d != "" {
			return d
		}
	}
	return DefaultAgentDir()
}

func isAuthoredCollision(ctx sdk.Context, sanitizedName, agentDir string) bool {
	if fn := IsNameClaimedByAuthoredSkill; fn != nil {
		return fn(ctx, sanitizedName, agentDir)
	}
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	opts, err := ctx.GetSystemPromptOptions()
	if err != nil {
		return false
	}
	managedRoot := filepath.Clean(ManagedSkillsDir(agentDir))
	managedPrefix := managedRoot + string(filepath.Separator)
	for _, sk := range opts.Skills {
		if strings.ToLower(strings.TrimSpace(sk.Name)) != sanitizedName {
			continue
		}
		cleanPath := filepath.Clean(sk.FilePath)
		if cleanPath == managedRoot || strings.HasPrefix(cleanPath, managedPrefix) {
			continue
		}
		return true
	}
	return false
}

// ExecuteManageSkill runs `manage_skill` directly (exported for unit tests).
// upstream: packages/coding-agent/src/tools/manage-skill.ts:57-103
func ExecuteManageSkill(ctx sdk.Context, params ManageSkillParams) (*sdk.ToolResult, error) {
	agentDir := agentDirForContext(ctx)

	switch params.Action {
	case "delete":
		if err := DeleteManagedSkill(params.Name, agentDir); err != nil {
			return nil, err
		}
		if RefreshSkills != nil {
			_ = RefreshSkills(ctx)
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("Deleted managed skill %q.", params.Name),
			Details: map[string]any{"action": "delete", "name": params.Name},
		}, nil

	case "create", "update":
		if params.Description == "" || params.Body == "" {
			return nil, fmt.Errorf("%q requires both \"description\" and \"body\".", params.Action)
		}
		sanitized, err := SanitizeSkillName(params.Name)
		if err != nil {
			return nil, err
		}
		if params.Action == "create" && isAuthoredCollision(ctx, sanitized, agentDir) {
			return &sdk.ToolResult{
				Content: fmt.Sprintf(
					"Cannot create managed skill %q: an authored skill of that name already exists, and managed skills cannot override authored ones. Choose a different name.",
					params.Name,
				),
				IsError: true,
				Details: map[string]any{"action": "create", "name": params.Name, "shadowed": true},
			}, nil
		}
		skillPath, err := WriteManagedSkill(WriteManagedSkillInput{
			Action:      params.Action,
			Name:        params.Name,
			Description: params.Description,
			Body:        params.Body,
			AgentDir:    agentDir,
		})
		if err != nil {
			return nil, err
		}
		if RefreshSkills != nil {
			_ = RefreshSkills(ctx)
		}
		relPath, err := filepath.Rel(ManagedSkillsDir(agentDir), skillPath)
		if err != nil {
			relPath = filepath.Join(sanitized, "SKILL.md")
		}
		relPath = filepath.ToSlash(relPath)
		verb := "Updated"
		if params.Action == "create" {
			verb = "Created"
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("%s managed skill %q (managed-skills/%s).", verb, params.Name, relPath),
			Details: map[string]any{"action": params.Action, "name": params.Name},
		}, nil

	default:
		return nil, fmt.Errorf("Invalid action %q; expected \"create\", \"update\", or \"delete\".", params.Action)
	}
}

func executeManageSkill(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid manage_skill arguments: %w", err)
	}
	var params ManageSkillParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid manage_skill arguments: %w", err)
	}
	return ExecuteManageSkill(ctx, params)
}
