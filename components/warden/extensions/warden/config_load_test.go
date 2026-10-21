package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Twins of tests/config.test.ts. The raw values are the JSON the upstream test passes as object literals. Where the
// port's own keys or deliberate deviations (D1 off until /warden enable, D3 backends, D5 own file) change an
// expectation, the case says so.

func raw(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("bad test JSON %q: %v", text, err)
	}
	return v
}

func user(t *testing.T, base Config, text string) Config {
	return ApplyUserOverrides(base, raw(t, text))
}
func project(t *testing.T, base Config, text string) Config {
	return ApplyProjectOverrides(base, raw(t, text))
}

func eq(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", name, got, want)
	}
}

func cfgRuleIDs[T any](rules []T, id func(T) string) []string {
	out := []string{}
	for _, r := range rules {
		out = append(out, id(r))
	}
	return out
}

// twin: tests/config.test.ts:25
func TestConfigDefaultsMatchTheOriginalExceptThePortsOptIn(t *testing.T) {
	c := DefaultConfig()
	// Deviations: D1/D2 (off until /warden enable, no consent), visible steers and notices (D4).
	eq(t, "enabled", c.Enabled, false)
	eq(t, "consent", c.Consent, "")
	eq(t, "mode", c.Mode, "steer")
	eq(t, "tools", c.Action.Tools, []string{"bash", "powershell", "ctx_execute", "ctx_batch_execute", "ctx_execute_file", "write", "edit"})
	eq(t, "failOpen", c.Action.FailOpen, true)
	eq(t, "irreversible", c.Action.Irreversible, Threshold{Warn: 0.5, Confirm: 0.9})
	eq(t, "stuck.nudge", c.Stuck.Nudge, true)
	eq(t, "done.nudge", c.Done.Nudge, true)
	eq(t, "slop", c.Slop.Enabled && c.Slop.Prose.Enabled, true)
	eq(t, "steerVisible", c.SteerVisible, true)
	eq(t, "notices", c.Notices, true)
	eq(t, "timeoutMs", c.TimeoutMs, c.Action.TimeoutMs)
	eq(t, "warnings", len(DefaultConfig().Warnings), 0)
}

// twin: tests/config.test.ts:43
func TestShouldProceedSteerParser(t *testing.T) {
	base := DefaultConfig()
	eq(t, "default", base.Action.ShouldProceed, ShouldProceed{Threshold: 0.6, Steer: false})
	for name, apply := range map[string]func(*testing.T, Config, string) Config{"user": user, "project": project} {
		got := apply(t, base, `{"action":{"shouldProceed":{"steer":true,"threshold":0.3}}}`).Action.ShouldProceed
		eq(t, name+" set", got, ShouldProceed{Threshold: 0.3, Steer: true})
		for _, steer := range []string{`null`, `"true"`, `1`, `false`} {
			eq(t, name+" "+steer, apply(t, base, `{"action":{"shouldProceed":{"steer":`+steer+`}}}`).Action.ShouldProceed.Steer, false)
		}
		eq(t, name+" missing", apply(t, base, `{"action":{"shouldProceed":{}}}`).Action.ShouldProceed.Steer, false)
	}
}

// twin: tests/config.test.ts:54
func TestUserOverridesAcceptValidValuesAndIgnoreJunk(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{
	  "consent": "typesafe", "mode": "advise", "enabled": "yes", "headless": "allow",
	  "action": {"tools": ["bash", 7, ""], "irreversible": {"warn": 0.9, "confirm": 0.6}, "offTask": {"confirm": 2}, "timeoutMs": -1, "failOpen": false, "unknown": 1},
	  "stuck": {"minFailures": 20, "window": 5, "sameStrategy": 0.9, "nudge": false},
	  "done": {"claimsDone": 0.5},
	  "slop": {"placeholder": 0.6, "prose": {"audience": "plain", "trend": 9, "threshold": 2}},
	  "runaway": {"repeats": 1, "thinkingRepeats": 0.5, "minChars": 100, "recover": false},
	  "notify": {"enabled": false, "cooldownMs": 0, "command": ["my-notifier", "{title}", "{body}"]}
	}`)
	eq(t, "notify", cfg.Notify, NotifyConfig{Enabled: false, CooldownMs: 0, Command: []string{"my-notifier", "{title}", "{body}"}})
	eq(t, "junk notify", user(t, DefaultConfig(), `{"notify":{"cooldownMs":-5,"command":["","x"]}}`).Notify, DefaultConfig().Notify)
	eq(t, "judge cap", user(t, DefaultConfig(), `{"judge":{"cooldownMs":3600000}}`).Judge.CooldownMs, 600_000)
	eq(t, "judge", user(t, DefaultConfig(), `{"judge":{"cooldownMs":30000}}`).Judge.CooldownMs, 30_000)
	eq(t, "notify non-string arg", user(t, DefaultConfig(), `{"notify":{"command":["ok",7]}}`).Notify.Command, []string{})
	eq(t, "runaway", cfg.Runaway, RunawayConfig{Enabled: true, Repeats: 2, ThinkingRepeats: 10, MinChars: 100, Recover: false})
	eq(t, "consent", cfg.Consent, "typesafe")
	eq(t, "mode", cfg.Mode, "advise")
	eq(t, "enabled non-boolean falls back to the base (off in the port)", cfg.Enabled, false)
	eq(t, "tools", cfg.Action.Tools, []string{"bash"})
	eq(t, "irreversible clamp", cfg.Action.Irreversible, Threshold{Warn: 0.6, Confirm: 0.6})
	eq(t, "offTask", cfg.Action.OffTask.Steer, 0.85)
	eq(t, "timeout", cfg.Action.TimeoutMs, 5000)
	eq(t, "failOpen", cfg.Action.FailOpen, false)
	eq(t, "window", cfg.Stuck.Window, 5)
	eq(t, "minFailures clamped", cfg.Stuck.MinFailures, 5)
	eq(t, "sameStrategy", cfg.Stuck.SameStrategy, 0.9)
	eq(t, "nudge", cfg.Stuck.Nudge, false)
	eq(t, "claimsDone", cfg.Done.ClaimsDone, 0.5)
	eq(t, "uiProof", cfg.Done.UIProof, true)
	if !cfgContainsString(cfg.Done.UIFiles, "**/web/**/*.js") {
		t.Error("default uiFiles lost")
	}
	ui := user(t, DefaultConfig(), `{"done":{"uiProof":false,"uiFiles":["**/*.css",3],"visualTools":{"commands":["cypress"],"images":[".gif"]}}}`).Done
	eq(t, "uiProof off", ui.UIProof, false)
	eq(t, "uiFiles", ui.UIFiles, []string{"**/*.css"})
	want := DefaultConfig().Done.VisualTools
	want.Commands, want.Images = []string{"cypress"}, []string{"gif"}
	eq(t, "visualTools", ui.VisualTools, want)
	eq(t, "placeholder", cfg.Slop.Threshold, 0.6)
	eq(t, "audience", cfg.Slop.Prose.Audience, "plain")
	eq(t, "trend cap", cfg.Slop.Prose.Trend, 3)
	eq(t, "prose threshold", cfg.Slop.Prose.Threshold, 0.7)
	eq(t, "steerVisible", user(t, DefaultConfig(), `{"steerVisible":false}`).SteerVisible, false)
	eq(t, "notices", user(t, DefaultConfig(), `{"notices":false}`).Notices, false)
	eq(t, "bad mode", user(t, DefaultConfig(), `{"mode":"loud"}`).Mode, "steer")
}

// twin: tests/config.test.ts:98
func TestLegacyActionTimeoutAndMaxRequestsAreSharedSettings(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{"timeoutMs":8000,"maxRequests":50}}`)
	eq(t, "timeoutMs", cfg.TimeoutMs, 8000)
	eq(t, "action.timeoutMs", cfg.Action.TimeoutMs, 8000)
	eq(t, "maxRequests", cfg.MaxRequests, 50)
	eq(t, "top-level wins", user(t, DefaultConfig(), `{"timeoutMs":3000,"action":{"timeoutMs":8000}}`).TimeoutMs, 3000)
}

