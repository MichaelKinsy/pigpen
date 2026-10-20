package pi_permission_system

import (
	"path/filepath"
	"strings"
	"testing"
)

const fLoader = "config/config-loader"

func TestStripJSONCommentsTwins(t *testing.T) {
	tw(t, fLoader, "returns empty string for empty input", func(t *testing.T) { eq(t, stripJSONComments(""), "", "empty") })
	tw(t, fLoader, "passes through plain JSON unchanged", func(t *testing.T) {
		in := `{"key": true}`
		eq(t, stripJSONComments(in), in, "plain")
	})
	tw(t, fLoader, "drops a line comment body and preserves the trailing newline", func(t *testing.T) {
		eq(t, stripJSONComments("{ // comment\n\"k\": 1}"), "{ \n\"k\": 1}", "line comment")
	})
	tw(t, fLoader, "drops a line comment that runs to EOF with no trailing newline", func(t *testing.T) {
		eq(t, stripJSONComments(`{"k": 1} // trailing`), `{"k": 1} `, "eof comment")
	})
	tw(t, fLoader, "drops a block comment and nothing else", func(t *testing.T) {
		eq(t, stripJSONComments(`{ /* block */ "k": 1}`), `{  "k": 1}`, "block")
	})
	tw(t, fLoader, "drops an unterminated block comment to EOF", func(t *testing.T) {
		eq(t, stripJSONComments("{ /* no close"), "{ ", "unterminated")
	})
	tw(t, fLoader, "preserves // inside a double-quoted string", func(t *testing.T) {
		in := `{"url": "http://example.com"}`
		eq(t, stripJSONComments(in), in, "dq")
	})
	tw(t, fLoader, "preserves block-comment markers inside a double-quoted string", func(t *testing.T) {
		in := `{"v": "a /* b */ c"}`
		eq(t, stripJSONComments(in), in, "dq block")
	})
	tw(t, fLoader, "preserves // inside a single-quoted string", func(t *testing.T) {
		in := "{'url': 'http://x.com'}"
		eq(t, stripJSONComments(in), in, "sq")
	})
	tw(t, fLoader, "preserves block-comment markers inside a single-quoted string", func(t *testing.T) {
		in := "{'v': 'a /* b */ c'}"
		eq(t, stripJSONComments(in), in, "sq block")
	})
	tw(t, fLoader, "honors a backslash-escaped quote so it does not close the string", func(t *testing.T) {
		in := `{"k": "a\"b"}`
		eq(t, stripJSONComments(in), in, "escaped quote")
	})
	tw(t, fLoader, "emits an unterminated string to EOF verbatim", func(t *testing.T) {
		in := `{"k": "unterminated`
		eq(t, stripJSONComments(in), in, "unterminated string")
	})
	tw(t, fLoader, "preserves a lone slash that is not part of // or /*", func(t *testing.T) {
		in := `{"v": 1/2}`
		eq(t, stripJSONComments(in), in, "lone slash")
	})
	tw(t, fLoader, "handles a combined JSONC document that round-trips to valid JSON", func(t *testing.T) {
		jsonc := strings.Join([]string{"{", `  "debugLog": true, // runtime knob`, `  "permission": { /* the policy */ "*": "ask" }`, "}"}, "\n")
		v, err := parseJSON([]byte(stripJSONComments(jsonc)))
		if err != nil {
			t.Fatal(err)
		}
		o := v.(*jsObject)
		eq(t, o.vals["debugLog"], true, "debugLog")
		eq(t, marshalJSON(o.vals["permission"], ""), `{"*":"ask"}`, "permission")
	})
}

