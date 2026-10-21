package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Layer-1 tests for the configuration at run time: the files PiG's host points at, the trust decision, the reload when
// a file changes, and the /warden config command.

type cfgOpts struct {
	user, project string
	trusted       bool
	noUI          bool
	env           map[string]string
}

func startCfg(t *testing.T, o cfgOpts) (*harness, string) {
	t.Helper()
	h := &harness{t: t, ts: newFakeTypeSafe(t), home: t.TempDir(), confirm: true, model: map[string]any{"id": "mod", "provider": "prov", "name": "Mod"}}
	t.Setenv("PIG_HOME", h.home)
	cwd := t.TempDir()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if o.user != "" {
		write(ConfigPath(h.home), o.user)
	}
	if o.project != "" {
		write(filepath.Join(cwd, ".pig", ProjectConfigFile), o.project)
	}
	getenv := func(k string) string { return o.env[k] }
	hasUI := !o.noUI
	ext, w := newWarden(Options{Getenv: getenv})
	h.w = w
	h.host = StartHost(t, ext, HostOptions{HasUI: &hasUI, Cwd: cwd, OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "isProjectTrusted" {
			return map[string]any{"trusted": o.trusted}, ""
		}
		return h.onCall(method, args)
	}})
	// As startHarness: background rules and learning writes must be over before the temporary directories are removed.
	t.Cleanup(w.rulesShutdown)
	t.Cleanup(func() { w.learnFlush(); CloseLearningDatabases() })
	return h, cwd
}

func (h *harness) configStatusText() string {
	h.t.Helper()
	if msg := h.host.Command("warden", ""); msg != "" {
		h.t.Fatalf("/warden: %s", msg)
	}
	var last string
	for _, m := range h.messages() {
		if m.Type == statusType {
			last = m.Content
		}
	}
	return last
}

func TestConfigReloadsWhenTheFileChanges(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true,"mode":"advise"}`})
	h.start()
	if got := h.configStatusText(); !strings.Contains(got, "advise mode") {
		t.Fatalf("status %q", got)
	}
	if err := os.WriteFile(ConfigPath(h.home), []byte(`{"enabled":true,"mode":"confirm"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := h.configStatusText(); !strings.Contains(got, "confirm mode") {
		t.Errorf("an edit to the file did not take effect: %q", got)
	}
}

func TestTrustedProjectFileIsReadAndAWeakerValueIsIgnoredWithAWarning(t *testing.T) {
	h, cwd := startCfg(t, cfgOpts{user: `{"enabled":true}`, project: `{"security":{"enabled":false},"stuck":{"window":7}}`, trusted: true})
	h.start()
	got := h.configStatusText()
	for _, want := range []string{"Config: " + ConfigPath(h.home) + " and " + filepath.Join(cwd, ".pig", ProjectConfigFile), "project file: security.enabled false is ignored"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	// The warning is said once per session, at its start.
	var said int
	for _, n := range h.notices() {
		if strings.Contains(n, "config warnings") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("config warnings said %d times: %v", said, h.notices())
	}
}

func TestUntrustedProjectFileIsNotRead(t *testing.T) {
	h, cwd := startCfg(t, cfgOpts{user: `{"enabled":true}`, project: `{"security":{"enabled":false}}`, trusted: false})
	h.start()
	got := h.configStatusText()
	if strings.Contains(got, filepath.Join(cwd, ".pig")) || strings.Contains(got, "project file:") {
		t.Errorf("an untrusted project was read:\n%s", got)
	}
}

func TestHostileUserFileStartsCleanAndARefusedBackendIsSaid(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true,"mode":"loud","action":"x","consent":7,"backend":"azure","stuck":[],"widget":{"shortcut":3}}`})
	h.start()
	var said bool
	for _, n := range h.notices() {
		if strings.Contains(n, `judgments are off: Unknown judgment backend "azure"`) {
			said = true
		}
	}
	if !said {
		t.Errorf("the refused backend was not said: %v", h.notices())
	}
	if got := h.configStatusText(); !strings.Contains(got, "steer mode") || !strings.Contains(got, `Judgments are off: Unknown judgment backend "azure"`) {
		t.Errorf("status %q", got)
	}
}

func TestMalformedUserFileIsReportedAndNeverTurnsWardenOn(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{ not json`})
	h.start()
	if len(h.notices()) == 0 || !strings.Contains(h.notices()[0], "could not read") {
		t.Errorf("notices %v", h.notices())
	}
	if got := h.status(); got != "warden: off · /warden enable" {
		t.Errorf("status %q", got)
	}
}