// twin: tests/config.test.ts:107
func TestProjectCannotGrantConsentChangeModeOrRaiseBudgets(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"consent":"typesafe","backend":"own-model","mode":"advise","maxRequests":9999,"timeoutMs":1,"action":{"tools":["bash"],"irreversible":{"confirm":0.95}},"stuck":{"enabled":false}}`)
	eq(t, "consent", cfg.Consent, "")
	eq(t, "backend", cfg.Backend, "")
	eq(t, "mode", cfg.Mode, "steer")
	eq(t, "maxRequests", cfg.MaxRequests, 500)
	eq(t, "timeout", cfg.Action.TimeoutMs, 5000)
	eq(t, "tools", cfg.Action.Tools, DefaultConfig().Action.Tools)
	eq(t, "confirm", cfg.Action.Irreversible.Confirm, 0.9)
	eq(t, "lowered", project(t, DefaultConfig(), `{"action":{"irreversible":{"confirm":0.8}}}`).Action.Irreversible.Confirm, 0.8)
	eq(t, "stuck", cfg.Stuck.Enabled, false)
	quiet := project(t, DefaultConfig(), `{"notify":{"enabled":false,"command":["evil"]}}`)
	eq(t, "notify off", quiet.Notify.Enabled, false)
	eq(t, "notify command", quiet.Notify.Command, []string{})
}

// twin: tests/config.test.ts:122
func TestProjectCannotSetCommandRules(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"action":{"commandRules":[{"id":"evil","pattern":".","severity":"deny"}],"commandDenyRules":[{"id":"evil-deny","pattern":".","severity":"deny"}],"exemptRules":["infra-destroy"]}}`)
	eq(t, "commandRules", len(cfg.Action.CommandRules), 0)
	eq(t, "commandDenyRules", len(cfg.Action.CommandDenyRules), 0)
	eq(t, "exemptRules", len(cfg.Action.ExemptRules), 0)
}

// twin: tests/config.test.ts:129
func TestProjectActionBlockKeepsUserCommandRules(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"commandRules":[{"id":"kubectl-delete","pattern":"\\bkubectl\\s+delete\\b","severity":"confirm"}],"commandDenyRules":[{"id":"never-reset","pattern":"\\btalosctl\\s+reset\\b"}],"exemptRules":["sudo"]}}`)
	cfg := project(t, base, `{"action":{"tools":["bash","write","read"]}}`)
	eq(t, "tools", cfg.Action.Tools, append(append([]string{}, DefaultConfig().Action.Tools...), "read"))
	eq(t, "commandRules", cfgRuleIDs(cfg.Action.CommandRules, func(r CommandRule) string { return r.ID }), []string{"kubectl-delete"})
	eq(t, "deny", cfgRuleIDs(cfg.Action.CommandDenyRules, func(r CommandRule) string { return r.ID }), []string{"never-reset"})
	eq(t, "exempt", cfg.Action.ExemptRules, []string{"sudo"})
}

// twin: tests/config.test.ts:145
func TestUserConfigAcceptsCommandRules(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{"commandRules":[{"id":"kubectl-delete","pattern":"\\bkubectl\\s+delete\\b","severity":"confirm"}],"commandDenyRules":[{"id":"never-reset","pattern":"\\btalosctl\\s+reset\\b"}],"exemptRules":["infra-destroy","sudo"]}}`)
	eq(t, "n", len(cfg.Action.CommandRules), 1)
	eq(t, "id", cfg.Action.CommandRules[0].ID, "kubectl-delete")
	eq(t, "severity", cfg.Action.CommandRules[0].Severity, "confirm")
	eq(t, "deny id", cfg.Action.CommandDenyRules[0].ID, "never-reset")
	eq(t, "deny severity", cfg.Action.CommandDenyRules[0].Severity, "deny")
	eq(t, "exempt", cfg.Action.ExemptRules, []string{"infra-destroy", "sudo"})
	messy := user(t, DefaultConfig(), `{"action":{"commandRules":[{"id":"x","pattern":".","severity":"warn"},{"id":"x","pattern":".","severity":"warn"},{"id":"","pattern":"."},{"id":"y","pattern":""}]}}`)
	eq(t, "messy", cfgRuleIDs(messy.Action.CommandRules, func(r CommandRule) string { return r.ID }), []string{"x"})
}