func TestLoadUnifiedConfigTwins(t *testing.T) {
	tw(t, fLoader, "parses a valid JSON file with runtime knobs and flat permission", func(t *testing.T) {
		r := loadedAt(t, `{"debugLog":true,"permissionReviewLog":false,"yoloMode":true,"permission":{"*":"ask","read":"allow","bash":{"git status":"allow"}}}`)
		eq(t, r.Issues, []string(nil), "issues")
		eq(t, *r.Config.DebugLog, true, "debugLog")
		eq(t, *r.Config.PermissionReviewLog, false, "permissionReviewLog")
		eq(t, *r.Config.YoloMode, true, "yoloMode")
		eq(t, permJSON(r.Config.Permission), `{"*":"ask","bash":{"git status":"allow"},"read":"allow"}`, "permission")
	})
	tw(t, fLoader, "strips JSONC comments before parsing", func(t *testing.T) {
		r := loadedAt(t, "{\n  // This is a comment\n  \"debugLog\": true,\n  /* block comment */\n  \"permission\": { \"*\": \"ask\" }\n}")
		eq(t, r.Issues, []string(nil), "issues")
		eq(t, *r.Config.DebugLog, true, "debugLog")
		eq(t, permJSON(r.Config.Permission), `{"*":"ask"}`, "permission")
	})
	tw(t, fLoader, "rejects unknown keys with a clear issue and empty config", func(t *testing.T) {
		r := loadedAt(t, `{"debugLog":false,"unknownField":"ignored","anotherRandom":42}`)
		containsAll(t, strings.Join(r.Issues, "\n"), "unknownField", "anotherRandom")
		// fail closed: the whole scope is rejected, dropping the valid field too
		eq(t, isEmptyConfig(r.Config), true, "empty")
	})
	tw(t, fLoader, "returns empty config and no issues when the file does not exist", func(t *testing.T) {
		r := loadUnifiedConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
		eq(t, len(r.Issues), 0, "issues")
		eq(t, r.Config.DebugLog == nil, true, "debugLog")
		eq(t, r.Config.Permission == nil, true, "permission")
	})
	tw(t, fLoader, "returns empty config and an issue when the file contains invalid JSON", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "config.json")
		writeTestFile(t, p, "not valid json {{{")
		r := loadUnifiedConfig(p)
		eq(t, len(r.Issues), 1, "one issue")
		containsAll(t, r.Issues[0], p)
	})
	tw(t, fLoader, "rejects non-boolean runtime knobs with a clear issue", func(t *testing.T) {
		r := loadedAt(t, `{"debugLog":"yes","permissionReviewLog":1,"yoloMode":null}`)
		eq(t, isEmptyConfig(r.Config), true, "empty")
		containsAll(t, strings.Join(r.Issues, "\n"), "debugLog")
	})
	tw(t, fLoader, "fails closed: an invalid field contributes no permission rules", func(t *testing.T) {
		r := loadedAt(t, `{"permission":{"*":"allow","read":"allow"},"toolInputPreviewMaxLength":-5}`)
		eq(t, isEmptyConfig(r.Config), true, "empty")
		eq(t, r.Config.Permission == nil, true, "no permission")
		eq(t, len(r.Issues) > 0, true, "issues")
	})
	tw(t, fLoader, "rejects an invalid PermissionState inside a permission map", func(t *testing.T) {
		r := loadedAt(t, `{"permission":{"read":"allow","write":"invalid","bash":{"git *":"ask","rm -rf":42}}}`)
		eq(t, isEmptyConfig(r.Config), true, "empty")
		eq(t, len(r.Issues) > 0, true, "issues")
	})
	tw(t, fLoader, "accepts permission as object with mixed string and object values", func(t *testing.T) {
		r := loadedAt(t, `{"permission":{"*":"ask","read":"allow","bash":{"*":"ask","git *":"allow"},"external_directory":"ask"}}`)
		eq(t, len(r.Issues), 0, "issues")
		eq(t, permJSON(r.Config.Permission), `{"*":"ask","external_directory":"ask","bash":{"*":"ask","git *":"allow"},"read":"allow"}`, "permission")
	})
	tw(t, fLoader, "preserves a deny-with-reason object inside a pattern map", func(t *testing.T) {
		r := loadedAt(t, `{"permission":{"bash":{"git *":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"}}}}`)
		eq(t, permJSON(r.Config.Permission), `{"bash":{"git *":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"}}}`, "permission")
	})
	tw(t, fLoader, "rejects a deny object with a non-string reason", func(t *testing.T) {
		r := loadedAt(t, `{"permission":{"bash":{"git *":"allow","npm *":{"action":"deny","reason":42}}}}`)
		eq(t, isEmptyConfig(r.Config), true, "empty")
		eq(t, len(r.Issues) > 0, true, "issues")
	})
	tw(t, fLoader, "returns no permission when the permission field is absent", func(t *testing.T) {
		eq(t, loadedAt(t, `{"debugLog":false}`).Config.Permission == nil, true, "absent")
	})
	tw(t, fLoader, "rejects a non-object permission field", func(t *testing.T) {
		r := loadedAt(t, `{"permission":"allow"}`)
		eq(t, isEmptyConfig(r.Config), true, "empty")
		eq(t, len(r.Issues) > 0, true, "issues")
	})
	tw(t, fLoader, "parses forwardingTimeoutMs when a valid positive integer is present", func(t *testing.T) {
		eq(t, *loadedAt(t, `{"forwardingTimeoutMs":120000}`).Config.ForwardingTimeoutMs, 120000.0, "value")
	})
	tw(t, fLoader, "rejects a non-positive forwardingTimeoutMs", func(t *testing.T) {
		eq(t, len(loadedAt(t, `{"forwardingTimeoutMs":0}`).Issues) > 0, true, "issues")
	})
	tw(t, fLoader, "parses toolInputPreviewMaxLength when a valid positive integer is present", func(t *testing.T) {
		eq(t, *loadedAt(t, `{"toolInputPreviewMaxLength":1000}`).Config.ToolInputPreviewMaxLength, 1000.0, "value")
	})
	tw(t, fLoader, "parses toolTextSummaryMaxLength when a valid positive integer is present", func(t *testing.T) {
		eq(t, *loadedAt(t, `{"toolTextSummaryMaxLength":120}`).Config.ToolTextSummaryMaxLength, 120.0, "value")
	})
	tw(t, fLoader, "omits toolInputPreviewMaxLength when absent", func(t *testing.T) {
		eq(t, loadedAt(t, `{"debugLog":false}`).Config.ToolInputPreviewMaxLength == nil, true, "absent")
	})
	tw(t, fLoader, "omits toolTextSummaryMaxLength when absent", func(t *testing.T) {
		eq(t, loadedAt(t, `{"debugLog":false}`).Config.ToolTextSummaryMaxLength == nil, true, "absent")
	})
	// the upstream it.each cases (invalid caps, invalid piInfrastructureReadPaths) carry a %s title; run them as subtests here
	t.Run("rejects invalid toolInputPreviewMaxLength", func(t *testing.T) {
		for _, v := range []string{"0", "-1", "1.5", `"200"`, "true"} {
			r := loadedAt(t, `{"toolInputPreviewMaxLength":`+v+`}`)
			eq(t, isEmptyConfig(r.Config) && len(r.Issues) > 0, true, v)
		}
	})
	t.Run("rejects invalid toolTextSummaryMaxLength", func(t *testing.T) {
		for _, v := range []string{"0", "-1", "1.5", `"80"`, "false"} {
			r := loadedAt(t, `{"toolTextSummaryMaxLength":`+v+`}`)
			eq(t, isEmptyConfig(r.Config) && len(r.Issues) > 0, true, v)
		}
	})
	tw(t, fLoader, "parses piInfrastructureReadPaths when a valid string array is present", func(t *testing.T) {
		eq(t, loadedAt(t, `{"piInfrastructureReadPaths":["/extra/path"]}`).Config.PiInfrastructureReadPaths, []string{"/extra/path"}, "paths")
	})
	tw(t, fLoader, "parses piInfrastructureReadPaths as empty array when set to []", func(t *testing.T) {
		eq(t, loadedAt(t, `{"piInfrastructureReadPaths":[]}`).Config.PiInfrastructureReadPaths, []string{}, "paths")
	})
	tw(t, fLoader, "omits piInfrastructureReadPaths when absent", func(t *testing.T) {
		eq(t, loadedAt(t, `{"debugLog":false}`).Config.PiInfrastructureReadPaths == nil, true, "absent")
	})
	t.Run("rejects invalid piInfrastructureReadPaths", func(t *testing.T) {
		for _, v := range []string{`"not-an-array"`, "42", `["a",1]`, `{"a":"b"}`} {
			r := loadedAt(t, `{"piInfrastructureReadPaths":`+v+`}`)
			eq(t, isEmptyConfig(r.Config) && len(r.Issues) > 0, true, v)
		}
	})
}

