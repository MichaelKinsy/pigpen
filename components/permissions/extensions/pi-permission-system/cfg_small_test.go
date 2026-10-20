package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigPathsTwins(t *testing.T) {
	const f = "config/config-paths"
	agentDir, cwd, extensionRoot := "/home/user/.pi/agent", "/projects/my-app", "/opt/extensions/pi-permission-system"
	tw(t, f, "getGlobalConfigDir returns extensions/pi-permission-system under agentDir", func(t *testing.T) {
		eq(t, getGlobalConfigDir(agentDir), filepath.Join(agentDir, "extensions", "pi-permission-system"), "dir")
	})
	tw(t, f, "getGlobalConfigPath returns config.json under the global config dir", func(t *testing.T) {
		eq(t, getGlobalConfigPath(agentDir), filepath.Join(agentDir, "extensions", "pi-permission-system", "config.json"), "path")
	})
	tw(t, f, "getGlobalLogsDir returns logs under the global config dir", func(t *testing.T) {
		eq(t, getGlobalLogsDir(agentDir), filepath.Join(agentDir, "extensions", "pi-permission-system", "logs"), "dir")
	})
	tw(t, f, "getProjectConfigPath returns .pi/extensions/pi-permission-system/config.json under cwd", func(t *testing.T) {
		eq(t, getProjectConfigPath(cwd), filepath.Join(cwd, ".pi", "extensions", "pi-permission-system", "config.json"), "path")
	})
	tw(t, f, "getProjectAgentsDir returns .pi/agents under cwd", func(t *testing.T) {
		eq(t, getProjectAgentsDir(cwd), filepath.Join(cwd, ".pi", "agents"), "dir")
	})
	tw(t, f, "getLegacyGlobalPolicyPath returns pi-permissions.jsonc under agentDir", func(t *testing.T) {
		eq(t, getLegacyGlobalPolicyPath(agentDir), filepath.Join(agentDir, "pi-permissions.jsonc"), "path")
	})
	tw(t, f, "getLegacyProjectPolicyPath returns .pi/agent/pi-permissions.jsonc under cwd", func(t *testing.T) {
		eq(t, getLegacyProjectPolicyPath(cwd), filepath.Join(cwd, ".pi", "agent", "pi-permissions.jsonc"), "path")
	})
	tw(t, f, "getLegacyExtensionConfigPath returns config.json under extensionRoot", func(t *testing.T) {
		eq(t, getLegacyExtensionConfigPath(extensionRoot), filepath.Join(extensionRoot, "config.json"), "path")
	})
	tw(t, f, "DEBUG_LOG_FILENAME is a .jsonl file", func(t *testing.T) { eq(t, debugLogFilename, "pi-permission-system-debug.jsonl", "name") })
	tw(t, f, "REVIEW_LOG_FILENAME is a .jsonl file", func(t *testing.T) {
		eq(t, reviewLogFilename, "pi-permission-system-permission-review.jsonl", "name")
	})
}