// twin: tests/config.test.ts:158
func TestProjectCannotSetPathRules(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"action":{"pathRules":[{"id":"evil","paths":["**/*"],"access":"none","tools":["*"],"action":"block"}]}}`)
	eq(t, "pathRules", len(cfg.Action.PathRules), 0)
}

// twin: tests/config.test.ts:163
func TestProjectActionBlockKeepsUserPathRules(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"pathRules":[{"id":"ssh-keys","paths":["~/.ssh/id_*"],"access":"write","tools":["*"],"action":"block"}]}}`)
	cfg := project(t, base, `{"action":{"tools":["bash","write"]}}`)
	eq(t, "pathRules", cfgRuleIDs(cfg.Action.PathRules, func(r PathRule) string { return r.ID }), []string{"ssh-keys"})
}

// twin: tests/config.test.ts:172
func TestUserConfigAcceptsPathRules(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{"pathRules":[
	  {"id":"env-files","paths":["**/.env","**/.env.*"],"access":"none","tools":["*"],"action":"confirm","onlyIfExists":true},
	  {"id":"env-files","paths":["**/.env"],"access":"none","tools":["*"],"action":"note"},
	  {"id":"","paths":["**/.env"]},
	  {"id":"no-paths","paths":[]},
	  {"id":"bad-tool","paths":["**/.env"],"tools":["made-up-tool"],"access":"none","action":"block"},
	  {"id":"defaults","paths":["~/.ssh/*"]}]}}`)
	eq(t, "count", len(cfg.Action.PathRules), 2)
	env := cfg.Action.PathRules[0]
	eq(t, "env id", env.ID, "env-files")
	eq(t, "env access", env.Access, "none")
	eq(t, "env action", env.Action, "confirm")
	if env.OnlyIfExists != nil {
		t.Error("onlyIfExists defaults to true and is omitted at the default")
	}
	d := cfg.Action.PathRules[1]
	eq(t, "defaults id", d.ID, "defaults")
	eq(t, "access default", d.Access, "none")
	eq(t, "action default", d.Action, "note")
	eq(t, "tools default", d.Tools, []string{"*"})
	if d.OnlyIfExists != nil {
		t.Error("onlyIfExists omitted when not overridden")
	}
	off := user(t, DefaultConfig(), `{"action":{"pathRules":[{"id":"a","paths":["x"],"onlyIfExists":false}]}}`).Action.PathRules[0]
	if off.OnlyIfExists == nil || *off.OnlyIfExists {
		t.Error("onlyIfExists false is kept")
	}
}

// twin: tests/config.test.ts:195
func TestLoadConfigMergesUserThenTrustedProjectAndSurvivesMalformedFiles(t *testing.T) {
	temp := t.TempDir()
	dirs := HostDirs{ConfigHome: filepath.Join(temp, "pig"), ConfigDirName: ".pig"}
	proj := filepath.Join(temp, "project")
	if err := os.MkdirAll(filepath.Join(proj, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	userPath, projectPath := dirs.UserConfigPath(), dirs.ProjectConfigPath(proj)
	cfg, err := LoadConfigWithProject(userPath, projectPath, true)
	if err != nil || cfg.Consent != "" {
		t.Fatalf("no files yet: %v %v", cfg.Consent, err)
	}
	if err := SetUserSetting(userPath, "consent", "typesafe"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(userPath); info.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", info.Mode().Perm())
		}
	}
	var saved map[string]any
	data, _ := os.ReadFile(userPath)
	_ = json.Unmarshal(data, &saved)
	eq(t, "file", saved, map[string]any{"consent": "typesafe"})

	if err := os.WriteFile(projectPath, []byte(`{"consent":"","action":{"offTask":{"warn":0.3,"confirm":0.4}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := LoadConfigWithProject(userPath, projectPath, true)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "consent survives the project", trusted.Consent, "typesafe")
	eq(t, "offTask", trusted.Action.OffTask, OffTaskThreshold{Warn: 0.3, Steer: 0.4})
	eq(t, "warn clamped", user(t, DefaultConfig(), `{"action":{"offTask":{"warn":0.5,"steer":0.4}}}`).Action.OffTask, OffTaskThreshold{Warn: 0.4, Steer: 0.4})
	untrusted, _ := LoadConfigWithProject(userPath, projectPath, false)
	eq(t, "untrusted ignored", untrusted.Action.OffTask.Warn, 0.6)

	if err := os.WriteFile(userPath, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err := LoadConfigWithProject(userPath, projectPath, true)
	if err == nil {
		t.Error("a malformed user file is reported")
	}
	eq(t, "malformed user file falls back to the defaults", bad.Consent, "")
	if err := SetUserSetting(userPath, "consent", "typesafe"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserSetting(userPath, "enabled", false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(userPath)
	saved = nil
	_ = json.Unmarshal(data, &saved)
	eq(t, "file after", saved, map[string]any{"consent": "typesafe", "enabled": false})
}

// twin: tests/config.test.ts:217
func TestProjectCannotUnmaskOutput(t *testing.T) {
	p := project(t, DefaultConfig(), `{"security":{"maskOutput":false,"threshold":0.5}}`)
	eq(t, "mask", p.Security.MaskOutput, true)
	eq(t, "threshold", p.Security.Threshold, 0.5)
	eq(t, "user can", user(t, DefaultConfig(), `{"security":{"maskOutput":false}}`).Security.MaskOutput, false)
	userOff := user(t, DefaultConfig(), `{"security":{"maskOutput":false}}`)
	eq(t, "nor turn it back on", project(t, userOff, `{"security":{"maskOutput":true}}`).Security.MaskOutput, false)
}

// twin: tests/config.test.ts:227
func TestHostileSectionsNeverLeaveAGuardUndefined(t *testing.T) {
	hostile := []string{`null`, `false`, `0`, `"yes"`, `[]`, `{"enabled":null}`, `{"prose":null}`, `{"prose":false}`, `{"prose":3}`, `{"prose":[]}`}
	sections := []string{"action", "stuck", "done", "runaway", "notify", "slop", "widget"}
	for _, section := range sections {
		for _, value := range hostile {
			for name, apply := range map[string]func(*testing.T, Config, string) Config{"user": user, "project": project} {
				cfg := apply(t, DefaultConfig(), `{"`+section+`":`+value+`}`)
				// Go has no undefined: the proof is that the guard keeps its base values rather than a zero value.
				base := DefaultConfig()
				switch section {
				case "action":
					eq(t, name+" "+section+value, cfg.Action.Enabled, base.Action.Enabled)
					eq(t, name+" "+section+value, cfg.Action.Tools, base.Action.Tools)
				case "stuck":
					eq(t, name+" "+section+value, cfg.Stuck.Window, base.Stuck.Window)
				case "done":
					eq(t, name+" "+section+value, cfg.Done.ClaimsDone, base.Done.ClaimsDone)
				case "runaway":
					eq(t, name+" "+section+value, cfg.Runaway.Repeats, base.Runaway.Repeats)
				case "notify":
					eq(t, name+" "+section+value, cfg.Notify.Command, base.Notify.Command)
				case "slop":
					eq(t, name+" "+section+value, cfg.Slop.Prose.MinChars, base.Slop.Prose.MinChars)
				case "widget":
					eq(t, name+" "+section+value, cfg.Widget.Placement, base.Widget.Placement)
				}
			}
		}
	}
	for _, value := range hostile {
		cfg := user(t, DefaultConfig(), `{"slop":`+value+`}`)
		eq(t, "slop prose "+value, cfg.Slop.Prose.MinChars, 200)
		eq(t, "slop enabled "+value, cfg.Slop.Enabled && cfg.Slop.Prose.Enabled, true)
	}
	// A hostile top level: not an object at all.
	for _, top := range []string{`null`, `[]`, `3`, `"x"`} {
		eq(t, "top "+top, ApplyUserOverrides(DefaultConfig(), raw(t, top)).Mode, "steer")
	}
}

// twin: tests/config.test.ts:256
func TestProjectCannotSetArmingRules(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"action":{"armingRules":[{"id":"evil","when":{"edited":["**/*"]},"arms":{"command":".*"},"action":"block"}]}}`)
	eq(t, "armingRules", len(cfg.Action.ArmingRules), 0)
}

// twin: tests/config.test.ts:261
func TestProjectActionBlockKeepsUserArmingRules(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"armingRules":[{"id":"gitops","when":{"edited":["**/kustomization.yaml"]},"arms":{"command":"\\bflux\\b","for":"10m"},"action":"confirm"}]}}`)
	cfg := project(t, base, `{"action":{"tools":["bash","write"]}}`)
	eq(t, "ids", cfgRuleIDs(cfg.Action.ArmingRules, func(r ArmingRule) string { return r.ID }), []string{"gitops"})
}

// twin: tests/config.test.ts:270
func TestUserConfigAcceptsArmingRules(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{"armingRules":[
	  {"id":"gitops","when":{"edited":["**/kustomization.yaml"]},"arms":{"command":"\\bflux\\b","for":"10m"},"action":"confirm"},
	  {"id":"gitops","when":{"edited":["**/x.yaml"]},"arms":{"command":"."},"action":"hold"},
	  {"id":"","when":{"edited":["**/x.yaml"]},"arms":{"command":"."},"action":"confirm"},
	  {"id":"no-edited","when":{"edited":[]},"arms":{"command":"."},"action":"confirm"},
	  {"id":"no-command","when":{"edited":["**/x.yaml"]},"arms":{"command":""},"action":"confirm"},
	  {"id":"no-action","when":{"edited":["**/x.yaml"]},"arms":{"command":"."}},
	  {"id":"bad-when-tools","when":{"edited":["**/x.yaml"],"tools":["bash"]},"arms":{"command":"."},"action":"confirm"}]}}`)
	eq(t, "ids", cfgRuleIDs(cfg.Action.ArmingRules, func(r ArmingRule) string { return r.ID }), []string{"gitops"})
	if cfg.Action.ArmingRules[0].When.Tools != nil {
		t.Error("when.tools stays unset")
	}
}

// twin: tests/config.test.ts:285
func TestArmingRulesParseDurations(t *testing.T) {
	rule := func(id, forValue string) string {
		return `{"id":"` + id + `","when":{"edited":["**/x"]},"arms":{"command":"."` + forValue + `},"action":"confirm"}`
	}
	cfg := user(t, DefaultConfig(), `{"action":{"armingRules":[`+strings.Join([]string{
		rule("string-minutes", `,"for":"10m"`), rule("string-seconds", `,"for":"30s"`), rule("string-hours", `,"for":"2h"`),
		rule("number-ms", `,"for":5000`), rule("default", ``)}, ",")+`]}}`)
	var got []any
	for _, r := range cfg.Action.ArmingRules {
		got = append(got, r.Arms.For)
	}
	eq(t, "durations", got, []any{600_000.0, 30_000.0, 7_200_000.0, 5000.0, 600_000.0})
}

// twin: tests/config.test.ts:302
func TestDefaultBackendIsNoneUntilEnabled(t *testing.T) {
	// D3/D1: upstream defaults typesafeBackend to "typesafe"; the port's default is offline patterns.
	eq(t, "backend", DefaultConfig().Backend, "")
	eq(t, "refusal", DefaultConfig().BackendRefusal, "")
}

// twin: tests/config.test.ts:307
func TestUserBackendAcceptsThePortsBackendsAndRefusesJunk(t *testing.T) {
	// D3: typesafe and own-model (and "" for offline) are the backends; openrouter, commandcode and endpoint objects are not.
	eq(t, "typesafe", user(t, DefaultConfig(), `{"backend":"typesafe"}`).Backend, "typesafe")
	eq(t, "own-model", user(t, DefaultConfig(), `{"backend":"own-model"}`).Backend, "own-model")
	eq(t, "offline", user(t, DefaultConfig(), `{"backend":""}`).BackendRefusal, "")
	for _, junk := range []string{`"azure"`, `"openrouter"`, `"commandcode"`, `42`, `{"label":"Acme","host":"https://gw.acme.example","keyEnv":"K"}`} {
		cfg := user(t, DefaultConfig(), `{"backend":`+junk+`}`)
		eq(t, junk+" backend", cfg.Backend, "")
		want := "Unknown judgment backend " + junk
		if strings.HasPrefix(junk, "{") {
			want = "Unknown judgment backend {" // keys are written sorted: encoding/json, not insertion order
		}
		if !strings.Contains(cfg.BackendRefusal, want) {
			t.Errorf("%s: refusal %q", junk, cfg.BackendRefusal)
		}
	}
	base := DefaultConfig()
	base.Backend = "own-model"
	eq(t, "null falls back", user(t, base, `{"backend":null}`).Backend, "own-model")
	eq(t, "missing falls back", user(t, base, `{}`).Backend, "own-model")
}

// twin: tests/config.test.ts:323
func TestProjectCannotSetBackend(t *testing.T) {
	base := DefaultConfig()
	base.Backend = "typesafe"
	for _, v := range []string{`"own-model"`, `{"label":"Evil gateway","host":"https://evil.example","keyEnv":"EVIL_KEY"}`} {
		cfg := project(t, base, `{"backend":`+v+`}`)
		eq(t, "backend", cfg.Backend, "typesafe")
		eq(t, "refusal", cfg.BackendRefusal, "")
	}
}

// twin: tests/config.test.ts:329
func TestFloorUserLevelPersistsThroughProject(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"floor":"level"}}`)
	eq(t, "floor", project(t, base, `{}`).Action.Floor, "level")
}

