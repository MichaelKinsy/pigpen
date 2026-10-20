package pi_permission_system

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The config system against the original's own functions: testdata/config-oracle.json is what port/probe/config-probe.test.ts
// recorded by running port/probe/config-system-cases.json through loadUnifiedConfig, loadAndMergeConfigs, FilePolicyLoader,
// ConfigStore.save, the `/permission-system` handlers, resolveDialogKeys and the frontmatter parser (port/record-config-probe.sh).

type cfgOracle struct {
	Cases struct {
		Files []struct {
			ID     string `json:"id"`
			Raw    string `json:"raw"`
			RawB64 string `json:"rawB64"`
		} `json:"files"`
		Loaders []struct {
			ID             string            `json:"id"`
			Global         *string           `json:"global"`
			Project        *string           `json:"project"`
			LegacyGlobal   *string           `json:"legacyGlobal"`
			LegacyProject  *string           `json:"legacyProject"`
			LegacyExt      *string           `json:"legacyExt"`
			MCPGlobal      *string           `json:"mcpGlobal"`
			MCPProject     *string           `json:"mcpProject"`
			IncludeProject *bool             `json:"includeProject"`
			GlobalIsDir    bool              `json:"globalIsDir"`
			GlobalMode     *int              `json:"globalMode"`
			ProjectIsDir   bool              `json:"projectIsDir"`
			Agents         map[string]string `json:"agents"`
			ProjectAgents  map[string]string `json:"projectAgents"`
			AgentNames     []string          `json:"agentNames"`
		} `json:"loaders"`
		Yaml []struct {
			ID       string `json:"id"`
			Markdown string `json:"markdown"`
		} `json:"yaml"`
		DialogKeys []struct {
			ID        string            `json:"id"`
			Overrides map[string]string `json:"overrides"`
		} `json:"dialogKeys"`
		Saves []struct {
			ID       string          `json:"id"`
			Existing *string         `json:"existing"`
			Next     json.RawMessage `json:"next"`
		} `json:"saves"`
		Commands []struct {
			ID          string          `json:"id"`
			Config      json.RawMessage `json:"config"`
			Rules       []Rule          `json:"rules"`
			Args        []string        `json:"args"`
			HasUI       *bool           `json:"hasUI"`
			Completions []string        `json:"completions"`
		} `json:"commands"`
	} `json:"cases"`
	PiTui struct {
		SymbolKeys []string        `json:"symbolKeys"`
		Bindable   []string        `json:"bindable"`
		Matches    map[string]bool `json:"matches"`
	} `json:"piTui"`
	Schema json.RawMessage `json:"schema"`
	Files  []struct {
		ID     string   `json:"id"`
		Config string   `json:"config"`
		Issues []string `json:"issues"`
	} `json:"files"`
	Loaders []struct {
		ID      string   `json:"id"`
		Global  string   `json:"global"`
		Project string   `json:"project"`
		Merged  string   `json:"merged"`
		Runtime string   `json:"runtime"`
		Issues  []string `json:"issues"`
		Scopes  struct {
			Global  string `json:"global"`
			Project string `json:"project"`
			Agents  map[string]struct {
				Agent        string `json:"agent"`
				ProjectAgent string `json:"projectAgent"`
			} `json:"agents"`
			MCP    []string       `json:"mcp"`
			Issues []string       `json:"issues"`
			Paths  map[string]any `json:"paths"`
		} `json:"scopes"`
	} `json:"loaders"`
	Yaml []struct {
		ID          string `json:"id"`
		Frontmatter string `json:"frontmatter"`
		Map         string `json:"map"`
		Whole       string `json:"whole"`
	} `json:"yaml"`
	DialogKeys []struct {
		ID     string   `json:"id"`
		Keys   string   `json:"keys"`
		Issues []string `json:"issues"`
	} `json:"dialogKeys"`
	Saves []struct {
		ID       string     `json:"id"`
		File     *string    `json:"file"`
		Notes    [][]string `json:"notes"`
		Statuses [][]any    `json:"statuses"`
		Current  string     `json:"current"`
	} `json:"saves"`
	Commands []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Results     []struct {
			Args        string     `json:"args"`
			Notes       [][]string `json:"notes"`
			CustomCalls int        `json:"customCalls"`
			Current     string     `json:"current"`
		} `json:"results"`
		Completions [][]json.RawMessage `json:"completions"`
	} `json:"commands"`
}