func TestMergeUnifiedConfigsTwins(t *testing.T) {
	merge := func(base, override string) UnifiedConfig { return mergeUnifiedConfigs(cfg(t, base), cfg(t, override)) }
	tw(t, fLoader, "deep-merges permission objects so project overrides global per-key", func(t *testing.T) {
		m := merge(`{"permission":{"*":"ask","read":"allow","bash":{"git status":"allow"}}}`, `{"permission":{"*":"allow","bash":{"rm -rf *":"deny"}}}`)
		eq(t, permJSON(m.Permission), `{"*":"allow","bash":{"git status":"allow","rm -rf *":"deny"},"read":"allow"}`, "permission")
	})
	tw(t, fLoader, "string permission value in override replaces base string for same key", func(t *testing.T) {
		eq(t, permJSON(merge(`{"permission":{"read":"ask"}}`, `{"permission":{"read":"allow"}}`).Permission), `{"read":"allow"}`, "permission")
	})
	tw(t, fLoader, "object replaces string when override uses object for same surface", func(t *testing.T) {
		eq(t, permJSON(merge(`{"permission":{"bash":"ask"}}`, `{"permission":{"bash":{"*":"allow","rm -rf *":"deny"}}}`).Permission),
			`{"bash":{"*":"allow","rm -rf *":"deny"}}`, "permission")
	})
	tw(t, fLoader, "string replaces object when override uses string for same surface", func(t *testing.T) {
		eq(t, permJSON(merge(`{"permission":{"bash":{"git *":"allow"}}}`, `{"permission":{"bash":"deny"}}`).Permission), `{"bash":"deny"}`, "permission")
	})
	tw(t, fLoader, "replaces scalar runtime knobs (project wins)", func(t *testing.T) {
		m := merge(`{"debugLog":true,"permissionReviewLog":true,"yoloMode":false,"doublePressToConfirm":true}`,
			`{"debugLog":false,"yoloMode":true,"doublePressToConfirm":false}`)
		eq(t, *m.DebugLog, false, "debugLog")
		eq(t, *m.PermissionReviewLog, true, "permissionReviewLog")
		eq(t, *m.YoloMode, true, "yoloMode")
		eq(t, *m.DoublePressToConfirm, false, "doublePress")
	})
	tw(t, fLoader, "replaces the prompt-budget scalars (project wins)", func(t *testing.T) {
		m := merge(`{"promptMaxRows":24,"promptFieldMaxWidth":400}`, `{"promptMaxRows":10}`)
		eq(t, *m.PromptMaxRows, 10.0, "rows")
		eq(t, *m.PromptFieldMaxWidth, 400.0, "width")
	})
	tw(t, fLoader, "replaces the review-log field width (project wins)", func(t *testing.T) {
		eq(t, *merge(`{"reviewLogFieldMaxWidth":1000}`, `{"reviewLogFieldMaxWidth":200}`).ReviewLogFieldMaxWidth, 200.0, "width")
	})
	tw(t, fLoader, "carries the review-log field width from the base when unoverridden", func(t *testing.T) {
		eq(t, *merge(`{"reviewLogFieldMaxWidth":200}`, `{}`).ReviewLogFieldMaxWidth, 200.0, "width")
	})
	tw(t, fLoader, "returns base unchanged when override is empty", func(t *testing.T) {
		m := merge(`{"debugLog":true,"permission":{"read":"allow"}}`, `{}`)
		eq(t, *m.DebugLog, true, "debugLog")
		eq(t, permJSON(m.Permission), `{"read":"allow"}`, "permission")
	})
	tw(t, fLoader, "returns override unchanged when base is empty", func(t *testing.T) {
		m := merge(`{}`, `{"yoloMode":true,"permission":{"bash":{"rm -rf *":"deny"}}}`)
		eq(t, *m.YoloMode, true, "yolo")
		eq(t, permJSON(m.Permission), `{"bash":{"rm -rf *":"deny"}}`, "permission")
	})
	tw(t, fLoader, "does not set undefined keys in the merged result", func(t *testing.T) {
		m := merge(`{"debugLog":true}`, `{"yoloMode":false}`)
		eq(t, *m.DebugLog, true, "debugLog")
		eq(t, *m.YoloMode, false, "yolo")
		eq(t, m.PermissionReviewLog == nil && m.Permission == nil, true, "absent keys")
	})
	tw(t, fLoader, "override toolInputPreviewMaxLength replaces base value", func(t *testing.T) {
		eq(t, *merge(`{"toolInputPreviewMaxLength":200}`, `{"toolInputPreviewMaxLength":1000}`).ToolInputPreviewMaxLength, 1000.0, "value")
	})
	tw(t, fLoader, "base toolInputPreviewMaxLength survives when override omits it", func(t *testing.T) {
		eq(t, *merge(`{"toolInputPreviewMaxLength":500}`, `{"debugLog":true}`).ToolInputPreviewMaxLength, 500.0, "value")
	})
	tw(t, fLoader, "toolInputPreviewMaxLength is absent when both base and override omit it", func(t *testing.T) {
		eq(t, merge(`{"debugLog":true}`, `{"yoloMode":false}`).ToolInputPreviewMaxLength == nil, true, "absent")
	})
	tw(t, fLoader, "override toolTextSummaryMaxLength replaces base value", func(t *testing.T) {
		eq(t, *merge(`{"toolTextSummaryMaxLength":80}`, `{"toolTextSummaryMaxLength":200}`).ToolTextSummaryMaxLength, 200.0, "value")
	})
	tw(t, fLoader, "base toolTextSummaryMaxLength survives when override omits it", func(t *testing.T) {
		eq(t, *merge(`{"toolTextSummaryMaxLength":120}`, `{"debugLog":false}`).ToolTextSummaryMaxLength, 120.0, "value")
	})
	tw(t, fLoader, "toolTextSummaryMaxLength is absent when both base and override omit it", func(t *testing.T) {
		eq(t, merge(`{}`, `{"permissionReviewLog":true}`).ToolTextSummaryMaxLength == nil, true, "absent")
	})
	tw(t, fLoader, "override piInfrastructureReadPaths replaces base array", func(t *testing.T) {
		eq(t, merge(`{"piInfrastructureReadPaths":["/base/path"]}`, `{"piInfrastructureReadPaths":["/override/path"]}`).PiInfrastructureReadPaths,
			[]string{"/override/path"}, "paths")
	})
	tw(t, fLoader, "base piInfrastructureReadPaths survives when override omits it", func(t *testing.T) {
		eq(t, merge(`{"piInfrastructureReadPaths":["/kept/path"]}`, `{"debugLog":true}`).PiInfrastructureReadPaths, []string{"/kept/path"}, "paths")
	})
	tw(t, fLoader, "piInfrastructureReadPaths is absent when both base and override omit it", func(t *testing.T) {
		eq(t, merge(`{"debugLog":true}`, `{"yoloMode":false}`).PiInfrastructureReadPaths == nil, true, "absent")
	})
	tw(t, fLoader, "override piInfrastructureReadPaths as empty array replaces non-empty base", func(t *testing.T) {
		eq(t, merge(`{"piInfrastructureReadPaths":["/base/path"]}`, `{"piInfrastructureReadPaths":[]}`).PiInfrastructureReadPaths, []string{}, "paths")
	})
	tw(t, fLoader, "override authorizerChain replaces base array", func(t *testing.T) {
		eq(t, merge(`{"authorizerChain":["base-judge"]}`, `{"authorizerChain":["override-judge"]}`).AuthorizerChain, []string{"override-judge"}, "chain")
	})
	tw(t, fLoader, "base authorizerChain survives when override omits it", func(t *testing.T) {
		eq(t, merge(`{"authorizerChain":["kept-judge"]}`, `{"debugLog":true}`).AuthorizerChain, []string{"kept-judge"}, "chain")
	})
	tw(t, fLoader, "override promptNotifications replaces the base list whole", func(t *testing.T) {
		eq(t, merge(`{"promptNotifications":["osc777"]}`, `{"promptNotifications":["bell"]}`).PromptNotifications, []string{"bell"}, "list")
	})
	tw(t, fLoader, "base promptNotifications survives when override omits it", func(t *testing.T) {
		eq(t, merge(`{"promptNotifications":["bell","osc777"]}`, `{"debugLog":true}`).PromptNotifications, []string{"bell", "osc777"}, "list")
	})
	tw(t, fLoader, "an empty override promptNotifications turns a base list off", func(t *testing.T) {
		eq(t, merge(`{"promptNotifications":["bell"]}`, `{"promptNotifications":[]}`).PromptNotifications, []string{}, "list")
	})
	tw(t, fLoader, "override permissionDialogKeys replaces the base map entirely", func(t *testing.T) {
		eq(t, merge(`{"permissionDialogKeys":{"approve":"1","deny":"4"}}`, `{"permissionDialogKeys":{"deny":"8"}}`).PermissionDialogKeys,
			DialogKeyOverrides{ActionDeny: "8"}, "keys")
	})
	tw(t, fLoader, "base permissionDialogKeys survives when override omits it", func(t *testing.T) {
		eq(t, merge(`{"permissionDialogKeys":{"approve":"1"}}`, `{"debugLog":true}`).PermissionDialogKeys, DialogKeyOverrides{ActionApprove: "1"}, "keys")
	})
	tw(t, fLoader, "permissionDialogKeys is absent when both base and override omit it", func(t *testing.T) {
		eq(t, merge(`{"debugLog":true}`, `{"yoloMode":false}`).PermissionDialogKeys == nil, true, "absent")
	})
	shell := func(m UnifiedConfig) string { return permJSON(m.ShellTools) }
	tw(t, fLoader, "base shellTools survives when override omits it", func(t *testing.T) {
		eq(t, shell(merge(`{"shellTools":{"exec_command":{"commandArgument":"cmd"}}}`, `{}`)), `{"exec_command":{"commandArgument":"cmd"}}`, "shellTools")
	})
	tw(t, fLoader, "override shellTools survives when base omits it", func(t *testing.T) {
		eq(t, shell(merge(`{}`, `{"shellTools":{"exec_command":{"commandArgument":"cmd"}}}`)), `{"exec_command":{"commandArgument":"cmd"}}`, "shellTools")
	})
	tw(t, fLoader, "shallow-merges shellTools by tool name: override adds without dropping base", func(t *testing.T) {
		eq(t, shell(merge(`{"shellTools":{"exec_command":{"commandArgument":"cmd"}}}`, `{"shellTools":{"run_shell":{"commandArgument":"script"}}}`)),
			`{"exec_command":{"commandArgument":"cmd"},"run_shell":{"commandArgument":"script"}}`, "shellTools")
	})
	tw(t, fLoader, "override shellTools replaces a colliding tool's alias wholesale", func(t *testing.T) {
		eq(t, shell(merge(`{"shellTools":{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"}}}`, `{"shellTools":{"exec_command":{"commandArgument":"command"}}}`)),
			`{"exec_command":{"commandArgument":"command"}}`, "shellTools")
	})
	tw(t, fLoader, "shellTools is absent when both base and override omit it", func(t *testing.T) {
		eq(t, merge(`{"debugLog":true}`, `{"yoloMode":false}`).ShellTools == nil, true, "absent")
	})
}