func TestExtensionPathsTwins(t *testing.T) {
	const f = "config/extension-paths"
	old := nodeModulesRootDiscovery
	t.Cleanup(func() { nodeModulesRootDiscovery = old })
	discovered := "/mock/global/node_modules"
	nodeModulesRootDiscovery = func() string { return discovered }
	reset := func() { discovered = "/mock/global/node_modules" }
	slash := filepath.FromSlash
	harness := func() []string {
		var out []string
		for _, e := range []string{"agents", "extensions", "git", "npm", "prompts", "skills", "themes", "settings.json", "SYSTEM.md", "APPEND_SYSTEM.md", "AGENTS.md"} {
			out = append(out, slash("/test/agent/"+e))
		}
		return out
	}
	tw(t, f, "sets agentDir from argument", func(t *testing.T) {
		reset()
		eq(t, computeExtensionPaths("/test/agent", "").AgentDir, "/test/agent", "agentDir")
	})
	tw(t, f, "derives sessionsDir as agentDir/sessions", func(t *testing.T) {
		eq(t, computeExtensionPaths("/test/agent", "").SessionsDir, slash("/test/agent/sessions"), "sessionsDir")
	})
	tw(t, f, "derives subagentSessionsDir as agentDir/subagent-sessions", func(t *testing.T) {
		eq(t, computeExtensionPaths("/test/agent", "").SubagentSessionsDir, slash("/test/agent/subagent-sessions"), "dir")
	})
	tw(t, f, "derives forwardingDir as sessionsDir/permission-forwarding", func(t *testing.T) {
		eq(t, computeExtensionPaths("/test/agent", "").ForwardingDir, filepath.Join(slash("/test/agent/sessions"), "permission-forwarding"), "dir")
	})
	tw(t, f, "derives globalLogsDir via getGlobalLogsDir(agentDir)", func(t *testing.T) {
		eq(t, computeExtensionPaths("/test/agent", "").GlobalLogsDir, getGlobalLogsDir("/test/agent"), "dir")
	})
	tw(t, f, "excludes the package's own logs dir from infrastructure reads", func(t *testing.T) {
		eq(t, computeExtensionPaths("/test/agent", "").PiInfrastructureExcludedDirs, []string{getGlobalLogsDir("/test/agent")}, "excluded")
	})
	tw(t, f, "lists Pi's harness entries, the discovered root, and piPackageDir", func(t *testing.T) {
		reset()
		eq(t, computeExtensionPaths("/test/agent", "/pi/install").PiInfrastructureDirs, append(harness(), "/mock/global/node_modules", "/pi/install"), "dirs")
	})
	tw(t, f, "does not list agentDir itself, so its other entries are not infrastructure", func(t *testing.T) {
		for _, d := range computeExtensionPaths("/test/agent", "").PiInfrastructureDirs {
			eq(t, d == "/test/agent", false, "agentDir listed")
		}
	})
	tw(t, f, "omits global node_modules from piInfrastructureDirs when discovery returns null", func(t *testing.T) {
		discovered = ""
		eq(t, computeExtensionPaths("/test/agent", "").PiInfrastructureDirs, harness(), "dirs")
	})
	tw(t, f, "omits piPackageDir when not provided", func(t *testing.T) {
		reset()
		eq(t, computeExtensionPaths("/test/agent", "").PiInfrastructureDirs, append(harness(), "/mock/global/node_modules"), "dirs")
	})
	tw(t, f, "omits piPackageDir when given an empty string", func(t *testing.T) {
		for _, d := range computeExtensionPaths("/test/agent", "").PiInfrastructureDirs {
			eq(t, d == "", false, "empty entry")
		}
	})
	tw(t, f, "two calls with different agentDirs produce independent results", func(t *testing.T) {
		a, b := computeExtensionPaths("/agent/a", ""), computeExtensionPaths("/agent/b", "")
		eq(t, a.AgentDir, "/agent/a", "a")
		eq(t, b.AgentDir, "/agent/b", "b")
		eq(t, a.SessionsDir, slash("/agent/a/sessions"), "a sessions")
		eq(t, b.SessionsDir, slash("/agent/b/sessions"), "b sessions")
	})
}

func TestStatusTwins(t *testing.T) {
	tw(t, "config/status", "Permission-system status is only exposed when yolo mode is enabled", func(t *testing.T) {
		d := defaultExtensionConfig()
		eq(t, getPermissionSystemStatus(d), "", "default")
		d.YoloMode = true
		eq(t, getPermissionSystemStatus(d), "yolo", "yolo")
	})
}