var loneSurrogateEscape = regexp.MustCompile(`\\u[dD][89a-fA-F][0-9a-fA-F]{2}`)

var (
	cfgOracleOnce sync.Once
	cfgOracleData cfgOracle
)

func loadConfigOracle(t *testing.T) *cfgOracle {
	t.Helper()
	cfgOracleOnce.Do(func() {
		b, err := os.ReadFile("testdata/config-oracle.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &cfgOracleData); err != nil {
			t.Fatal(err)
		}
	})
	return &cfgOracleData
}

func scrubList(a []string, tmp string) []string {
	out := []string{}
	for _, s := range a {
		out = append(out, strings.ReplaceAll(s, tmp, "<TMP>"))
	}
	return out
}

func orEmpty(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

// jsonCfg is JSON.stringify of a validated config, in the schema's output order.
func jsonCfg(c UnifiedConfig) string { return marshalJSON(c.toObject(), "") }

// mergedJSON is JSON.stringify of mergeUnifiedConfigs' result: its keys come in the order the merge assigns them.
func mergedJSON(c UnifiedConfig) string {
	o := newObject()
	full := c.toObject()
	for _, k := range []string{"debugLog", "permissionReviewLog", "yoloMode", "doublePressToConfirm", "forwardingTimeoutMs", "promptMaxRows",
		"promptFieldMaxWidth", "reviewLogFieldMaxWidth", "toolInputPreviewMaxLength", "toolTextSummaryMaxLength", "piInfrastructureReadPaths",
		"authorizerChain", "permissionDialogKeys", "promptNotifications", "shellTools", "permission"} {
		if v, ok := full.vals[k]; ok {
			o.set(k, v)
		}
	}
	return marshalJSON(o, "")
}

// runtimeJSON is JSON.stringify of a normalized runtime config.
func runtimeJSON(c PermissionSystemExtensionConfig) string {
	o := newObject()
	o.set("debugLog", c.DebugLog)
	o.set("permissionReviewLog", c.PermissionReviewLog)
	o.set("yoloMode", c.YoloMode)
	o.set("doublePressToConfirm", c.DoublePressToConfirm)
	list := func(k string, a []string) {
		if a != nil {
			arr := make([]any, len(a))
			for i, s := range a {
				arr[i] = s
			}
			o.set(k, arr)
		}
	}
	list("piInfrastructureReadPaths", c.PiInfrastructureReadPaths)
	for k, p := range map[string]*float64{} {
		_, _ = k, p
	}
	for _, kv := range []struct {
		k string
		p *float64
	}{{"forwardingTimeoutMs", c.ForwardingTimeoutMs}, {"promptMaxRows", c.PromptMaxRows}, {"promptFieldMaxWidth", c.PromptFieldMaxWidth}, {"reviewLogFieldMaxWidth", c.ReviewLogFieldMaxWidth}} {
		if kv.p != nil {
			o.set(kv.k, *kv.p)
		}
	}
	if c.PermissionDialogKeys != nil {
		keys := newObject()
		for _, a := range actionOrder {
			if v, ok := c.PermissionDialogKeys[a]; ok {
				keys.set(string(a), v)
			}
		}
		o.set("permissionDialogKeys", keys)
	}
	list("promptNotifications", c.PromptNotifications)
	if c.ShellTools != nil {
		o.set("shellTools", c.ShellTools)
	}
	list("authorizerChain", c.AuthorizerChain)
	return marshalJSON(o, "")
}

func scopeJSON(s *ScopeConfig) string {
	o := newObject()
	if s.Permission != nil {
		o.set("permission", s.Permission)
	}
	if s.Invalid {
		o.set("invalid", true)
	}
	return marshalJSON(o, "")
}

func TestConfigFilesAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.Files) != len(r.Cases.Files) || len(r.Files) < 500 {
		t.Fatalf("%d recorded files for %d cases", len(r.Files), len(r.Cases.Files))
	}
	for i, want := range r.Files {
		c := r.Cases.Files[i]
		raw := []byte(c.Raw)
		if c.RawB64 != "" {
			raw, _ = base64.StdEncoding.DecodeString(c.RawB64)
		}
		dir := t.TempDir()
		p := filepath.Join(dir, "config.json")
		writeTestFile(t, p, string(raw))
		got := loadUnifiedConfig(p)
		issues := []string{}
		for _, s := range got.Issues {
			issues = append(issues, strings.ReplaceAll(s, p, "<PATH>"))
		}
		if !reflect.DeepEqual(issues, orEmpty(want.Issues)) {
			t.Errorf("%s %q: issues\n got %q\nwant %q", c.ID, string(raw), issues, want.Issues)
		}
		// Explained difference: a lone UTF-16 surrogate (written as a \ud800-style escape in a string) cannot live in a Go string;
		// the port holds U+FFFD where the original holds the surrogate, which JSON.stringify writes back as an escape.
		if loneSurrogateEscape.MatchString(want.Config) {
			continue
		}
		if g := jsonCfg(got.Config); g != want.Config {
			t.Errorf("%s %q: config\n got %s\nwant %s", c.ID, string(raw), g, want.Config)
		}
	}
}

func TestConfigLoadersAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.Loaders) != len(r.Cases.Loaders) || len(r.Loaders) < 80 {
		t.Fatalf("%d recorded loaders for %d cases", len(r.Loaders), len(r.Cases.Loaders))
	}
	for i, want := range r.Loaders {
		c := r.Cases.Loaders[i]
		tmp := t.TempDir()
		agentDir, cwd, ext := filepath.Join(tmp, "agent"), filepath.Join(tmp, "project"), filepath.Join(tmp, "ext")
		put := func(root, rel string, content *string) {
			if content != nil {
				writeTestFile(t, filepath.Join(root, rel), *content)
			}
		}
		put(agentDir, "extensions/pi-permission-system/config.json", c.Global)
		if c.GlobalIsDir {
			if err := os.MkdirAll(filepath.Join(agentDir, "extensions/pi-permission-system/config.json"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if c.GlobalMode != nil && runtime.GOOS != "windows" {
			if err := os.Chmod(filepath.Join(agentDir, "extensions/pi-permission-system/config.json"), os.FileMode(*c.GlobalMode)); err != nil {
				t.Fatal(err)
			}
		}
		put(cwd, ".pi/extensions/pi-permission-system/config.json", c.Project)
		if c.ProjectIsDir {
			if err := os.MkdirAll(filepath.Join(cwd, ".pi/extensions/pi-permission-system/config.json"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		put(agentDir, "pi-permissions.jsonc", c.LegacyGlobal)
		put(cwd, ".pi/agent/pi-permissions.jsonc", c.LegacyProject)
		put(ext, "config.json", c.LegacyExt)
		for n, md := range c.Agents {
			md := md
			put(agentDir, "agents/"+n+".md", &md)
		}
		for n, md := range c.ProjectAgents {
			md := md
			put(cwd, ".pi/agents/"+n+".md", &md)
		}
		put(agentDir, "mcp.json", c.MCPGlobal)
		put(cwd, ".pi/mcp.json", c.MCPProject)

		merge := loadAndMergeConfigs(agentDir, cwd, ext, MergeOptions{IncludeProjectScope: c.IncludeProject})
		check := func(what, got, want string) {
			if got != want {
				t.Errorf("%s %s:\n got %s\nwant %s", c.ID, what, got, want)
			}
		}
		check("global", jsonCfg(merge.Global), want.Global)
		check("project", jsonCfg(merge.Project), want.Project)
		check("merged", mergedJSON(merge.Merged), want.Merged)
		check("runtime", runtimeJSON(normalizePermissionSystemConfig(merge.Merged)), want.Runtime)
		if g := scrubList(merge.Issues, tmp); !reflect.DeepEqual(g, orEmpty(want.Issues)) {
			t.Errorf("%s issues:\n got %q\nwant %q", c.ID, g, want.Issues)
		}

		trusted := c.IncludeProject == nil || *c.IncludeProject
		opts := PolicyLoaderOptions{GlobalConfigPath: filepath.Join(agentDir, "extensions/pi-permission-system/config.json"),
			AgentsDir: filepath.Join(agentDir, "agents"), GlobalMcpConfigPath: filepath.Join(agentDir, "mcp.json")}
		if c.IncludeProject != nil && !*c.IncludeProject {
			trusted = false
		}
		if trusted {
			opts.ProjectGlobalConfigPath = getProjectConfigPath(cwd)
			opts.ProjectAgentsDir = getProjectAgentsDir(cwd)
			opts.ProjectMcpConfigPath = filepath.Join(cwd, ".pi", "mcp.json")
		}
		loader := NewFilePolicyLoader(opts)
		check("scope global", scopeJSON(loader.LoadGlobalConfig()), want.Scopes.Global)
		check("scope project", scopeJSON(loader.LoadProjectConfig()), want.Scopes.Project)
		for _, n := range c.AgentNames {
			w := want.Scopes.Agents[n]
			check("agent "+n, scopeJSON(loader.LoadAgentConfig(n)), w.Agent)
			check("project agent "+n, scopeJSON(loader.LoadProjectAgentConfig(n)), w.ProjectAgent)
		}
		if g := loader.GetConfiguredMcpServerNames(); !reflect.DeepEqual(orEmpty(g), orEmpty(want.Scopes.MCP)) {
			t.Errorf("%s mcp names:\n got %q\nwant %q", c.ID, g, want.Scopes.MCP)
		}
		if g := scrubList(loader.GetConfigIssues(), tmp); !reflect.DeepEqual(g, orEmpty(want.Scopes.Issues)) {
			t.Errorf("%s loader issues:\n got %q\nwant %q", c.ID, g, want.Scopes.Issues)
		}
		paths := loader.GetResolvedPolicyPaths()
		var gotPaths map[string]any
		b, _ := json.Marshal(map[string]any{
			"globalConfigPath": paths.GlobalConfigPath, "globalConfigExists": paths.GlobalConfigExists, "projectConfigPath": paths.ProjectConfigPath,
			"projectConfigExists": paths.ProjectConfigExists, "agentsDir": paths.AgentsDir, "agentsDirExists": paths.AgentsDirExists,
			"projectAgentsDir": paths.ProjectAgentsDir, "projectAgentsDirExists": paths.ProjectAgentsDirExists})
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(b), strings.ReplaceAll(tmp, `\`, `\\`), "<TMP>")), &gotPaths)
		if !reflect.DeepEqual(gotPaths, want.Scopes.Paths) {
			t.Errorf("%s resolved paths:\n got %v\nwant %v", c.ID, gotPaths, want.Scopes.Paths)
		}
	}
}

func TestYamlFrontmatterAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.Yaml) != len(r.Cases.Yaml) || len(r.Yaml) < 90 {
		t.Fatalf("%d recorded yaml cases", len(r.Yaml))
	}
	for i, want := range r.Yaml {
		c := r.Cases.Yaml[i]
		fm := extractFrontmatter(c.Markdown)
		if fm != want.Frontmatter {
			t.Errorf("%s %q: frontmatter %q, want %q", c.ID, c.Markdown, fm, want.Frontmatter)
		}
		if g := marshalJSON(parseSimpleYamlMap(fm), ""); g != want.Map {
			t.Errorf("%s %q: map\n got %s\nwant %s", c.ID, c.Markdown, g, want.Map)
		}
		if g := marshalJSON(parseSimpleYamlMap(c.Markdown), ""); g != want.Whole {
			t.Errorf("%s %q: whole-text map\n got %s\nwant %s", c.ID, c.Markdown, g, want.Whole)
		}
	}
}

func TestDialogKeysAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.DialogKeys) != len(r.Cases.DialogKeys) || len(r.DialogKeys) < 400 {
		t.Fatalf("%d recorded dialog-key cases", len(r.DialogKeys))
	}
	for i, want := range r.DialogKeys {
		c := r.Cases.DialogKeys[i]
		o := DialogKeyOverrides{}
		for k, v := range c.Overrides {
			o[PromptAction(k)] = v
		}
		got := resolveDialogKeys(o)
		keys := newObject()
		for _, a := range actionOrder {
			keys.set(string(a), got.Keys[a])
		}
		if g := marshalJSON(keys, ""); g != want.Keys {
			t.Errorf("%s keys %v:\n got %s\nwant %s", c.ID, c.Overrides, g, want.Keys)
		}
		if !reflect.DeepEqual(got.Issues, orEmpty(want.Issues)) {
			t.Errorf("%s issues %v:\n got %q\nwant %q", c.ID, c.Overrides, got.Issues, want.Issues)
		}
	}
}

// storeProbeLogger drops the store's log entries.
type storeProbeLogger struct{}

func (storeProbeLogger) Debug(string, *jsObject)  {}
func (storeProbeLogger) Review(string, *jsObject) {}

type probePaths struct{}

func (probePaths) GetResolvedPolicyPaths() ResolvedPolicyPaths { return ResolvedPolicyPaths{} }

type recordingUI struct {
	notes    [][]string
	statuses [][]any
	hasUI    bool
	custom   int
}

func (u *recordingUI) Notify(m, l string) { u.notes = append(u.notes, []string{m, l}) }
func (u *recordingUI) SetStatus(k, v string) {
	var val any = v
	if v == "" {
		val = nil
	}
	u.statuses = append(u.statuses, []any{k, val})
}
func (u *recordingUI) HasUI() bool                  { return u.hasUI }
func (u *recordingUI) Custom(any, any) (any, error) { u.custom++; return nil, nil }

// extrasFrom is normalizePermissionSystemConfig on the loosely typed object a caller might hand to save().
func extrasFrom(o *jsObject) PermissionSystemExtensionConfig {
	c := PermissionSystemExtensionConfig{DebugLog: o.vals["debugLog"] == true, PermissionReviewLog: o.vals["permissionReviewLog"] != false,
		YoloMode: o.vals["yoloMode"] == true, DoublePressToConfirm: o.vals["doublePressToConfirm"] != false}
	strs := func(k string) []string {
		if a, ok := o.vals[k].([]any); ok {
			out := []string{}
			for _, e := range a {
				out = append(out, e.(string))
			}
			return out
		}
		return nil
	}
	num := func(k string) *float64 {
		if f, ok := o.vals[k].(float64); ok {
			return &f
		}
		return nil
	}
	c.PiInfrastructureReadPaths = strs("piInfrastructureReadPaths")
	c.ForwardingTimeoutMs, c.PromptMaxRows = num("forwardingTimeoutMs"), num("promptMaxRows")
	c.ShellTools = o.obj("shellTools")
	return c
}

func TestConfigStoreSaveAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.Saves) != len(r.Cases.Saves) || len(r.Saves) < 15 {
		t.Fatalf("%d recorded saves", len(r.Saves))
	}
	for i, want := range r.Saves {
		c := r.Cases.Saves[i]
		tmp := t.TempDir()
		agentDirPath := filepath.Join(tmp, "agent")
		file := filepath.Join(agentDirPath, "extensions", "pi-permission-system", "config.json")
		if c.Existing != nil {
			writeTestFile(t, file, *c.Existing)
		}
		store := NewConfigStore(ConfigStoreDeps{AgentDir: agentDirPath, PolicyPaths: probePaths{}, Logger: storeProbeLogger{}})
		ui := &recordingUI{}
		v, err := parseJSON(c.Next)
		if err != nil {
			t.Fatal(err)
		}
		store.Save(extrasFrom(v.(*jsObject)), ui)
		var got *string
		if b, err := os.ReadFile(file); err == nil {
			s := string(b)
			got = &s
		}
		if (got == nil) != (want.File == nil) || (got != nil && *got != *want.File) {
			t.Errorf("%s file:\n got %v\nwant %v", c.ID, got, want.File)
		}
		gotNotes := [][]string{}
		for _, n := range ui.notes {
			gotNotes = append(gotNotes, []string{strings.ReplaceAll(n[0], tmp, "<TMP>"), n[1]})
		}
		if len(gotNotes) != len(want.Notes) || (len(gotNotes) > 0 && !reflect.DeepEqual(gotNotes, want.Notes)) {
			t.Errorf("%s notes: got %v want %v", c.ID, gotNotes, want.Notes)
		}
		if g, _ := json.Marshal(ui.statuses); string(g) != mustJSON(want.Statuses) && !(len(ui.statuses) == 0 && len(want.Statuses) == 0) {
			t.Errorf("%s statuses: got %s want %s", c.ID, g, mustJSON(want.Statuses))
		}
		if g := runtimeJSON(store.Current()); g != want.Current {
			t.Errorf("%s current:\n got %s\nwant %s", c.ID, g, want.Current)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestPermissionSystemCommandAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	if len(r.Commands) != len(r.Cases.Commands) || len(r.Commands) < 12 {
		t.Fatalf("%d recorded command scenarios", len(r.Commands))
	}
	for i, want := range r.Commands {
		c := r.Cases.Commands[i]
		tmp := t.TempDir()
		agentDirPath := filepath.Join(tmp, "agent")
		store := NewConfigStore(ConfigStoreDeps{AgentDir: agentDirPath, PolicyPaths: probePaths{}, Logger: storeProbeLogger{}})
		if len(c.Config) > 0 && string(c.Config) != "null" {
			writeTestFile(t, filepath.Join(agentDirPath, "extensions", "pi-permission-system", "config.json"), string(c.Config))
			store.Refresh("", false)
		}
		controller := PermissionSystemConfigController{Config: store, ConfigPath: "/cfg/config.json", GetActiveAgentConfigRules: func() Ruleset { return c.Rules }}
		for j, a := range c.Args {
			ui := &recordingUI{hasUI: c.HasUI == nil || *c.HasUI}
			if err := runPermissionSystemCommand(ui, a, controller); err != nil {
				t.Fatal(err)
			}
			w := want.Results[j]
			gotNotes := [][]string{}
			for _, n := range ui.notes {
				gotNotes = append(gotNotes, []string{strings.ReplaceAll(n[0], tmp, "<TMP>"), n[1]})
			}
			if len(gotNotes) != len(w.Notes) || (len(gotNotes) > 0 && !reflect.DeepEqual(gotNotes, w.Notes)) {
				t.Errorf("%s %q notes:\n got %q\nwant %q", c.ID, a, gotNotes, w.Notes)
			}
			if ui.custom != w.CustomCalls {
				t.Errorf("%s %q: %d ui.custom calls, want %d", c.ID, a, ui.custom, w.CustomCalls)
			}
			if g := runtimeJSON(store.Current()); g != w.Current {
				t.Errorf("%s %q current:\n got %s\nwant %s", c.ID, a, g, w.Current)
			}
		}
		for j, p := range c.Completions {
			var wantItems []map[string]any
			_ = json.Unmarshal(want.Completions[j][1], &wantItems)
			var got []map[string]any
			for _, it := range getArgumentCompletions(p) {
				got = append(got, map[string]any{"value": it.Value, "label": it.Label, "description": it.Description})
			}
			if !reflect.DeepEqual(got, wantItems) {
				t.Errorf("%s completions for %q:\n got %v\nwant %v", c.ID, p, got, wantItems)
			}
		}
	}
}

func TestPiTuiFactsAgainstOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	t.Run("the bindable characters", func(t *testing.T) {
		want := append([]string{}, r.PiTui.Bindable...)
		var got []string
		for _, c := range bindableDialogKeyCharacters {
			got = append(got, string(c))
		}
		sort.Strings(want)
		sort.Strings(got)
		eq(t, got, want, "BINDABLE_DIALOG_KEY_CHARACTERS")
	})
	t.Run("pi-tui's symbol keys, minus +", func(t *testing.T) {
		var admitted, wantAdmitted []string
		for _, s := range r.PiTui.SymbolKeys {
			if isBindableDialogKey(s) {
				admitted = append(admitted, s)
			}
			if s != "+" {
				wantAdmitted = append(wantAdmitted, s)
			}
		}
		if len(r.PiTui.SymbolKeys) <= 20 {
			t.Fatal("the derivation is vacuous")
		}
		eq(t, admitted, wantAdmitted, "admitted symbols")
	})
	t.Run("pi-tui's matcher accepts every bindable character and not +", func(t *testing.T) {
		for c, ok := range r.PiTui.Matches {
			if bindable := isBindableDialogKey(c); bindable && !ok {
				t.Errorf("%q is bindable but pi-tui's matcher rejects it", c)
			}
		}
		eq(t, r.PiTui.Matches["+"], false, "matchesKey('+', '+')")
	})
}

func TestGeneratedSchemaMatchesTheOriginal(t *testing.T) {
	r := loadConfigOracle(t)
	want, err := parseJSON(r.Schema)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, marshalJSON(buildPermissionsJSONSchema(), ""), marshalJSON(want, ""), "the generated schema")
}
