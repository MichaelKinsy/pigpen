package pig_doctor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pigdoctor "github.com/MichaelKinsy/pigpen/pig-doctor"
	"github.com/MichaelKinsy/pigpen/pig-doctor/doctor"
)

type noProcs struct{}

func (noProcs) List() ([]doctor.Proc, error) { return nil, nil }

// home builds a fixture PiG home (rule 17: nothing outside the temp directory is read).
type home struct{ root, pig, user, agent string }

func newHome(t *testing.T) home {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	h := home{root: root, pig: filepath.Join(root, "pig"), user: filepath.Join(root, "user")}
	h.agent = filepath.Join(h.pig, "agent")
	pkg := filepath.Join(root, "user", "p")
	write(t, filepath.Join(pkg, "package.json"), `{"name":"p","version":"1.0.0","pi":{"extensions":["extensions/*"]}}`)
	write(t, filepath.Join(pkg, "extensions", "ask", "go.mod"), "module x/ask\n\ngo 1.26\n")
	write(t, filepath.Join(h.agent, "settings.json"), `{"theme":"dark","packages":["`+pkg+`","`+pkg+`/"]}`)
	write(t, filepath.Join(h.pig, "extensions", "old", "go.mod"), "module x/old\n\ngo 1.26\n")
	past := time.Now().Add(-400 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(h.pig, "extensions"), past, past)
	return h
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h home) loader() pigdoctor.Loader {
	return func() (doctor.Options, error) {
		return doctor.Options{Home: h.pig, AgentDir: h.agent, UserHome: h.user, Env: []string{"PATH=" + filepath.Join(h.root, "nobin"), "HOME=" + h.user}, Procs: noProcs{}}, nil
	}
}

func messages(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		s, _ := m["content"].(string)
		out = append(out, s)
	}
	return out
}

func TestRegistersOnlyACommandAndATool(t *testing.T) {
	h := StartHost(t, pigdoctor.New(newHome(t).loader()), HostOptions{})
	if !h.cmds["doctor"] || !h.tools["pig_doctor"] {
		t.Fatalf("commands %v tools %v", h.cmds, h.tools)
	}
	if len(h.handlers) != 0 {
		t.Errorf("nothing may run automatically, but handlers are registered: %v", h.handlers)
	}
	if len(h.cmds) != 1 || len(h.tools) != 1 {
		t.Errorf("unexpected surface: %v %v", h.cmds, h.tools)
	}
}

func TestCommandCheckIsReadOnly(t *testing.T) {
	hm := newHome(t)
	before, _ := os.ReadFile(filepath.Join(hm.agent, "settings.json"))
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{})
	if fail := h.Command("doctor", ""); fail != "" {
		t.Fatal(fail)
	}
	msgs := messages(h)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "pkg.duplicate") || !strings.Contains(msgs[0], "legacy.extensions-dir") {
		t.Fatalf("%v", msgs)
	}
	after, _ := os.ReadFile(filepath.Join(hm.agent, "settings.json"))
	if string(before) != string(after) || !exists(filepath.Join(hm.pig, "extensions")) {
		t.Error("check changed something")
	}
	if len(h.CallsTo("ui.confirm")) != 0 {
		t.Error("check must not open dialogs")
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestToolIsReadOnlyEvenWhenAskedToFix(t *testing.T) {
	hm := newHome(t)
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{})
	for _, params := range []map[string]any{{}, {"mode": "check"}, {"mode": "plan"}, {"mode": "plan", "groups": []any{"packages"}}, {"mode": "backups"}} {
		raw, fail := h.Tool("pig_doctor", params)
		if fail != "" {
			t.Fatalf("%v: %s", params, fail)
		}
		if len(raw) == 0 {
			t.Errorf("%v: empty result", params)
		}
	}
	if _, fail := h.Tool("pig_doctor", map[string]any{"mode": "fix"}); fail == "" {
		t.Error("the tool has no fix mode")
	}
	if !exists(filepath.Join(hm.pig, "extensions")) {
		t.Error("the tool changed the home")
	}
	raw, _ := h.Tool("pig_doctor", map[string]any{"mode": "plan"})
	var s string
	_ = json.Unmarshal(raw, &s)
	if !strings.Contains(s, "DRY RUN") && !strings.Contains(string(raw), "DRY RUN") {
		t.Errorf("plan: %s", raw)
	}
	if _, fail := h.Tool("pig_doctor", map[string]any{"mode": "plan", "groups": []any{"nope"}}); fail == "" {
		t.Error("an unknown group is an error")
	}
}