func TestYamlFrontmatterTwins(t *testing.T) {
	const f = "config/yaml-frontmatter"
	tw(t, f, "returns empty string when no frontmatter delimiter", func(t *testing.T) {
		eq(t, extractFrontmatter("# Hello\nSome content"), "", "none")
	})
	tw(t, f, "returns empty string when only opening delimiter with no closing", func(t *testing.T) {
		eq(t, extractFrontmatter("---\nkey: value"), "", "unclosed")
	})
	tw(t, f, "returns frontmatter body between delimiters", func(t *testing.T) {
		eq(t, extractFrontmatter("---\nissue: 1\ntitle: Test\n---\n# Content"), "issue: 1\ntitle: Test", "body")
	})
	tw(t, f, "returns empty string when file does not start with ---", func(t *testing.T) {
		eq(t, extractFrontmatter("content\n---\nkey: val\n---"), "", "late")
	})
	tw(t, f, "handles CRLF line endings", func(t *testing.T) {
		eq(t, extractFrontmatter("---\r\nissue: 5\r\n---\r\n# Content"), "issue: 5", "crlf")
	})
	tw(t, f, "returns empty string for empty string input", func(t *testing.T) { eq(t, extractFrontmatter(""), "", "empty") })
	tw(t, f, "returns empty frontmatter for --- \\n--- with nothing between", func(t *testing.T) {
		eq(t, extractFrontmatter("---\n---\n# Content"), "", "empty")
	})
	y := func(s string) string { return marshalJSON(parseSimpleYamlMap(s), "") }
	tw(t, f, "returns empty object for empty string", func(t *testing.T) { eq(t, y(""), "{}", "empty") })
	tw(t, f, "parses simple key-value pairs", func(t *testing.T) { eq(t, y("issue: 21\ntitle: Test"), `{"issue":"21","title":"Test"}`, "map") })
	tw(t, f, "strips surrounding quotes from values", func(t *testing.T) {
		eq(t, y(`title: "My Title"`), `{"title":"My Title"}`, "dq")
		eq(t, y("title: 'My Title'"), `{"title":"My Title"}`, "sq")
	})
	tw(t, f, "skips lines without colon or with colon at position 0", func(t *testing.T) {
		r := parseSimpleYamlMap("no separator here\n:starts-with-colon: val\nkey: val")
		eq(t, r.vals["key"], "val", "key")
		_, has := r.vals["no separator here"]
		eq(t, has, false, "no separator")
	})
	tw(t, f, "skips comment lines", func(t *testing.T) { eq(t, y("# This is a comment\nkey: value"), `{"key":"value"}`, "map") })
	tw(t, f, "skips blank lines", func(t *testing.T) { eq(t, y("\n\nkey: value\n\n"), `{"key":"value"}`, "map") })
	tw(t, f, "parses nested map (child indented under parent)", func(t *testing.T) {
		eq(t, marshalJSON(parseSimpleYamlMap("parent:\n  child: nested_value").vals["parent"], ""), `{"child":"nested_value"}`, "parent")
	})
	tw(t, f, "handles multi-line values correctly (second line is new key)", func(t *testing.T) {
		r := parseSimpleYamlMap("key1: val1\nkey2: val2")
		eq(t, r.vals["key1"], "val1", "key1")
		eq(t, r.vals["key2"], "val2", "key2")
	})
	tw(t, f, "strips quotes from keys", func(t *testing.T) {
		eq(t, parseSimpleYamlMap(`"quoted-key": value`).vals["quoted-key"], "value", "key")
	})
}

func TestConfigIssueReporterTwins(t *testing.T) {
	const f = "config/config-issue-reporter"
	type rig struct {
		reporter *ConfigIssueReporter
		issues   *[]string
		warns    *[]string
	}
	mk := func(initial ...string) rig {
		issues, warns := &initial, &[]string{}
		src := issueSourceFunc(func() []string { return *issues })
		return rig{NewConfigIssueReporter(src, warnerFunc(func(m string) { *warns = append(*warns, m) })), issues, warns}
	}
	const a, b = "config issue A", "config issue B"
	tw(t, f, "warns the one issue the source answers", func(t *testing.T) {
		r := mk(a)
		r.reporter.Report()
		eq(t, *r.warns, []string{a}, "warns")
	})
	tw(t, f, "warns several issues as one message, newline-joined", func(t *testing.T) {
		r := mk(a, b)
		r.reporter.Report()
		eq(t, *r.warns, []string{a + "\n" + b}, "warns")
	})
	tw(t, f, "says nothing when the config has no issues", func(t *testing.T) {
		r := mk()
		r.reporter.Report()
		eq(t, len(*r.warns), 0, "warns")
	})
	tw(t, f, "does not re-warn an issue that has not changed", func(t *testing.T) {
		r := mk(a)
		r.reporter.Report()
		r.reporter.Report()
		eq(t, *r.warns, []string{a}, "warns")
	})
	tw(t, f, "warns only the issue that newly appeared", func(t *testing.T) {
		r := mk(a)
		r.reporter.Report()
		*r.issues = []string{a, b}
		r.reporter.Report()
		eq(t, *r.warns, []string{a, b}, "warns")
	})
	tw(t, f, "warns again an issue that went away and came back", func(t *testing.T) {
		r := mk(a)
		r.reporter.Report()
		*r.issues = nil
		r.reporter.Report()
		*r.issues = []string{a}
		r.reporter.Report()
		eq(t, *r.warns, []string{a, a}, "warns")
	})
}