// mergeRig is the original's temp agent/project/extension directories and the writers beside them.
type mergeRig struct {
	t                           *testing.T
	agentDir, cwd, extensionDir string
}

func newMergeRig(t *testing.T) *mergeRig {
	tmp := t.TempDir()
	return &mergeRig{t, filepath.Join(tmp, "agent"), filepath.Join(tmp, "project"), filepath.Join(tmp, "ext")}
}
func (r *mergeRig) global(content string) {
	writeTestFile(r.t, filepath.Join(r.agentDir, "extensions", "pi-permission-system", "config.json"), content)
}
func (r *mergeRig) project(content string) {
	writeTestFile(r.t, filepath.Join(r.cwd, ".pi", "extensions", "pi-permission-system", "config.json"), content)
}
func (r *mergeRig) legacyGlobal(content string) {
	writeTestFile(r.t, filepath.Join(r.agentDir, "pi-permissions.jsonc"), content)
}
func (r *mergeRig) legacyProject(content string) {
	writeTestFile(r.t, filepath.Join(r.cwd, ".pi", "agent", "pi-permissions.jsonc"), content)
}
func (r *mergeRig) legacyExt(content string) {
	writeTestFile(r.t, filepath.Join(r.extensionDir, "config.json"), content)
}
func (r *mergeRig) load(opts ...MergeOptions) MergedConfigResult {
	o := MergeOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return loadAndMergeConfigs(r.agentDir, r.cwd, r.extensionDir, o)
}