// twin: tests/config.test.ts:335
func TestFloorProjectCannotLowerUserLevel(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"floor":"level"}}`)
	eq(t, "floor", project(t, base, `{"action":{"floor":"evidence"}}`).Action.Floor, "level")
}

// twin: tests/config.test.ts:341
func TestFloorInvalidFallsBack(t *testing.T) {
	base := user(t, DefaultConfig(), `{"action":{"floor":"level"}}`)
	eq(t, "floor", project(t, base, `{"action":{"floor":"bogus"}}`).Action.Floor, "level")
}

// twin: tests/config.test.ts:347
func TestConscienceSkipTools(t *testing.T) {
	eq(t, "default", DefaultConfig().Conscience.SkipTools, []string{"read", "bash", "edit", "write", "grep", "find", "ls"})
	eq(t, "empty", user(t, DefaultConfig(), `{"conscience":{"skipTools":[]}}`).Conscience.SkipTools, []string{})
	eq(t, "filtered", user(t, DefaultConfig(), `{"conscience":{"skipTools":["bash",3]}}`).Conscience.SkipTools, []string{"bash"})
	eq(t, "junk", user(t, DefaultConfig(), `{"conscience":{"skipTools":"bash"}}`).Conscience.SkipTools, DefaultConfig().Conscience.SkipTools)
}

// twin: tests/config.test.ts:354
func TestConscienceAdvanceThresholdDefault(t *testing.T) {
	eq(t, "default", DefaultConfig().Conscience.AdvanceThreshold, 0.70)
}

// twin: tests/config.test.ts:359
func TestConscienceAdvanceThresholdClamps(t *testing.T) {
	eq(t, "negative", user(t, DefaultConfig(), `{"conscience":{"advanceThreshold":-0.5}}`).Conscience.AdvanceThreshold, 0.0)
	eq(t, "above 1", user(t, DefaultConfig(), `{"conscience":{"advanceThreshold":1.5}}`).Conscience.AdvanceThreshold, 1.0)
	eq(t, "valid", user(t, DefaultConfig(), `{"conscience":{"advanceThreshold":0.85}}`).Conscience.AdvanceThreshold, 0.85)
}

// twin: tests/config.test.ts:368
func TestPrefsSection(t *testing.T) {
	eq(t, "default", DefaultConfig().Prefs, PrefsConfig{Enabled: true, Inject: true})
	eq(t, "inject off", user(t, DefaultConfig(), `{"prefs":{"inject":false}}`).Prefs, PrefsConfig{Enabled: true, Inject: false})
	eq(t, "junk", user(t, DefaultConfig(), `{"prefs":{"enabled":"no","inject":1}}`).Prefs, PrefsConfig{Enabled: true, Inject: true})
	eq(t, "project", project(t, DefaultConfig(), `{"prefs":{"enabled":false}}`).Prefs, PrefsConfig{Enabled: false, Inject: true})
}

// twin: tests/config.test.ts:375
func TestDenyAndBlockAreTheSameValueWithNoWarning(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{
	  "commandRules":[{"id":"c-block","pattern":"a","severity":"block"},{"id":"c-deny","pattern":"b","severity":"deny"}],
	  "commandDenyRules":[{"id":"d-block","pattern":"c","severity":"block"}],
	  "pathRules":[{"id":"p-deny","paths":["~/.ssh/*"],"action":"deny"},{"id":"p-block","paths":["~/.aws/*"],"action":"block"}],
	  "armingRules":[{"id":"a-deny","when":{"edited":["**/x"]},"arms":{"command":"apply"},"action":"deny"},{"id":"a-block","when":{"edited":["**/y"]},"arms":{"command":"apply"},"action":"block"}]}}`)
	eq(t, "command", cfgRuleIDs(cfg.Action.CommandRules, func(r CommandRule) string { return r.Severity }), []string{"deny", "deny"})
	eq(t, "no dialog action", cfg.Action.CommandRules[0].Action, "")
	eq(t, "deny list", cfgRuleIDs(cfg.Action.CommandDenyRules, func(r CommandRule) string { return r.Severity }), []string{"deny"})
	eq(t, "path", cfgRuleIDs(cfg.Action.PathRules, func(r PathRule) string { return r.Action }), []string{"block", "block"})
	eq(t, "arming", cfgRuleIDs(cfg.Action.ArmingRules, func(r ArmingRule) string { return r.Action }), []string{"block", "block"})
	eq(t, "warnings", len(cfg.Warnings), 0)
}