func TestFixNeedsAnInteractiveSession(t *testing.T) {
	hm := newHome(t)
	no := false
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{Mode: "print", HasUI: &no})
	if fail := h.Command("doctor", "fix"); fail == "" {
		t.Error("fix without a UI must fail")
	}
	if !exists(filepath.Join(hm.pig, "extensions")) || len(h.CallsTo("ui.confirm")) != 0 {
		t.Error("nothing may change and no dialog may open")
	}
	if m := messages(h); len(m) != 1 || !strings.Contains(m[0], "DRY RUN") {
		t.Errorf("a dry run should be shown: %v", m)
	}
}

func TestFixConfirmsEachGroupInADialog(t *testing.T) {
	hm := newHome(t)
	answers := map[string]bool{}
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			title, _ := args["title"].(string)
			ok := strings.Contains(title, "duplicate Package")
			answers[title] = ok
			return map[string]any{"confirmed": ok}, ""
		}
		return nil, ""
	}})
	if fail := h.Command("doctor", "fix"); fail != "" {
		t.Fatal(fail)
	}
	if len(answers) < 2 {
		t.Fatalf("each group is confirmed separately: %v", answers)
	}
	settings, _ := os.ReadFile(filepath.Join(hm.agent, "settings.json"))
	if strings.Count(string(settings), "/user/p") != 1 {
		t.Errorf("the duplicate should be gone: %s", settings)
	}
	if !strings.Contains(string(settings), `"theme":"dark"`) {
		t.Errorf("other keys stay: %s", settings)
	}
	if !exists(filepath.Join(hm.pig, "extensions")) {
		t.Error("the declined legacy group must not run")
	}
	if len(h.CallsTo("ui.input")) != 0 {
		t.Error("no credentials in this home")
	}
}

func TestCredentialGroupNeedsTheTypedPhrase(t *testing.T) {
	hm := newHome(t)
	write(t, filepath.Join(hm.pig, "x-agent", "auth.json"), "SECRET")
	write(t, filepath.Join(hm.pig, "x-agent", "settings.json"), "{}")
	past := time.Now().Add(-60 * 24 * time.Hour)
	for _, p := range []string{"x-agent/auth.json", "x-agent/settings.json", "x-agent"} {
		_ = os.Chtimes(filepath.Join(hm.pig, p), past, past)
	}
	typed := "yes"
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "ui.confirm":
			return map[string]any{"confirmed": false}, ""
		case "ui.input":
			return map[string]any{"text": typed, "ok": true}, ""
		}
		return nil, ""
	}})
	if fail := h.Command("doctor", "fix --group credentials"); fail != "" {
		t.Fatal(fail)
	}
	if !exists(filepath.Join(hm.pig, "x-agent", "auth.json")) {
		t.Fatal("a wrong phrase must not delete anything")
	}
	if len(h.CallsTo("ui.input")) != 1 {
		t.Errorf("credentials are confirmed with a typed phrase, not a yes/no dialog")
	}
	typed = doctor.CredentialsPhrase
	if fail := h.Command("doctor", "fix --group credentials"); fail != "" {
		t.Fatal(fail)
	}
	if exists(filepath.Join(hm.pig, "x-agent", "auth.json")) {
		t.Error("the typed phrase deletes the credential copy")
	}
	for _, m := range messages(h) {
		if strings.Contains(m, "SECRET") {
			t.Error("leak")
		}
	}
}

func TestRestoreThroughTheCommand(t *testing.T) {
	hm := newHome(t)
	h := StartHost(t, pigdoctor.New(hm.loader()), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			return map[string]any{"confirmed": true}, ""
		}
		return nil, ""
	}})
	if fail := h.Command("doctor", "fix --group legacy"); fail != "" {
		t.Fatal(fail)
	}
	if exists(filepath.Join(hm.pig, "extensions")) {
		t.Fatal("legacy group should have run")
	}
	bs, err := doctor.ListBackups(mustOpts(t, hm))
	if err != nil || len(bs) != 1 {
		t.Fatalf("%v %v", bs, err)
	}
	if fail := h.Command("doctor", "restore "+bs[0].Timestamp); fail != "" {
		t.Fatal(fail)
	}
	if !exists(filepath.Join(hm.pig, "extensions", "old", "go.mod")) {
		t.Error("not restored")
	}
	if fail := h.Command("doctor", "restore ../../etc"); fail == "" {
		t.Error("a bad timestamp must fail")
	}
	if fail := h.Command("doctor", "bogus"); fail == "" {
		t.Error("unknown subcommand must fail")
	}
}

func mustOpts(t *testing.T, h home) doctor.Options {
	o, err := h.loader()()
	if err != nil {
		t.Fatal(err)
	}
	return o
}