func TestLoadAndMergeConfigsTwins(t *testing.T) {
	tw(t, fLoader, "merges global and project new-layout configs", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"debugLog":true,"permission":{"*":"ask","read":"allow"}}`)
		r.project(`{"permission":{"*":"allow","write":"deny"}}`)
		res := r.load()
		// the merged config leaves a permissive top-level '*' with no bash '*' policy, so the bash-fallback warning is expected
		eq(t, len(res.Issues), 1, "one issue")
		containsAll(t, res.Issues[0], "bash")
		eq(t, *res.Merged.DebugLog, true, "debugLog")
		eq(t, permJSON(res.Merged.Permission), `{"*":"allow","read":"allow","write":"deny"}`, "permission")
	})
	tw(t, fLoader, "detects legacy global policy and emits migration issue", func(t *testing.T) {
		r := newMergeRig(t)
		r.legacyGlobal(`{"defaultPolicy":{"tools":"allow"},"tools":{"read":"allow"}}`)
		res := r.load()
		eq(t, len(res.Issues), 1, "one issue")
		containsAll(t, res.Issues[0], "pi-permissions.jsonc", "extensions/pi-permission-system")
		eq(t, res.Merged.Permission == nil, true, "no rules extracted")
	})
	tw(t, fLoader, "detects legacy project policy and emits migration issue", func(t *testing.T) {
		r := newMergeRig(t)
		r.legacyProject(`{"bash":{"git status":"allow"}}`)
		res := r.load()
		eq(t, len(res.Issues), 1, "one issue")
		containsAll(t, res.Issues[0], ".pi/agent/pi-permissions.jsonc", ".pi/extensions/pi-permission-system")
		eq(t, res.Merged.Permission == nil, true, "no rules extracted")
	})
	tw(t, fLoader, "detects legacy extension runtime config and emits migration issue", func(t *testing.T) {
		r := newMergeRig(t)
		r.legacyExt(`{"debugLog":true,"yoloMode":true}`)
		res := r.load()
		eq(t, len(res.Issues), 1, "one issue")
		containsAll(t, res.Issues[0], r.extensionDir)
		eq(t, *res.Merged.DebugLog, true, "debugLog")
		eq(t, *res.Merged.YoloMode, true, "yolo")
	})
	tw(t, fLoader, "does not emit legacy extension config issue when path equals new global path", func(t *testing.T) {
		r := newMergeRig(t)
		newGlobalDir := filepath.Join(r.agentDir, "extensions", "pi-permission-system")
		writeTestFile(t, filepath.Join(newGlobalDir, "config.json"), `{"debugLog":true}`)
		res := loadAndMergeConfigs(r.agentDir, r.cwd, newGlobalDir, MergeOptions{})
		n := 0
		for _, i := range res.Issues {
			if strings.Contains(i, "legacy") {
				n++
			}
		}
		eq(t, n, 0, "legacy issues")
	})
	tw(t, fLoader, "emits no issues when no legacy files exist and no new files exist", func(t *testing.T) {
		eq(t, newMergeRig(t).load().Issues, []string{}, "issues")
	})
	tw(t, fLoader, "new-layout config takes precedence over legacy config at same scope", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"deny"}}`)
		r.legacyGlobal(`{"permission":{"*":"allow"}}`)
		res := r.load()
		// the legacy file is loaded first and the new layout second, so the new layout wins; the legacy file still warns
		eq(t, permJSON(res.Merged.Permission), `{"*":"deny"}`, "permission")
		found := false
		for _, i := range res.Issues {
			found = found || strings.Contains(i, "pi-permissions.jsonc")
		}
		eq(t, found, true, "migration warning")
	})
	tw(t, fLoader, "warns when the merged config leaves bash inheriting a permissive top-level '*'", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"allow","read":"allow"}}`)
		found := false
		for _, i := range r.load().Issues {
			found = found || strings.Contains(i, "bash")
		}
		eq(t, found, true, "bash warning")
	})
	tw(t, fLoader, "does not warn about bash fallback when bash is explicitly gated", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"allow","bash":{"*":"ask"}}}`)
		eq(t, r.load().Issues, []string{}, "issues")
	})
	off, on := MergeOptions{IncludeProjectScope: bptr(false)}, MergeOptions{IncludeProjectScope: bptr(true)}
	tw(t, fLoader, "omits the new project config when includeProjectScope is false", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask","bash":"deny"}}`)
		r.project(`{"permission":{"bash":"allow"}}`)
		res := r.load(off)
		// the untrusted project's `bash: allow` must not override global `deny`
		eq(t, permJSON(res.Merged.Permission), `{"*":"ask","bash":"deny"}`, "permission")
		eq(t, isEmptyConfig(res.Project), true, "project empty")
	})
	tw(t, fLoader, "omits the legacy project policy when includeProjectScope is false", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask"}}`)
		r.legacyProject(`{"permission":{"*":"allow"}}`)
		res := r.load(off)
		eq(t, permJSON(res.Merged.Permission), `{"*":"ask"}`, "permission")
		for _, i := range res.Issues {
			eq(t, strings.Contains(i, "pi-permissions.jsonc"), false, "no legacy notice")
		}
	})
	tw(t, fLoader, "includes the project config when includeProjectScope is true", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask","bash":"deny"}}`)
		r.project(`{"permission":{"bash":"allow"}}`)
		eq(t, permJSON(r.load(on).Merged.Permission), `{"*":"ask","bash":"allow"}`, "permission")
	})
	tw(t, fLoader, "includes the project config by default (option omitted)", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask","bash":"deny"}}`)
		r.project(`{"permission":{"bash":"allow"}}`)
		eq(t, permJSON(r.load().Merged.Permission), `{"*":"ask","bash":"allow"}`, "permission")
	})
	tw(t, fLoader, "notices a config that sets either cap", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask"},"toolInputPreviewMaxLength":400}`)
		res := r.load()
		eq(t, len(res.Issues), 1, "one issue")
		containsAll(t, res.Issues[0], "toolInputPreviewMaxLength")
	})
	tw(t, fLoader, "keeps accepting the config rather than rejecting it fail-closed", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask","bash":"deny"},"toolTextSummaryMaxLength":120}`)
		eq(t, permJSON(r.load().Merged.Permission), `{"*":"ask","bash":"deny"}`, "permission")
	})
	tw(t, fLoader, "stays silent when neither cap is set", func(t *testing.T) {
		r := newMergeRig(t)
		r.global(`{"permission":{"*":"ask","bash":"deny"}}`)
		eq(t, r.load().Issues, []string{}, "issues")
	})
}