func TestACommandWritesOnlyWhatItChanged(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true,"mode":"advise","unknown":{"a":1}}`})
	h.start()
	if msg := h.host.Command("warden", "mode steer"); msg != "" {
		t.Fatal(msg)
	}
	data, _ := os.ReadFile(ConfigPath(h.home))
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	eq(t, "file", got, map[string]any{"enabled": true, "mode": "steer", "unknown": map[string]any{"a": 1.0}})
}

func TestWardenConfigSetGetAndUsage(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true}`, noUI: true})
	h.start()
	out := func(args string) string {
		t.Helper()
		before := len(h.messages())
		if msg := h.host.Command("warden", args); msg != "" {
			t.Fatalf("/warden %s: %s", args, msg)
		}
		all := h.messages()
		if len(all) == before {
			t.Fatalf("/warden %s said nothing", args)
		}
		return all[len(all)-1].Content
	}
	eq(t, "bare set", out("config set"), "Usage: /warden config set <key> <value> or /warden config get <key>")
	eq(t, "bare get", out("config get"), "Usage: /warden config set <key> <value> or /warden config get <key>")
	eq(t, "set without a value", out("config set action.failOpen"), "Usage: /warden config set <key> <value>")
	eq(t, "set", out("config set action.irreversible.confirm 0.8"), "Saved action.irreversible.confirm = 0.8.")
	eq(t, "get", out("config get action.irreversible.confirm"), "action.irreversible.confirm = 0.8")
	eq(t, "get object", out("config get action.irreversible"), `action.irreversible = {"confirm":0.8}`)
	eq(t, "get unset default", out("config get action.failOpen"), "action.failOpen not set (default: true)")
	eq(t, "get unset object default keeps the original's key order", out("config get action.offTask"), `action.offTask not set (default: {"warn":0.6,"steer":0.85})`)
	eq(t, "get nothing", out("config get nope.nothing"), "nope.nothing not set (default: undefined)")
	eq(t, "string value", out("config set widget.placement belowEditor"), `Saved widget.placement = "belowEditor".`)
	eq(t, "value keeps inner spaces collapsed to one, as the original joins its tokens", out("config set widget.action a   b"), `Saved widget.action = "a b".`)
	eq(t, "null", out("config set learning.retentionDays null"), "Saved learning.retentionDays = null.")
	eq(t, "null is set, not absent", out("config get learning.retentionDays"), "learning.retentionDays = null")
	data, _ := os.ReadFile(ConfigPath(h.home))
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	eq(t, "file", got["action"], map[string]any{"irreversible": map[string]any{"confirm": 0.8}})
	eq(t, "no UI says where the file is", out("config"), "Edit "+ConfigPath(h.home)+" directly. Use /warden config set <key> <value> for quick changes.")
	// The change took effect: the loader reads it back.
	cfg, _ := LoadConfig(ConfigPath(h.home))
	eq(t, "loaded", cfg.Action.Irreversible.Confirm, 0.8)
}

func TestTheTraceShortcutIsRegisteredFromTheUserFile(t *testing.T) {
	// The default key, with the description the original gives it.
	h, _ := startCfg(t, cfgOpts{})
	eq(t, "default", h.host.Shortcuts(), map[string]string{"ctrl+shift+w": "Toggle the pi-warden trace sidebar"})

	h, _ = startCfg(t, cfgOpts{user: `{"widget":{"shortcut":"alt+t"}}`})
	eq(t, "configured", h.host.Shortcuts(), map[string]string{"alt+t": "Toggle the pi-warden trace sidebar"})
	h, _ = startCfg(t, cfgOpts{user: `{"widget":{"shortcut":"F9"}}`})
	eq(t, "function key", h.host.Shortcuts(), map[string]string{"F9": "Toggle the pi-warden trace sidebar"})
	for _, bad := range []string{`""`, `"hello"`, `"ctrl+"`, `"ctrl+shift+ww w"`, `3`} {
		h, _ = startCfg(t, cfgOpts{user: `{"widget":{"shortcut":` + bad + `}}`})
		eq(t, "shape check "+bad, len(h.host.Shortcuts()), func() int {
			if bad == `3` {
				return 1 // a non-string is junk: the default key applies
			}
			return 0
		}())
	}
}

func TestPressingTheShortcutWithoutAUIDoesNothing(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{noUI: true})
	h.start()
	if msg := h.host.Shortcut("ctrl+shift+w"); msg != "" {
		t.Fatal(msg)
	}
	if len(h.host.CallsTo("ui.custom")) != 0 {
		t.Error("a panel opened without a UI")
	}
}

func TestTraceCommandHeadlessPrintsTheTraceAsText(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true}`, noUI: true})
	h.start()
	if msg := h.host.Command("warden", "trace"); msg != "" {
		t.Fatal(msg)
	}
	var last string
	for _, m := range h.messages() {
		last = m.Content
	}
	eq(t, "empty trace", last, "No guarded activity yet this session.")
}

func TestTraceFileIsWrittenWhenTheDirectoryIsSet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "traces")
	h, _ := startCfg(t, cfgOpts{user: `{"enabled":true}`, env: map[string]string{TraceDirEnv: dir}})
	h.start()
	h.say("user", "list the files")
	h.toolCall("bash", "c1", map[string]any{"command": "rm -rf /"})
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("trace files: %v %v", files, err)
	}
	var records []map[string]any
	waitUntil(t, func() bool {
		data, _ := os.ReadFile(files[0])
		records = nil
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				records = append(records, m)
			}
		}
		return len(records) >= 2
	})
	eq(t, "session line", records[0]["kind"], "session")
	eq(t, "judgments", records[0]["judgments"], "off:no_consent")
	eq(t, "entry", records[1]["kind"], "entry")
	eq(t, "guard", records[1]["guard"], "action")
}

// twin: tests/host-tui.test.ts:93
// The original mounts every guard on a host whose TUI has a mouse region: 14 event hooks, one command, at least one
// shortcut. The port's event set is its own and grows with each lane, so the count is not pinned; what carries over is that
// the extension registers /warden, the toggle shortcut and its session hooks on a host with a TUI, and that a first
// session_start runs without a database or a developer's data (the port keeps none at start).
func TestEveryPartMountsOnAHostWithATUI(t *testing.T) {
	h, _ := startCfg(t, cfgOpts{})
	if !h.host.Registered("session_start") || !h.host.Registered("tool_call") || !h.host.Registered("tool_result") || !h.host.Registered("agent_end") {
		t.Error("the guard hooks are not all registered")
	}
	if len(h.host.cmds) != 1 || !h.host.cmds["warden"] {
		t.Errorf("commands %v", h.host.cmds)
	}
	if len(h.host.Shortcuts()) < 1 {
		t.Error("no shortcut registered")
	}
	h.start()
	if _, err := os.Stat(filepath.Join(h.home, "pigpen-warden")); err == nil {
		t.Error("session_start created the data directory")
	}
}
