package pi_permission_system

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The differential for PermissionSession: testdata/session-oracle.json holds every return value and every collaborator call the original's
// PermissionSession made over the operation scripts of testdata/session-cases.json (recorded by port/record-session-probe.sh), and this test
// replays the scripts through the port with equivalent recording fakes. Any difference fails.

type sessionCtxCase struct {
	Cwd     string          `json:"cwd"`
	HasUI   bool            `json:"hasUI"`
	Entries json.RawMessage `json:"entries"`
}

type sessionConfigCase struct {
	YoloMode                  bool            `json:"yoloMode"`
	PiInfrastructureReadPaths []string        `json:"piInfrastructureReadPaths"`
	ShellTools                json.RawMessage `json:"shellTools"`
}

type sessionOpCase struct {
	Op         string   `json:"op"`
	Ctx        *int     `json:"ctx"`
	Trusted    bool     `json:"trusted"`
	Msg        string   `json:"msg"`
	Prompt     *string  `json:"prompt"`
	Names      []string `json:"names"`
	Active     []string `json:"active"`
	Registered []string `json:"registered"`
	Denied     []string `json:"denied"`
	Surface    string   `json:"surface"`
	Pattern    string   `json:"pattern"`
}

type sessionCase struct {
	ID      string              `json:"id"`
	Flavor  string              `json:"flavor"`
	Ctxs    []sessionCtxCase    `json:"ctxs"`
	Configs []sessionConfigCase `json:"configs"`
	Ops     []sessionOpCase     `json:"ops"`
}

// sessionRecorder records every collaborator call as the original's fakes do (an empty cwd is the original's undefined).
type sessionRecorder struct {
	log       []any
	refreshes int
	configs   []sessionConfigCase
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *sessionRecorder) Start(ctx ForwarderContext) {
	r.log = append(r.log, []any{"forward.start", ctx.Cwd})
}
func (r *sessionRecorder) Stop() { r.log = append(r.log, []any{"forward.stop"}) }
func (r *sessionRecorder) Activate(ctx SessionContext) {
	r.log = append(r.log, []any{"auth.activate", ctx.Cwd})
}
func (r *sessionRecorder) Deactivate() { r.log = append(r.log, []any{"auth.deactivate"}) }
func (r *sessionRecorder) Notify(message, level string) {
	r.log = append(r.log, []any{"notify", message, level})
}
func (r *sessionRecorder) SetStatus(key, text string) {
	r.log = append(r.log, []any{"setStatus", key, nullIfEmpty(text)})
}
func (r *sessionRecorder) ConfigureForCwd(cwd string) {
	r.log = append(r.log, []any{"manager.configure", nullIfEmpty(cwd)})
}
func (r *sessionRecorder) Check(ResolvedAccessIntent, Ruleset) PermissionCheckResult {
	panic("not used")
}
func (r *sessionRecorder) GetToolPermission(string, *string) string { panic("not used") }
func (r *sessionRecorder) IsToolFullyDenied(string, *string) bool   { panic("not used") }
func (r *sessionRecorder) GetConfigIssues(*string) []string         { panic("not used") }
func (r *sessionRecorder) LogResolvedPaths(cwd string) {
	r.log = append(r.log, []any{"store.logPaths", nullIfEmpty(cwd)})
}
func (r *sessionRecorder) Refresh(cwd string, trusted bool) {
	r.refreshes++
	r.log = append(r.log, []any{"store.refresh", nullIfEmpty(cwd), trusted})
}
func (r *sessionRecorder) currentCase() sessionConfigCase {
	i := r.refreshes
	if i > len(r.configs)-1 {
		i = len(r.configs) - 1
	}
	return r.configs[i]
}
func (r *sessionRecorder) Current() PermissionSystemExtensionConfig {
	c := r.currentCase()
	cfg := PermissionSystemExtensionConfig{YoloMode: c.YoloMode, PiInfrastructureReadPaths: c.PiInfrastructureReadPaths}
	if c.ShellTools != nil && string(c.ShellTools) != "null" {
		v, err := parseJSON(c.ShellTools)
		if err != nil {
			panic(err)
		}
		cfg.ShellTools = v.(*jsObject)
	}
	return cfg
}