// twin: tests/config.test.ts:393
func TestUnknownRuleValueAppliesAtConfirmAndWarnsOnce(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{
	  "commandRules":[{"id":"c-typo","pattern":"a","severity":"blok"},{"id":"c-missing","pattern":"b"}],
	  "commandDenyRules":[{"id":"d-typo","pattern":"c","severity":"stop"}],
	  "pathRules":[{"id":"p-typo","paths":["~/.ssh/*"],"action":"forbid"},{"id":"p-missing","paths":["~/.aws/*"]}],
	  "armingRules":[{"id":"a-typo","when":{"edited":["**/x"]},"arms":{"command":"apply","for":"5m"},"action":"halt"}]}}`)
	eq(t, "typo severity", cfg.Action.CommandRules[0].Severity, "confirm")
	eq(t, "typo action", cfg.Action.CommandRules[0].Action, "dialog")
	eq(t, "missing severity", cfg.Action.CommandRules[1].Severity, "warn")
	eq(t, "deny list", cfg.Action.CommandDenyRules[0].Severity, "deny")
	eq(t, "path typo", cfg.Action.PathRules[0].Action, "confirm")
	eq(t, "path missing", cfg.Action.PathRules[1].Action, "note")
	eq(t, "arming kept", len(cfg.Action.ArmingRules), 1)
	eq(t, "arming action", cfg.Action.ArmingRules[0].Action, "confirm")
	eq(t, "warnings", cfg.Warnings, []string{
		`command rule "c-typo": severity "blok" is not one of warn, confirm, deny, block; the rule applies at confirm`,
		`command rule "d-typo": severity "stop" is not one of warn, confirm, deny, block; the rule applies at deny`,
		`path rule "p-typo": action "forbid" is not one of note, warn, confirm, block, deny; the rule applies at confirm`,
		`arming rule "a-typo": action "halt" is not one of confirm, hold, block, deny; the rule applies at confirm`,
	})
}

// twin: tests/config.test.ts:417
func TestArmingDurationsAndSurpriseWarnings(t *testing.T) {
	rule := func(id, forValue string) string {
		return `{"id":"` + id + `","when":{"edited":["**/x"]},"arms":{"command":"apply","for":` + forValue + `},"action":"confirm"}`
	}
	cfg := user(t, DefaultConfig(), `{"action":{"armingRules":[`+strings.Join([]string{
		rule("ms-number", `30`), rule("bare-string", `"30"`), rule("hours", `"1.5h"`), rule("half-second", `"500ms"`), rule("seconds", `"45s"`)}, ",")+`]}}`)
	var got []any
	for _, r := range cfg.Action.ArmingRules {
		got = append(got, r.Arms.For)
	}
	eq(t, "durations", got, []any{30.0, 1_800_000.0, 5_400_000.0, 500.0, 45_000.0})
	eq(t, "warnings", cfg.Warnings, []string{
		`arming rule "ms-number": arms for 30 ms, under one second; a number in arms.for is milliseconds, "10m" is ten minutes`,
		`arming rule "bare-string": arms.for "30" has no unit and is read as minutes; write it with ms, s, m, or h`,
		`arming rule "half-second": arms for 500 ms, under one second; a number in arms.for is milliseconds, "10m" is ten minutes`,
	})
}

// twin: tests/config.test.ts:440
func TestProjectFileGuardKeysTakeStricterAndIgnoreWeaker(t *testing.T) {
	type tc struct {
		key, raw, user string
		read           func(Config) any
		tighter        any
		weaker         string
		kept           any
	}
	cases := []tc{
		{"enabled", `{"enabled":true}`, `{"enabled":false}`, func(c Config) any { return c.Enabled }, true, `{"enabled":false}`, true},
		{"action.enabled", `{"action":{"enabled":true}}`, `{"action":{"enabled":false}}`, func(c Config) any { return c.Action.Enabled }, true, `{"action":{"enabled":false}}`, true},
		{"security.enabled", `{"security":{"enabled":true}}`, `{"security":{"enabled":false}}`, func(c Config) any { return c.Security.Enabled }, true, `{"security":{"enabled":false}}`, true},
		{"action.irreversible.warn", `{"action":{"irreversible":{"warn":0.3}}}`, ``, func(c Config) any { return c.Action.Irreversible.Warn }, 0.3, `{"action":{"irreversible":{"warn":0.6}}}`, 0.5},
		{"action.irreversible.confirm", `{"action":{"irreversible":{"confirm":0.8}}}`, ``, func(c Config) any { return c.Action.Irreversible.Confirm }, 0.8, `{"action":{"irreversible":{"confirm":0.95}}}`, 0.9},
		{"security.threshold", `{"security":{"threshold":0.5}}`, ``, func(c Config) any { return c.Security.Threshold }, 0.5, `{"security":{"threshold":0.9}}`, 0.7},
		{"action.failOpen", `{"action":{"failOpen":false}}`, `{"action":{"failOpen":false}}`, func(c Config) any { return c.Action.FailOpen }, false, `{"action":{"failOpen":true}}`, false},
	}
	for _, c := range cases {
		userBase := DefaultConfig()
		if c.user != "" {
			userBase = user(t, userBase, c.user)
		}
		// D1: warden is off until /warden enable, so the `enabled` row measures a project against a user who turned it on.
		if c.key == "enabled" {
			userBase = DefaultConfig()
			userBase.Enabled = true
		}
		stricter := project(t, userBase, c.raw)
		eq(t, c.key+": the stricter project value is taken", c.read(stricter), c.tighter)
		eq(t, c.key+": no warning for a stricter value", len(stricter.Warnings), 0)
		weakerBase := DefaultConfig()
		if c.key == "action.failOpen" || c.key == "enabled" {
			weakerBase = userBase
		}
		ignored := project(t, weakerBase, c.weaker)
		eq(t, c.key+": the weaker project value is ignored", c.read(ignored), c.kept)
		if len(ignored.Warnings) != 1 || !strings.HasPrefix(ignored.Warnings[0], "project file: "+c.key+" ") {
			t.Errorf("%s: warnings %v", c.key, ignored.Warnings)
		}
	}
	eq(t, "failOpen on a user who already failed open", len(project(t, DefaultConfig(), `{"action":{"failOpen":true}}`).Warnings), 0)
}

// D1: a project file cannot turn warden on (upstream lets it, because upstream's default is on).
func TestProjectCannotEnableWarden(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"enabled":true}`)
	eq(t, "enabled", cfg.Enabled, false)
	eq(t, "warning", cfg.Warnings, []string{"project file: enabled true is ignored; warden stays off until the user runs /warden enable"})
	eq(t, "silent when false", len(project(t, DefaultConfig(), `{"enabled":false}`).Warnings), 0)
}