type issueSourceFunc func() []string

func (f issueSourceFunc) GetConfigIssues() []string { return f() }

type warnerFunc func(string)

func (f warnerFunc) Warn(m string) { f(m) }

func TestConfigReporterTwins(t *testing.T) {
	const f = "config/config-reporter"
	sp := func(s string) *string { return &s }
	tw(t, f, "buildResolvedConfigLogEntry includes policy paths and legacy detection flags", func(t *testing.T) {
		p := ResolvedPolicyPaths{GlobalConfigPath: "/home/user/.pi/agent/extensions/pi-permission-system/config.json", GlobalConfigExists: true,
			ProjectConfigPath: sp("/projects/my-app/.pi/extensions/pi-permission-system/config.json"), AgentsDir: "/home/user/.pi/agent/agents", AgentsDirExists: true,
			ProjectAgentsDir: sp("/projects/my-app/.pi/agent/agents")}
		r := buildResolvedConfigLogEntry(BuildResolvedConfigLogEntryOptions{PolicyPaths: p})
		eq(t, r.GlobalConfigPath, p.GlobalConfigPath, "global")
		eq(t, r.GlobalConfigExists, true, "global exists")
		eq(t, *r.ProjectConfigPath, *p.ProjectConfigPath, "project")
		eq(t, r.ProjectConfigExists, false, "project exists")
		eq(t, r.AgentsDir, "/home/user/.pi/agent/agents", "agents")
		eq(t, r.AgentsDirExists, true, "agents exists")
		eq(t, *r.ProjectAgentsDir, "/projects/my-app/.pi/agent/agents", "project agents")
		eq(t, r.ProjectAgentsDirExists, false, "project agents exists")
		eq(t, r.LegacyGlobalPolicyDetected || r.LegacyProjectPolicyDetected || r.LegacyExtensionConfigDetected, false, "legacy")
	})
	tw(t, f, "buildResolvedConfigLogEntry handles null project paths", func(t *testing.T) {
		r := buildResolvedConfigLogEntry(BuildResolvedConfigLogEntryOptions{PolicyPaths: ResolvedPolicyPaths{GlobalConfigPath: "/g", AgentsDir: "/a"}})
		eq(t, r.ProjectConfigPath == nil, true, "project null")
		eq(t, r.ProjectConfigExists, false, "project exists")
		eq(t, r.ProjectAgentsDir == nil, true, "agents null")
		eq(t, r.ProjectAgentsDirExists, false, "agents exists")
	})
	tw(t, f, "buildResolvedConfigLogEntry surfaces legacy detection flags", func(t *testing.T) {
		r := buildResolvedConfigLogEntry(BuildResolvedConfigLogEntryOptions{PolicyPaths: ResolvedPolicyPaths{GlobalConfigPath: "/g"},
			LegacyGlobalPolicyDetected: true, LegacyExtensionConfigDetected: true})
		eq(t, r.LegacyGlobalPolicyDetected, true, "global")
		eq(t, r.LegacyProjectPolicyDetected, false, "project")
		eq(t, r.LegacyExtensionConfigDetected, true, "ext")
	})
	tskip(t, f, "config.resolved entry appears in review log via logger", "writes the entry through createPermissionSystemLogger and builds a PermissionManager, both of other lanes (logging, policy); the entry's field order and null project paths are asserted by TestResolvedConfigLogEntryObject")
}