func TestDetectDeprecatedPreviewCapsTwins(t *testing.T) {
	tw(t, fLoader, "names the input-preview cap when it alone is set", func(t *testing.T) {
		n := detectDeprecatedPreviewCaps(UnifiedConfig{ToolInputPreviewMaxLength: fptr(400)})
		containsAll(t, n, "toolInputPreviewMaxLength")
		eq(t, strings.Contains(n, "toolTextSummaryMaxLength"), false, "other cap")
	})
	tw(t, fLoader, "names the text-summary cap when it alone is set", func(t *testing.T) {
		n := detectDeprecatedPreviewCaps(UnifiedConfig{ToolTextSummaryMaxLength: fptr(120)})
		containsAll(t, n, "toolTextSummaryMaxLength")
		eq(t, strings.Contains(n, "toolInputPreviewMaxLength"), false, "other cap")
	})
	tw(t, fLoader, "names both caps when both are set", func(t *testing.T) {
		containsAll(t, detectDeprecatedPreviewCaps(UnifiedConfig{ToolInputPreviewMaxLength: fptr(400), ToolTextSummaryMaxLength: fptr(120)}),
			"toolInputPreviewMaxLength", "toolTextSummaryMaxLength")
	})
	tw(t, fLoader, "returns undefined when neither cap is set", func(t *testing.T) {
		eq(t, detectDeprecatedPreviewCaps(UnifiedConfig{}), "", "undefined")
	})
}