type sessionManagerFake struct {
	entries json.RawMessage
}

func (m sessionManagerFake) GetSessionID() (string, error)  { return "", errors.New("unused") }
func (m sessionManagerFake) GetSessionDir() (string, error) { return "", errors.New("unused") }
func (m sessionManagerFake) GetEntries() ([]SessionEntryView, error) {
	if string(m.entries) == `"throws"` {
		return nil, errors.New("boom")
	}
	var es []SessionEntryView
	if err := json.Unmarshal(m.entries, &es); err != nil {
		return nil, err
	}
	return es, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestPermissionSessionOracleDifferential(t *testing.T) {
	read := func(name string, into any) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	var cases []sessionCase
	var oracle []struct {
		ID      string `json:"id"`
		Results []struct {
			Throws bool  `json:"throws"`
			Value  any   `json:"value"`
			Log    []any `json:"log"`
		} `json:"results"`
	}
	read("session-cases.json", &cases)
	read("session-oracle.json", &oracle)
	if len(cases) != len(oracle) {
		t.Fatalf("%d scripts, %d records", len(cases), len(oracle))
	}
	checks, differences := 0, 0
	for i, c := range cases {
		want := oracle[i]
		if want.ID != c.ID || len(want.Results) != len(c.Ops) {
			t.Fatalf("script %d: corpus %q (%d ops), record %q (%d results)", i, c.ID, len(c.Ops), want.ID, len(want.Results))
		}
		rec := &sessionRecorder{configs: c.Configs}
		rules := &SessionRules{}
		flavor := PosixPathFlavor
		if c.Flavor == "win32" {
			flavor = Win32PathFlavor
		}
		session := NewPermissionSession(ExtensionPaths{PiInfrastructureDirs: []string{"/pi/a", "/pi/b"}, PiInfrastructureExcludedDirs: []string{"/pi/x"}},
			rec, rec, rules, rec, rec, flavor)
		ctxs := make([]SessionContext, len(c.Ctxs))
		for j, x := range c.Ctxs {
			ctxs[j] = SessionContext{ForwarderContext: ForwarderContext{HasUI: x.HasUI, Cwd: x.Cwd, SessionManager: sessionManagerFake{entries: x.Entries}}, Notifier: rec}
		}
		for j, o := range c.Ops {
			rec.log = nil
			var value any
			threw := false
			ctx := func() SessionContext { return ctxs[*o.Ctx] }
			switch o.Op {
			case "activate":
				session.Activate(ctx())
			case "deactivate":
				session.Deactivate()
			case "reset":
				session.ResetForNewSession(ctx(), o.Trusted)
			case "reload":
				session.Reload(o.Trusted)
			case "shutdown":
				session.Shutdown()
			case "notify":
				session.Notify(o.Msg)
			case "resolveAgent":
				prompt := ""
				if o.Prompt != nil {
					prompt = *o.Prompt
				}
				name, err := session.ResolveAgentName(ctx(), prompt)
				if err != nil {
					threw = true
				}
				value = strOrNull(name)
			case "lastKnown":
				value = strOrNull(session.LastKnownActiveAgentName())
			case "setSkills":
				var entries []SkillPromptEntry
				for _, n := range o.Names {
					entries = append(entries, SkillPromptEntry{Name: n, State: "allow"})
				}
				session.SetActiveSkillEntries(entries)
			case "getSkills":
				names := []string{}
				for _, e := range session.GetActiveSkillEntries() {
					names = append(names, e.Name)
				}
				value = names
			case "exposed":
				denied := map[string]bool{}
				for _, d := range o.Denied {
					denied[d] = true
				}
				registered := map[string]bool{}
				for _, r := range o.Registered {
					registered[r] = true
				}
				r := session.ResolveExposedTools(ToolSurfaceObservation{Active: o.Active, Registered: registered}, func(n string) bool { return !denied[n] })
				value = map[string]any{"exposed": nonNil(r.exposed), "withheld": nonNil(r.withheld), "restored": nonNil(r.restored), "changed": r.changed}
			case "refresh":
				if o.Ctx == nil {
					session.RefreshConfig(nil, o.Trusted)
				} else {
					x := ctx()
					session.RefreshConfig(&x, o.Trusted)
				}
			case "logPaths":
				session.LogResolvedConfigPaths()
			case "config":
				cfg := session.Config()
				m := map[string]any{"yoloMode": cfg.YoloMode}
				if cfg.PiInfrastructureReadPaths != nil {
					m["piInfrastructureReadPaths"] = cfg.PiInfrastructureReadPaths
				}
				if cfg.ShellTools != nil {
					m["shellTools"] = json.RawMessage(rec.currentCase().ShellTools)
				}
				value = m
			case "infra":
				s := session.GetInfrastructureReadScope()
				value = map[string]any{"dirs": nonNil(s.Dirs), "excludedDirs": nonNil(s.ExcludedDirs)}
			case "shell":
				if aliases := session.GetShellToolAliases(); aliases != nil {
					m := map[string]any{}
					for n, a := range aliases {
						e := map[string]any{"commandArgument": a.CommandArgument}
						if a.WorkdirArgument != "" {
							e["workdirArgument"] = a.WorkdirArgument
						}
						m[n] = e
					}
					value = m
				}
			case "previewLimits":
				l := session.GetToolPreviewLimits()
				value = map[string]any{"toolInputPreviewMaxLength": l.ToolInputPreviewMaxLength, "toolTextSummaryMaxLength": l.ToolTextSummaryMaxLength}
			case "normalizer":
				n := session.GetPathNormalizer()
				value = map[string]any{"resolveBase": n.ResolveBase("sub"), "isAbsolute": []any{n.IsAbsolute("/a"), n.IsAbsolute(`C:\a`), n.IsAbsolute("rel")}}
			case "approve":
				rules.Approve(o.Surface, o.Pattern)
			case "rules":
				rs := []any{}
				for _, r := range rules.GetRuleset() {
					rs = append(rs, ruleJSON(r))
				}
				value = rs
			default:
				t.Fatalf("unknown op %q", o.Op)
			}
			w := want.Results[j]
			checks++
			if w.Throws != threw {
				differences++
				t.Errorf("%s op %d %+v: the original threw=%v, the port threw=%v", c.ID, j, o, w.Throws, threw)
				continue
			}
			if threw {
				continue
			}
			gotLog := normalizeJSON(append([]any{}, rec.log...))
			wantLog := normalizeJSON(append([]any{}, w.Log...))
			if !reflect.DeepEqual(gotLog, wantLog) {
				differences++
				t.Errorf("%s op %d %+v: calls\n  port     %v\n  original %v", c.ID, j, o, gotLog, wantLog)
			}
			gotValue, wantValue := normalizeJSON(value), normalizeJSON(w.Value)
			if o.Op == "normalizer" {
				// a cwd that is not absolute under the flavor (a drive path on POSIX) resolves against the process's own working
				// directory, which differs by process: the recording ran in the original's package directory
				wd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range []struct {
					m   map[string]any
					dir string
				}{{gotValue.(map[string]any), wd}, {wantValue.(map[string]any), "/work/components/permissions/port/oracle/packages/pi-permission-system"}} {
					for _, dir := range []string{v.dir, strings.ReplaceAll(v.dir, "/", `\`)} {
						v.m["resolveBase"] = strings.Replace(v.m["resolveBase"].(string), dir, "<PROCESS-CWD>", 1)
					}
				}
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				differences++
				t.Errorf("%s op %d %+v: value\n  port     %v\n  original %v", c.ID, j, o, gotValue, wantValue)
			}
		}
	}
	t.Logf("%d scripts, %d operations, %d differences", len(cases), checks, differences)
}
