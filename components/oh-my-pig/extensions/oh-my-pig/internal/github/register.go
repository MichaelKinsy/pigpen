package github

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

//go:embed github.md
var githubPrompt string

// Enabled reads a boolean setting key with default def. The settings lane
// replaces this variable with a host-backed reader; the default returns def
// (false for github.enabled, true for github.cache.enabled).
var Enabled = func(_ sdk.Context, _ string, def bool) bool {
	return def
}

// githubSchema matches omp's githubSchema in packages/coding-agent/src/tools/gh.ts:80-106.
func githubSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type": "string",
				"enum": []string{
					"repo_view",
					"file_read",
					"pr_create",
					"pr_checkout",
					"pr_push",
					"search_issues",
					"search_prs",
					"search_code",
					"search_commits",
					"search_repos",
					"run_watch",
				},
				"description": "github operation",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "owner/repo",
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "branch",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "repository-relative file path",
			},
			"pr": map[string]any{
				"anyOf": []map[string]any{
					{"type": "string"},
					{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"description": "pr number, url, or branch",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "reset existing local branch",
			},
			"forceWithLease": map[string]any{
				"type":        "boolean",
				"description": "force-with-lease push",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "pr title",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "pr body markdown",
			},
			"base": map[string]any{
				"type":        "string",
				"description": "pr base branch",
			},
			"head": map[string]any{
				"type":        "string",
				"description": "pr head branch",
			},
			"draft": map[string]any{
				"type":        "boolean",
				"description": "open pr as draft",
			},
			"fill": map[string]any{
				"type":        "boolean",
				"description": "auto-fill pr title/body from commits",
			},
			"reviewer": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "reviewers",
			},
			"assignee": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "assignees",
			},
			"label": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "labels",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "search query",
			},
			"since": map[string]any{
				"type":        "string",
				"description": "lower-bound date filter",
			},
			"until": map[string]any{
				"type":        "string",
				"description": "upper-bound date filter",
			},
			"dateField": map[string]any{
				"type":        "string",
				"enum":        []string{"created", "updated"},
				"description": "date field",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "max results",
			},
			"run": map[string]any{
				"type":        "string",
				"description": "actions run id or url",
			},
			"tail": map[string]any{
				"type":        "number",
				"description": "log lines per failed job",
			},
		},
		"required":             []string{"op"},
		"additionalProperties": false,
	}
}

func optString(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return strings.TrimSpace(v)
}

func optBool(params map[string]any, key string) bool {
	v, _ := params[key].(bool)
	return v
}

func optStringOrSlice(params map[string]any, key string) []string {
	raw, exists := params[key]
	if !exists || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return []string{s}
		}
	case []string:
		var out []string
		for _, item := range v {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		return out
	}
	return nil
}

func sessionIDForSDK(sdkCtx sdk.Context) string {
	var zero sdk.Context
	if sdkCtx == zero {
		return ""
	}
	if sid, err := sdkCtx.GetSessionID(); err == nil && sid != "" {
		return sid
	}
	return ""
}

func handleGithubTool(sdkCtx sdk.Context, params map[string]any) (any, error) {
	var zero sdk.Context
	var ctx context.Context
	var cwd string
	if sdkCtx != zero {
		ctx = sdkCtx.Signal()
		cwd = sdkCtx.Cwd()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withSessionID(ctx, sessionIDForSDK(sdkCtx))
	op := optString(params, "op")
	switch op {
	case "repo_view":
		return executeRepoView(ctx, cwd, params)
	case "file_read":
		return executeFileRead(ctx, cwd, params)
	case "pr_create":
		if err := gate.Check(sdkCtx, gate.Request{Tool: "github", Op: gate.OpExec, Detail: "pr_create"}); err != nil {
			return nil, err
		}
		return executePrCreate(ctx, cwd, params)
	case "pr_checkout":
		if err := gate.Check(sdkCtx, gate.Request{Tool: "github", Op: gate.OpExec, Detail: "pr_checkout"}); err != nil {
			return nil, err
		}
		return executePrCheckout(ctx, sdkCtx, cwd, params)
	case "pr_push":
		if err := gate.Check(sdkCtx, gate.Request{Tool: "github", Op: gate.OpExec, Detail: "pr_push"}); err != nil {
			return nil, err
		}
		return executePrPush(ctx, sdkCtx, cwd, params)
	case "search_issues":
		return executeSearchIssuesOrPrs(ctx, cwd, params, false)
	case "search_prs":
		return executeSearchIssuesOrPrs(ctx, cwd, params, true)
	case "search_code":
		return executeSearchCode(ctx, cwd, params)
	case "search_commits":
		return executeSearchCommits(ctx, cwd, params)
	case "search_repos":
		return executeSearchRepos(ctx, cwd, params)
	case "run_watch":
		return executeRunWatch(ctx, sdkCtx, cwd, params)
	default:
		return nil, fmt.Errorf("unsupported github op: %q", op)
	}
}
func endGithubSession(sessionID string) {
	if sessionID == "" {
		return
	}
	clearGhTrustedHostsCache(sessionID)
	liveProcs.killSession(sessionID)
}

// Register wires omp's `github` tool (discoverable via `toolcmd.Mountable`,
// default off via `github.enabled`, registered with `ToolExposureCodemode` so
// `Context.ExecuteTool` can invoke it when mounted as a bash command),
// package-level lifecycle cleanup, and the `issue://`, `pr://`, and `mr://`
// internal URL handlers (always on).
func Register(e *sdk.Extension) {
	defaultOff := false
	ghDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "github",
		Label:         "GitHub",
		Description:   strings.TrimSpace(githubPrompt),
		Parameters:    githubSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       handleGithubTool,
	})
	e.RegisterTool(intent.Tool(ghDef))
	intent.RegisterSync(e, ghDef)

	toolcmd.Mountable("github", func(ctx sdk.Context) bool {
		return Enabled(ctx, SettingEnabled, false) && ghAvailable()
	})
	lifecycle.OnEnd("github", endGithubSession)

	_ = urls.Register(issueHandler{})
	_ = urls.Register(prHandler{})
	_ = urls.Register(mrHandler{})
}