// twin: tests/config.test.ts:466
func TestProjectActionToolsAddButNeverRemove(t *testing.T) {
	tools := DefaultConfig().Action.Tools
	added := project(t, DefaultConfig(), `{"action":{"tools":["bash","powershell","ctx_execute","ctx_batch_execute","ctx_execute_file","write","edit","read"]}}`)
	eq(t, "added", added.Action.Tools, append(append([]string{}, tools...), "read"))
	eq(t, "no warning", len(added.Warnings), 0)
	shrunk := project(t, DefaultConfig(), `{"action":{"tools":[]}}`)
	eq(t, "kept", shrunk.Action.Tools, tools)
	eq(t, "warning", shrunk.Warnings, []string{"project file: action.tools leaves out " + strings.Join(tools, ", ") + "; a project file may add tools but not remove them, so they stay guarded"})
	eq(t, "user sets", user(t, DefaultConfig(), `{"action":{"tools":["bash"]}}`).Action.Tools, []string{"bash"})
}

// twin: tests/config.test.ts:476
func TestProjectOtherGuardsTunableBothWaysUserNeverLimited(t *testing.T) {
	loose := project(t, DefaultConfig(), `{"stuck":{"enabled":false},"rules":{"threshold":0.95},"action":{"offTask":{"warn":0.9,"steer":0.95}}}`)
	eq(t, "stuck", loose.Stuck.Enabled, false)
	eq(t, "rules", loose.Rules.Threshold, 0.95)
	eq(t, "offTask", loose.Action.OffTask, OffTaskThreshold{Warn: 0.9, Steer: 0.95})
	eq(t, "warnings", len(loose.Warnings), 0)
	u := user(t, DefaultConfig(), `{"enabled":false,"action":{"enabled":false,"failOpen":true,"irreversible":{"warn":0.8,"confirm":0.99}},"security":{"enabled":false,"threshold":0.9}}`)
	eq(t, "enabled", u.Enabled, false)
	eq(t, "action", u.Action.Enabled, false)
	eq(t, "irreversible", u.Action.Irreversible, Threshold{Warn: 0.8, Confirm: 0.99})
	eq(t, "security", []any{u.Security.Enabled, u.Security.Threshold}, []any{false, 0.9})
	eq(t, "warnings", len(u.Warnings), 0)
}