func TestResolvedConfigLogEntryObject(t *testing.T) {
	e := buildResolvedConfigLogEntry(BuildResolvedConfigLogEntryOptions{PolicyPaths: ResolvedPolicyPaths{GlobalConfigPath: "/g", GlobalConfigExists: true, AgentsDir: "/a"}})
	eq(t, marshalJSON(e.toObject(), ""), `{"globalConfigPath":"/g","globalConfigExists":true,"projectConfigPath":null,"projectConfigExists":false,`+
		`"agentsDir":"/a","agentsDirExists":false,"projectAgentsDir":null,"projectAgentsDirExists":false,"legacyGlobalPolicyDetected":false,`+
		`"legacyProjectPolicyDetected":false,"legacyExtensionConfigDetected":false}`, "entry")
}

func TestExtensionConfigTwins(t *testing.T) {
	const f = "config/extension-config"
	keys := func(s string) []string { return detectMisplacedPermissionKeys(jo(t, s)) }
	tw(t, f, "returns an empty array for a record with only valid extension keys", func(t *testing.T) {
		eq(t, keys(`{"debugLog":true,"permissionReviewLog":true,"yoloMode":false}`), []string{}, "keys")
	})
	tw(t, f, "returns an empty array for an empty record", func(t *testing.T) { eq(t, keys(`{}`), []string{}, "keys") })
	tw(t, f, "returns misplaced key names when legacy permission-rule keys are present", func(t *testing.T) {
		eq(t, keys(`{"debugLog":true,"defaultPolicy":{"tools":"ask"},"bash":{"git status":"allow"}}`), []string{"defaultPolicy", "bash"}, "keys")
	})
	tw(t, f, "detects all known legacy permission-rule keys", func(t *testing.T) {
		eq(t, keys(`{"defaultPolicy":{},"tools":{},"bash":{},"mcp":{},"skills":{},"special":{},"external_directory":{}}`),
			[]string{"defaultPolicy", "tools", "bash", "mcp", "skills", "special", "external_directory"}, "keys")
	})
	tw(t, f, "does not detect doom_loop as a misplaced permission key", func(t *testing.T) { eq(t, keys(`{"doom_loop":{}}`), []string{}, "keys") })
	tw(t, f, "does not flag the new flat-format permission key as misplaced", func(t *testing.T) {
		eq(t, keys(`{"debugLog":false,"permission":{"*":"ask"}}`), []string{}, "keys")
	})
	tw(t, f, "ignores unknown keys that are not permission-rule keys", func(t *testing.T) {
		eq(t, keys(`{"debugLog":true,"someRandomKey":"value"}`), []string{}, "keys")
	})
	norm := func(s string) PermissionSystemExtensionConfig { return normalizePermissionSystemConfig(cfg(t, s)) }
	tw(t, f, "normalizes a valid config object", func(t *testing.T) {
		eq(t, norm(`{"debugLog":true,"permissionReviewLog":false,"yoloMode":true}`),
			PermissionSystemExtensionConfig{DebugLog: true, PermissionReviewLog: false, YoloMode: true, DoublePressToConfirm: true}, "config")
	})
	tw(t, f, "defaults debugLog to false when missing", func(t *testing.T) { eq(t, norm(`{}`).DebugLog, false, "debugLog") })
	tw(t, f, "defaults permissionReviewLog to true when missing", func(t *testing.T) { eq(t, norm(`{}`).PermissionReviewLog, true, "log") })
	tw(t, f, "defaults yoloMode to false when missing", func(t *testing.T) { eq(t, norm(`{}`).YoloMode, false, "yolo") })
	tw(t, f, "defaults doublePressToConfirm to true when missing", func(t *testing.T) { eq(t, norm(`{}`).DoublePressToConfirm, true, "double") })
	tw(t, f, "sets doublePressToConfirm false when explicitly disabled", func(t *testing.T) {
		eq(t, norm(`{"doublePressToConfirm":false}`).DoublePressToConfirm, false, "double")
	})
	tw(t, f, "includes forwardingTimeoutMs when a valid positive integer is provided", func(t *testing.T) {
		eq(t, *norm(`{"forwardingTimeoutMs":120000}`).ForwardingTimeoutMs, 120000.0, "ms")
	})
	tw(t, f, "omits forwardingTimeoutMs when absent", func(t *testing.T) { eq(t, norm(`{}`).ForwardingTimeoutMs == nil, true, "absent") })
	tw(t, f, "includes the prompt-budget knobs when provided", func(t *testing.T) {
		r := norm(`{"promptMaxRows":12,"promptFieldMaxWidth":80}`)
		eq(t, *r.PromptMaxRows, 12.0, "rows")
		eq(t, *r.PromptFieldMaxWidth, 80.0, "width")
	})
	tw(t, f, "omits the prompt-budget knobs when absent, leaving the renderer's defaults", func(t *testing.T) {
		r := norm(`{}`)
		eq(t, r.PromptMaxRows == nil && r.PromptFieldMaxWidth == nil, true, "absent")
	})
	tw(t, f, "includes the review-log field width when provided", func(t *testing.T) {
		eq(t, *norm(`{"reviewLogFieldMaxWidth":200}`).ReviewLogFieldMaxWidth, 200.0, "width")
	})
	tw(t, f, "omits the review-log field width when absent, leaving the writer's default", func(t *testing.T) {
		eq(t, norm(`{}`).ReviewLogFieldMaxWidth == nil, true, "absent")
	})
	tw(t, f, "drops the deprecated preview caps even when the config sets them", func(t *testing.T) {
		// the runtime config has no field for either cap (checked by the compiler); the other fields stay at their defaults
		r := norm(`{"toolInputPreviewMaxLength":400,"toolTextSummaryMaxLength":120}`)
		eq(t, r, defaultExtensionConfig(), "unchanged")
	})
	tw(t, f, "includes shellTools when provided", func(t *testing.T) {
		eq(t, permJSON(norm(`{"shellTools":{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"}}}`).ShellTools),
			`{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"}}`, "shellTools")
	})
	tw(t, f, "omits shellTools when absent", func(t *testing.T) { eq(t, norm(`{}`).ShellTools == nil, true, "absent") })
	tw(t, f, "includes authorizerChain when provided", func(t *testing.T) {
		eq(t, norm(`{"authorizerChain":["model-judge","typo-reviewer"]}`).AuthorizerChain, []string{"model-judge", "typo-reviewer"}, "chain")
	})
	tw(t, f, "omits authorizerChain when absent", func(t *testing.T) { eq(t, norm(`{}`).AuthorizerChain == nil, true, "absent") })
	tw(t, f, "includes permissionDialogKeys when provided", func(t *testing.T) {
		eq(t, norm(`{"permissionDialogKeys":{"approve":"1","deny":"4"}}`).PermissionDialogKeys, DialogKeyOverrides{ActionApprove: "1", ActionDeny: "4"}, "keys")
	})
	tw(t, f, "omits permissionDialogKeys when absent", func(t *testing.T) { eq(t, norm(`{}`).PermissionDialogKeys == nil, true, "absent") })
	tw(t, f, "includes promptNotifications when provided", func(t *testing.T) {
		eq(t, norm(`{"promptNotifications":["bell","osc777"]}`).PromptNotifications, []string{"bell", "osc777"}, "list")
	})
	tw(t, f, "omits promptNotifications when absent", func(t *testing.T) { eq(t, norm(`{}`).PromptNotifications == nil, true, "absent") })
	tw(t, f, "creates the logs directory owner-only", func(t *testing.T) {
		logsDir := filepath.Join(t.TempDir(), "extensions", "pi-permission-system", "logs")
		eq(t, ensurePermissionSystemLogsDirectory(logsDir), "", "undefined")
		if runtime.GOOS != "windows" {
			st, err := os.Stat(logsDir)
			if err != nil {
				t.Fatal(err)
			}
			eq(t, st.Mode().Perm(), os.FileMode(0o700), "mode")
		}
	})
	tw(t, f, "tightens a directory inherited from an earlier version", func(t *testing.T) {
		logsDir := filepath.Join(t.TempDir(), "logs")
		if err := os.Mkdir(logsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(logsDir, 0o755)
		eq(t, ensurePermissionSystemLogsDirectory(logsDir), "", "undefined")
		if runtime.GOOS != "windows" {
			st, _ := os.Stat(logsDir)
			eq(t, st.Mode().Perm(), os.FileMode(0o700), "mode")
		}
	})
	tw(t, f, "returns true when yoloMode is true", func(t *testing.T) {
		eq(t, isYoloModeEnabled(PermissionSystemExtensionConfig{YoloMode: true}), true, "yolo")
	})
	tw(t, f, "returns false when yoloMode is false", func(t *testing.T) { eq(t, isYoloModeEnabled(PermissionSystemExtensionConfig{}), false, "yolo") })
	tw(t, f, "returns false when yoloMode is undefined", func(t *testing.T) {
		// an untyped caller's missing field is the Go zero value
		eq(t, isYoloModeEnabled(PermissionSystemExtensionConfig{}), false, "yolo")
	})
}

func TestConfigPipelineTwins(t *testing.T) {
	const f = "config/config-pipeline"
	run := func(t *testing.T, global string) (MergedConfigResult, PermissionSystemExtensionConfig) {
		r := newMergeRig(t)
		if global != "" {
			r.global(global)
		}
		res := r.load()
		return res, normalizePermissionSystemConfig(res.Merged)
	}
	tw(t, f, "a runtime knob survives the full pipeline", func(t *testing.T) {
		_, c := run(t, `{"debugLog":true}`)
		eq(t, c.DebugLog, true, "debugLog")
	})
	tw(t, f, "a deprecated preview cap reaches the merge intermediate but not the runtime config", func(t *testing.T) {
		res, c := run(t, `{"toolInputPreviewMaxLength":1000}`)
		eq(t, *res.Merged.ToolInputPreviewMaxLength, 1000.0, "merged")
		eq(t, c, defaultExtensionConfig(), "runtime config carries no cap")
	})
	tw(t, f, "dialog budget fields survive the full pipeline and resolve to a render budget", func(t *testing.T) {
		_, c := run(t, `{"promptMaxRows":12,"promptFieldMaxWidth":80}`)
		eq(t, *c.PromptMaxRows, 12.0, "rows")
		eq(t, *c.PromptFieldMaxWidth, 80.0, "width")
	})
	tskip(t, f, "falls back to the default render budget when the config names neither field",
		"asserts resolveRenderBudget and DEFAULT_RENDER_BUDGET of presentation/dialog-renderer, which the presentation lane ports; the config half (both fields absent) is asserted by TestPipelineLeavesTheBudgetUnset")
	tw(t, f, "a deprecated text-summary cap likewise stops at the merge intermediate", func(t *testing.T) {
		res, c := run(t, `{"toolTextSummaryMaxLength":250}`)
		eq(t, *res.Merged.ToolTextSummaryMaxLength, 250.0, "merged")
		eq(t, c, defaultExtensionConfig(), "runtime config carries no cap")
	})
	tw(t, f, "project config still overrides a global deprecated cap in the merge", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"toolInputPreviewMaxLength":200}`)
		r.project(`{"toolInputPreviewMaxLength":500}`)
		eq(t, *r.load().Merged.ToolInputPreviewMaxLength, 500.0, "merged")
	})
	tw(t, f, "defaults apply when config file is absent", func(t *testing.T) {
		_, c := run(t, "")
		eq(t, c.DebugLog, false, "debugLog")
		eq(t, c.PermissionReviewLog, true, "log")
		eq(t, c.YoloMode, false, "yolo")
	})
}

func TestPipelineLeavesTheBudgetUnset(t *testing.T) {
	r := newMergeRig(t)
	c := normalizePermissionSystemConfig(r.load().Merged)
	eq(t, c.PromptMaxRows == nil && c.PromptFieldMaxWidth == nil, true, "unset: the renderer's defaults apply")
}