func TestDetectUnusableDialogKeysTwins(t *testing.T) {
	tw(t, fLoader, "returns undefined when no dialog keys are configured", func(t *testing.T) {
		eq(t, detectUnusableDialogKeys(UnifiedConfig{}), "", "undefined")
	})
	tw(t, fLoader, "returns undefined when every configured binding is usable", func(t *testing.T) {
		eq(t, detectUnusableDialogKeys(UnifiedConfig{PermissionDialogKeys: DialogKeyOverrides{ActionApprove: "1", ActionDeny: "4"}}), "", "undefined")
	})
	tw(t, fLoader, "names the decision, the refused character, and the default it keeps", func(t *testing.T) {
		eq(t, detectUnusableDialogKeys(UnifiedConfig{PermissionDialogKeys: DialogKeyOverrides{ActionDeny: "j"}}),
			`permissionDialogKeys.deny: "j" is reserved for moving the dialog's highlight; keeping the default "n".`, "notice")
	})
	tw(t, fLoader, "joins one sentence per refused binding", func(t *testing.T) {
		containsAll(t, detectUnusableDialogKeys(UnifiedConfig{PermissionDialogKeys: DialogKeyOverrides{ActionApprove: "A", ActionDeny: "k"}}),
			"permissionDialogKeys.approve", "permissionDialogKeys.deny")
	})
}