// twin: tests/config.test.ts:490
func TestProjectWarningsFollowTheUsers(t *testing.T) {
	u := user(t, DefaultConfig(), `{"action":{"commandRules":[{"id":"x","pattern":"x","severity":"nope"}]}}`)
	p := project(t, u, `{"security":{"enabled":false}}`)
	eq(t, "count", len(p.Warnings), 2)
	if !strings.HasPrefix(p.Warnings[0], `command rule "x"`) || !strings.HasPrefix(p.Warnings[1], "project file: security.enabled false is ignored") {
		t.Errorf("warnings %v", p.Warnings)
	}
}

// twin: tests/config.test.ts:498
func TestLearningRetentionDaysZeroKeepsEveryRecord(t *testing.T) {
	eq(t, "zero", user(t, DefaultConfig(), `{"learning":{"retentionDays":0}}`).Learning.RetentionDays, 0)
	eq(t, "thirty", user(t, DefaultConfig(), `{"learning":{"retentionDays":30}}`).Learning.RetentionDays, 30)
	for _, junk := range []string{`-1`, `1.5`, `"0"`, `null`} {
		eq(t, junk, user(t, DefaultConfig(), `{"learning":{"retentionDays":`+junk+`}}`).Learning.RetentionDays, 365)
	}
}

// twin: tests/config.test.ts:506
func TestShouldProceedHoldIsTheDeprecatedThreshold(t *testing.T) {
	eq(t, "user", user(t, DefaultConfig(), `{"action":{"shouldProceed":{"hold":0.3}}}`).Action.ShouldProceed.Threshold, 0.3)
	eq(t, "project", project(t, DefaultConfig(), `{"action":{"shouldProceed":{"hold":0.4}}}`).Action.ShouldProceed.Threshold, 0.4)
	eq(t, "new name wins", user(t, DefaultConfig(), `{"action":{"shouldProceed":{"hold":0.3,"threshold":0.2}}}`).Action.ShouldProceed.Threshold, 0.2)
	// The stale-module half (completeConfig on a config that still delivers `hold`) cannot exist in Go: ShouldProceed has no `hold` field.
}

// twin: tests/config.test.ts:515
func TestConfigFileWithRemovedKeysLoadsCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pigpen-warden", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"learning":{"adaptiveThresholds":false,"patternAnalysis":false,"minHoldsForAdaptive":3,"adaptationRate":0.5,"retentionDays":30},"conscience":{"loadThreshold":0.2,"recommendThreshold":0.9}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "learning", cfg.Learning, LearningConfig{PatternAnalysis: false, RetentionDays: 30})
	eq(t, "recommendThreshold", cfg.Conscience.RecommendThreshold, 0.9)
	eq(t, "warnings", len(cfg.Warnings), 0)
}

// twin: tests/config.test.ts:533
func TestArmingRuleWithNoActionIsIgnoredWithOneWarning(t *testing.T) {
	cfg := user(t, DefaultConfig(), `{"action":{"armingRules":[
	  {"id":"no-action","when":{"edited":["**/x"]},"arms":{"command":"apply","for":"5m"}},
	  {"id":"kept","when":{"edited":["**/y"]},"arms":{"command":"apply","for":"5m"},"action":"hold"}]}}`)
	eq(t, "ids", cfgRuleIDs(cfg.Action.ArmingRules, func(r ArmingRule) string { return r.ID }), []string{"kept"})
	eq(t, "warnings", cfg.Warnings, []string{`arming rule "no-action": has no action and is ignored; set action to confirm, hold, or block`})
}

// Loader behavior upstream's tests do not pin: the dotted-path helpers behind /warden config.
func TestNestedValueHelpers(t *testing.T) {
	obj := map[string]any{"a": map[string]any{"b": 1.0}}
	got := SetNestedValue(obj, "a.c.d", "x")
	eq(t, "set", got, map[string]any{"a": map[string]any{"b": 1.0, "c": map[string]any{"d": "x"}}})
	eq(t, "input untouched", obj, map[string]any{"a": map[string]any{"b": 1.0}})
	eq(t, "get", GetNestedValue(got, "a.c.d"), "x")
	eq(t, "get missing", GetNestedValue(got, "a.q.z"), nil)
	eq(t, "get through a scalar", GetNestedValue(got, "a.b.z"), nil)
	// JavaScript spreads a string into index keys, and a list into index keys too.
	eq(t, "spread string", SetNestedValue(map[string]any{"s": "ab"}, "s.x", 1.0), map[string]any{"s": map[string]any{"0": "a", "1": "b", "x": 1.0}})
	for in, want := range map[string]any{"true": true, "false": false, "null": nil, "12": 12.0, "-1.5": -1.5, "1e3": "1e3", "abc": "abc", "": ""} {
		eq(t, "parse "+in, ParseConfigValue(in), want)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]float64{`"10m"`: 600_000, `"30s"`: 30_000, `"2h"`: 7_200_000, `"500ms"`: 500, `"30"`: 1_800_000, `" 5 m "`: 300_000, `"x"`: 7, `0`: 7, `-1`: 7, `5000`: 5000, `"0s"`: 7, `null`: 7} {
		eq(t, in, ParseDuration(raw(t, in), 7), want)
	}
}

func reflectEqualConfig(a, b Config) bool { return reflect.DeepEqual(a, b) }

// Each key of a project file's stuck section is read on its own (a scenario cannot show this one key at a time: see PORT.md).
func TestProjectTunesStuckKeysIndependently(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"stuck":{"repeatSteer":false}}`)
	eq(t, "repeatSteer", cfg.Stuck.RepeatSteer, false)
	eq(t, "nudge untouched", cfg.Stuck.Nudge, true)
	cfg = project(t, DefaultConfig(), `{"stuck":{"nudge":false}}`)
	eq(t, "nudge", cfg.Stuck.Nudge, false)
	eq(t, "repeatSteer untouched", cfg.Stuck.RepeatSteer, true)
}

func TestConscienceTimeoutIsClampedToTheFloor(t *testing.T) {
	eq(t, "floor", user(t, DefaultConfig(), `{"conscience":{"timeoutMs":50}}`).Conscience.TimeoutMs, 100)
	eq(t, "ceiling", user(t, DefaultConfig(), `{"conscience":{"timeoutMs":99999}}`).Conscience.TimeoutMs, 10000)
}

func TestProjectCannotTurnOnCompactionOrTheContextFilter(t *testing.T) {
	cfg := project(t, DefaultConfig(), `{"compaction":{"enabled":true,"keepThreshold":0.4},"context":{"filter":{"enabled":true,"chunkChars":900}}}`)
	eq(t, "compaction stays off", cfg.Compaction.Enabled, false)
	eq(t, "the rest of the section applies", cfg.Compaction.KeepThreshold, 0.4)
	eq(t, "filter stays off", cfg.Context.Filter.Enabled, false)
	eq(t, "the rest of the filter applies", cfg.Context.Filter.ChunkChars, 900)
	u := user(t, DefaultConfig(), `{"compaction":{"enabled":true},"context":{"filter":{"enabled":true}}}`)
	eq(t, "the user can: compaction", u.Compaction.Enabled, true)
	eq(t, "the user can: filter", u.Context.Filter.Enabled, true)
}

func TestARefusedBackendTurnsJudgmentsOffEvenWhenTheBaseHadOne(t *testing.T) {
	base := DefaultConfig()
	base.Backend = "typesafe"
	cfg := user(t, base, `{"backend":"azure"}`)
	eq(t, "backend", cfg.Backend, "")
}

func TestPanelWidthNeedsAtLeastTwentyColumnsOrAPercentage(t *testing.T) {
	eq(t, "too narrow", user(t, DefaultConfig(), `{"widget":{"panelWidth":10}}`).Widget.PanelWidth, "40%")
	eq(t, "columns", user(t, DefaultConfig(), `{"widget":{"panelWidth":25}}`).Widget.PanelWidth, 25.0)
	eq(t, "percentage", user(t, DefaultConfig(), `{"widget":{"panelWidth":"55%"}}`).Widget.PanelWidth, "55%")
	eq(t, "bad percentage", user(t, DefaultConfig(), `{"widget":{"panelWidth":"0%"}}`).Widget.PanelWidth, "40%")
}

func TestAFailedSaveLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pigpen-warden")
	target := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(target, 0o700); err != nil { // a directory where the file should be: the rename fails
		t.Fatal(err)
	}
	if err := WriteUserConfig(target, map[string]any{"a": 1}); err == nil {
		t.Fatal("expected a failure")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	eq(t, "temporary files", len(left), 0)
}
